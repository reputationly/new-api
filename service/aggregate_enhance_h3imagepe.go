package service

import (
	"context"
	_ "embed"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

//go:embed h3imagepe/edit_task_prompt.txt
var h3ImagePEEditTask string

//go:embed h3imagepe/t2i_system_prompt.txt
var h3ImagePET2ISystem string

//go:embed h3imagepe/t2i_task_prompt.txt
var h3ImagePET2ITask string

//go:embed h3imagepe/edit_appendix.txt
var h3ImagePEEditAppendix string

//go:embed h3imagepe/t2i_appendix.txt
var h3ImagePET2IAppendix string

const (
	h3ImagePETimeout         = 120 * time.Second
	h3ImagePEMaxRepairRounds = 2
)

var (
	h3ImagePERatios    = []string{"1:1", "5:4", "4:3", "3:2", "16:9", "21:9", "4:5", "3:4", "2:3", "9:16"}
	h3ImagePERatio     = regexp.MustCompile(`^[1-9][0-9]*:[1-9][0-9]*$`)
	h3ImagePESubject   = regexp.MustCompile(`<Subject ([0-9]+)>`)
	h3ImagePEPicture   = regexp.MustCompile(`<Picture ([0-9]+)>`)
	h3ImagePECJK       = regexp.MustCompile(`[\x{3400}-\x{9fff}\x{3040}-\x{30ff}\x{ac00}-\x{d7af}]`)
	h3ImagePEFence     = regexp.MustCompile("```|\\*\\*|(?m)^#+ |(?m)^\\s*[-*] ")
	h3ImagePESentence  = regexp.MustCompile(`([.!?])([\t\n\f\r\x{000b}\p{Z}]+)([A-Z<'"(])`)
	h3ImagePEGrid      = regexp.MustCompile(`\b[0-9]+\s*[x×]\s*[0-9]+\b`)
	h3ImagePEGridLabel = regexp.MustCompile(
		`\b[0-9]+ rows? and [0-9]+ columns?|\b(one|two|three|four|five|six|seven|eight|nine|ten) rows? and (one|two|three|four|five|six|seven|eight|nine|ten) columns?`,
	)
	h3ImagePEGridSizeSuffix    = regexp.MustCompile(`^ (px|pixel)`)
	h3ImagePERangeToken        = regexp.MustCompile(`<((Subject|Picture)\s*[0-9]+\s*(-|–|to|through)\s*|Pictures? [0-9]+>\s*(-|–|to|through)\s*<Picture)`)
	h3ImagePEBadToken          = regexp.MustCompile(`<([A-Za-z][^<>]{0,30})>`)
	h3ImagePEGoodToken         = regexp.MustCompile(`^(Subject|Picture) [0-9]+$`)
	h3ImagePEPartial           = regexp.MustCompile(`<Subject [0-9]+> (is|are) partially preserved[;,:]`)
	h3ImagePETransfer          = regexp.MustCompile(`\b(suppl(y|ies)|transferred|transfers?|provides?|donates?)\b`)
	h3ImagePERemoved           = regexp.MustCompile(`\b(removed|absent|deleted|no longer (appears|present)|not used|unused)\b`)
	h3ImagePEFullClause        = regexp.MustCompile(`<Subject [0-9]+> is fully preserved\.`)
	h3ImagePEFullDirty         = regexp.MustCompile(`fully preserved`)
	h3ImagePERelationshipStart = regexp.MustCompile(`^<Subject ([0-9]+)>('s)? `)
	h3ImagePENatural           = regexp.MustCompile(`\b(is|are)\b[^.]*<Subject [0-9]+>|<Subject [0-9]+>[^.]*\b(is|are)\b`)
	h3ImagePEMergedFull        = regexp.MustCompile(`<Subject [0-9]+>(,| and)[^.]*\b(are|is) (all |both |each )?fully preserved`)
	h3ImagePEUnquotedT2I       = regexp.MustCompile(`"[^"]*"|'[^']*'|“[^”]*”`)
)

type h3ImagePEOutput struct {
	Caption string
	Ratio   string
}

type h3ImagePEResult struct {
	Prompt   string
	Size     string
	Warnings []string
	Repaired bool
	Usage    *dto.Usage
}

type h3ImagePESize struct {
	ratio string
	size  string
}

// 与 relay/image_size_policy.go 的 h3ImageAreaBands 2K 档保持一致。
var h3ImagePE2KSizes = []h3ImagePESize{
	{ratio: "1:1", size: "2048x2048"},
	{ratio: "4:3", size: "2368x1760"},
	{ratio: "3:4", size: "1760x2368"},
	{ratio: "16:9", size: "2720x1536"},
	{ratio: "9:16", size: "1536x2720"},
	{ratio: "3:1", size: "3552x1184"},
	{ratio: "1:3", size: "1184x3552"},
}

func h3ImagePEBudget(cfg *common.AggregatePromptEnhance) time.Duration {
	if cfg != nil && cfg.TimeoutSeconds > 0 {
		return time.Duration(cfg.TimeoutSeconds) * time.Second
	}
	return h3ImagePETimeout
}

func compileH3ImagePEWithTimeout(
	ctx context.Context, authHeader, model string, in EnhanceInput, budget time.Duration,
) (*h3ImagePEResult, error) {
	cctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	return compileH3ImagePE(cctx, authHeader, model, in)
}

func compileH3ImagePE(ctx context.Context, authHeader, model string, in EnhanceInput) (*h3ImagePEResult, error) {
	images := make([]string, 0, len(in.ImageURLs))
	for _, imageURL := range in.ImageURLs {
		if strings.TrimSpace(imageURL) != "" {
			images = append(images, imageURL)
		}
	}
	if len(images) > 0 && !in.SendMedia {
		common.SysLog("aggregate enhance: h3_image_pe 未发送输入图,按 T2I 处理")
		images = nil
	}

	userRatio := "adaptive"
	if fixed := qwenPEFixedRatio(in.ClientSize); fixed != "" {
		userRatio = fixed
	}
	edit := len(images) > 0
	messages := buildH3ImagePEMessages(in.Prompt, images, userRatio, in.ClientSize)
	allowedRatios := h3ImagePERatios
	if !edit && userRatio != "adaptive" {
		allowedRatios = []string{userRatio}
	}

	totalUsage := &dto.Usage{}
	var raw string
	var lastIssues []string
	for round := 0; round <= h3ImagePEMaxRepairRounds; round++ {
		requestMessages := messages
		if round > 0 && raw != "" {
			repairIssues := lastIssues
			if edit {
				repairIssues = describeH3ImagePEIssues(lastIssues)
			}
			repair := buildH3ImagePERepair(repairIssues, edit)
			requestMessages = append(append([]map[string]any{}, messages...),
				map[string]any{"role": "assistant", "content": raw},
				map[string]any{"role": "user", "content": repair},
			)
		}

		body := map[string]any{
			"model": model, "messages": requestMessages, "stream": false,
		}
		if !edit {
			body["temperature"] = 1.0
			body["top_p"] = 0.95
		}
		if in.ReasoningEffort != "" {
			body["reasoning_effort"] = in.ReasoningEffort
		} else {
			applyThinking(body, in.Thinking)
		}
		payload, err := common.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("构造改写请求失败: %w", err)
		}
		content, usage, err := callEnhance(ctx, authHeader, payload, in.AggregateName)
		accumulateUsage(totalUsage, usage)
		if err != nil {
			return nil, err
		}
		raw = content
		parsed, issues := validateH3ImagePE(content, edit, len(images), userRatio, allowedRatios)
		lastIssues = issues
		if parsed != nil && len(issues) == 0 {
			result := &h3ImagePEResult{
				Prompt: parsed.Caption, Repaired: round > 0, Usage: totalUsage,
			}
			if strings.TrimSpace(in.ClientSize) == "" {
				size, warning := h3ImagePESizeForRatio(parsed.Ratio)
				result.Size = size
				if warning != "" {
					result.Warnings = append(result.Warnings, warning)
				}
			}
			return result, nil
		}
	}
	return nil, fmt.Errorf("传输检查未通过: %s", strings.Join(describeH3ImagePEIssues(lastIssues), "; "))
}

func buildH3ImagePEMessages(prompt string, images []string, userRatio, clientSize string) []map[string]any {
	fixedRatio := qwenPEFixedRatio(clientSize)
	if len(images) == 0 {
		user := formatH3ImagePET2ITask(prompt) + "\n\n" + strings.TrimSpace(h3ImagePET2IAppendix)
		if fixedRatio != "" {
			user += "\n\nUser Aspect Ratio Selection: " + fixedRatio +
				". Compose the image for exactly this final canvas and output it as aspect_ratio."
		}
		return []map[string]any{
			{"role": "system", "content": strings.TrimRight(h3ImagePET2ISystem, "\n")},
			{"role": "user", "content": user},
		}
	}

	parts := []map[string]any{
		{"type": "text", "text": h3ImagePEEditTask},
		{"type": "text", "text": fmt.Sprintf(
			"Reference Image Count:\n%d input image(s), labeled <Picture 1> through <Picture %d>.",
			len(images), len(images),
		)},
	}
	for i, imageURL := range images {
		ratio := h3ImagePEImageRatio(imageURL)
		parts = append(parts,
			map[string]any{"type": "text", "text": fmt.Sprintf(
				"Reference Media:\n- <Picture %d> [aspect_ratio %s]", i+1, ratio,
			)},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageURL}},
		)
	}
	if userRatio == "" {
		userRatio = "adaptive"
	}
	instruction := h3ImagePEJSONString(prompt)
	finalText := "\nUser Editing Instruction (JSON string):\n" + instruction +
		"\n\nUser Aspect Ratio Selection:\n" + userRatio + "\n\n" +
		strings.TrimSpace(h3ImagePEEditAppendix) +
		"\n\nApply the instructions above to the supplied inputs and reference images. Output only the required two-field JSON object."
	parts = append(parts, map[string]any{"type": "text", "text": finalText})
	return []map[string]any{{"role": "user", "content": parts}}
}

func formatH3ImagePET2ITask(prompt string) string {
	var result strings.Builder
	for i := 0; i < len(h3ImagePET2ITask); {
		switch {
		case strings.HasPrefix(h3ImagePET2ITask[i:], "{{"):
			result.WriteByte('{')
			i += 2
		case strings.HasPrefix(h3ImagePET2ITask[i:], "}}"):
			result.WriteByte('}')
			i += 2
		case strings.HasPrefix(h3ImagePET2ITask[i:], "{}"):
			result.WriteString(prompt)
			i += 2
		default:
			result.WriteByte(h3ImagePET2ITask[i])
			i++
		}
	}
	return result.String()
}

func h3ImagePEJSONString(value string) string {
	encoded, err := common.Marshal(value)
	if err != nil {
		return `""`
	}
	return strings.NewReplacer(
		`\u003c`, "<", `\u003e`, ">", `\u0026`, "&", `\u2028`, "\u2028", `\u2029`, "\u2029",
	).Replace(string(encoded))
}

func h3ImagePEImageRatio(imageURL string) string {
	width, height, err := qwenPEImageDims(imageURL)
	if err != nil || width <= 0 || height <= 0 {
		return "unavailable"
	}
	gcd := width
	for b := height; b != 0; {
		gcd, b = b, gcd%b
	}
	return fmt.Sprintf("%d:%d", width/gcd, height/gcd)
}

func validateH3ImagePE(
	content string, edit bool, imageCount int, userRatio string, allowedRatios []string,
) (*h3ImagePEOutput, []string) {
	keys := []string{"expanded_caption", "aspect_ratio"}
	if edit {
		keys[1] = "target_aspect_ratio"
	}
	parsed, issues := parseH3ImagePEObject(content, keys)
	if parsed == nil {
		return nil, issues
	}
	if edit {
		issues = append(issues, checkH3ImagePEEditCaption(
			parsed["expanded_caption"], parsed["target_aspect_ratio"], imageCount, userRatio,
		)...)
	} else {
		if _, hasCaption := parsed["expanded_caption"]; hasCaption {
			if _, hasRatio := parsed["aspect_ratio"]; hasRatio {
				issues = append(issues, checkH3ImagePET2I(parsed, allowedRatios)...)
			}
		}
	}
	caption, _ := parsed["expanded_caption"].(string)
	ratioKey := "aspect_ratio"
	if edit {
		ratioKey = "target_aspect_ratio"
	}
	ratio, _ := parsed[ratioKey].(string)
	return &h3ImagePEOutput{Caption: caption, Ratio: ratio}, issues
}

func parseH3ImagePEObject(content string, keys []string) (map[string]any, []string) {
	if strings.TrimSpace(content) == "" {
		return nil, []string{"empty_content"}
	}
	for _, candidate := range jsonCandidates(content) {
		var parsed map[string]any
		if err := common.Unmarshal([]byte(candidate), &parsed); err != nil || parsed == nil {
			continue
		}
		var issues []string
		if strings.TrimSpace(candidate) != strings.TrimSpace(content) {
			issues = append(issues, "not_pure_json")
		}
		actual := make([]string, 0, len(parsed))
		for key := range parsed {
			actual = append(actual, key)
		}
		sort.Strings(actual)
		expected := append([]string(nil), keys...)
		sort.Strings(expected)
		if strings.Join(actual, "\x00") != strings.Join(expected, "\x00") {
			quotedKeys := make([]string, len(actual))
			for i, key := range actual {
				quotedKeys[i] = "'" + key + "'"
			}
			return parsed, append(issues, "keys=["+strings.Join(quotedKeys, ", ")+"]")
		}
		return parsed, issues
	}
	var value any
	if err := common.Unmarshal([]byte(strings.TrimSpace(content)), &value); err == nil {
		return nil, []string{"not_object"}
	}
	return nil, []string{"not_pure_json", "json_unrecoverable"}
}

func checkH3ImagePEEditCaption(caption, ratio any, imageCount int, userRatio string) []string {
	var issues []string
	text, ok := caption.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return []string{"empty_caption"}
	}
	if strings.Contains(strings.TrimSpace(text), "\n") {
		issues = append(issues, "line_breaks")
	}
	if h3ImagePEFence.MatchString(text) {
		issues = append(issues, "markdown")
	}
	ratioText, ok := ratio.(string)
	if !ok || !h3ImagePERatio.MatchString(ratioText) {
		issues = append(issues, "bad_ratio")
	} else if userRatio != "adaptive" && ratioText != userRatio {
		issues = append(issues, "ratio_ignores_user_selection")
	}

	pictureIDs := uniqueH3ImagePEInts(h3ImagePEPicture.FindAllStringSubmatch(text, -1))
	for _, picture := range pictureIDs {
		if picture < 1 || picture > imageCount {
			issues = append(issues, fmt.Sprintf("picture_out_of_range:%d", picture))
		}
	}
	if h3ImagePERangeToken.MatchString(text) {
		issues = append(issues, "range_reference")
	}
	for _, match := range h3ImagePEBadToken.FindAllStringSubmatch(text, -1) {
		if !h3ImagePEGoodToken.MatchString(match[1]) {
			issues = append(issues, "malformed_token")
			break
		}
	}

	targetIndex := strings.Index(text, "The target image")
	if targetIndex < 0 {
		return append(issues, "no_target_overview")
	}
	if strings.Count(text, "The target image") > 1 {
		issues = append(issues, "multiple_target_overview")
	}
	partOne, rest := text[:targetIndex], text[targetIndex:]
	definitions := make([]int, 0)
	defined := make(map[int]bool)
	hasSubjects := h3ImagePESubject.MatchString(text)
	if hasSubjects {
		for _, sentence := range h3ImagePESplitSentences(partOne) {
			subjectIDs := h3ImagePESubjectIDs(sentence)
			var newSubjects []int
			for _, subject := range subjectIDs {
				if !defined[subject] {
					newSubjects = append(newSubjects, subject)
				}
			}
			if len(newSubjects) == 0 {
				issues = append(issues, "part1_sentence_without_new_subject")
				continue
			}
			if !h3ImagePEPicture.MatchString(sentence) {
				issues = append(issues, fmt.Sprintf("def_without_picture:%d", newSubjects[0]))
			}
			if !h3ImagePENatural.MatchString(sentence) {
				issues = append(issues, fmt.Sprintf("def_not_natural:%d", newSubjects[0]))
			}
			for _, subject := range newSubjects {
				definitions = append(definitions, subject)
				defined[subject] = true
			}
		}
		if len(definitions) == 0 {
			issues = append(issues, "no_subjects")
		}
		for i, subject := range definitions {
			if subject != i+1 {
				issues = append(issues, "subject_numbering")
				break
			}
		}
	}
	referenced := make(map[int]bool)
	for _, subject := range h3ImagePESubjectIDs(rest) {
		referenced[subject] = true
	}
	var undefined []int
	for subject := range referenced {
		if !defined[subject] {
			undefined = append(undefined, subject)
		}
	}
	sort.Ints(undefined)
	for _, subject := range undefined {
		issues = append(issues, fmt.Sprintf("undefined_subject:%d", subject))
	}

	if hasSubjects {
		relationships := make(map[int][]string)
		sentences := h3ImagePESplitSentences(rest)
		for _, sentence := range sentences[1:] {
			match := h3ImagePERelationshipStart.FindStringSubmatch(sentence)
			if match == nil {
				continue
			}
			subject, _ := strconv.Atoi(match[1])
			kind := ""
			switch {
			case h3ImagePEFullClause.MatchString(sentence) && h3ImagePEFullClause.FindString(sentence) == sentence:
				kind = "full"
			case h3ImagePEFullDirty.MatchString(sentence):
				kind = "full_dirty"
			case h3ImagePEPartial.MatchString(sentence):
				kind = "partial"
			case h3ImagePETransfer.MatchString(sentence):
				kind = "transfer"
			case strings.Contains(sentence, "weak") && strings.Contains(sentence, "reference"):
				kind = "weak"
			case h3ImagePERemoved.MatchString(sentence):
				kind = "removed"
			}
			if kind != "" {
				relationships[subject] = append(relationships[subject], kind)
			}
		}
		for _, subject := range definitions {
			kinds := relationships[subject]
			if len(kinds) == 0 {
				issues = append(issues, fmt.Sprintf("no_relationship:%d", subject))
				continue
			}
			preservationKinds := 0
			for _, kind := range kinds {
				if kind == "full" || kind == "partial" || kind == "full_dirty" {
					preservationKinds++
				}
			}
			if len(kinds) > 1 && preservationKinds > 1 {
				issues = append(issues, fmt.Sprintf("multiple_relationships:%d", subject))
			}
			if containsH3ImagePE(kinds, "full_dirty") {
				issues = append(issues, fmt.Sprintf("fully_preserved_not_standalone:%d", subject))
			}
			if containsH3ImagePE(kinds, "removed") && len(kinds) == 1 {
				issues = append(issues, fmt.Sprintf("subject_defined_only_to_remove:%d", subject))
			}
		}
	}
	if h3ImagePEMergedFull.MatchString(rest) {
		issues = append(issues, "merged_fully_preserved")
	}
	unquoted := h3ImagePEUnquoted(text)
	if h3ImagePECJK.MatchString(unquoted) {
		issues = append(issues, "non_english_prose")
	}
	if strings.ContainsAny(unquoted, "\"“”") {
		issues = append(issues, "double_quoted_literal")
	}
	if h3ImagePEHasUnlabeledGrid(unquoted) && !h3ImagePEGridLabel.MatchString(unquoted) {
		issues = append(issues, "unlabeled_grid")
	}
	return issues
}

func checkH3ImagePET2I(parsed map[string]any, allowedRatios []string) []string {
	caption, ok := parsed["expanded_caption"].(string)
	if !ok || strings.TrimSpace(caption) == "" {
		return []string{"empty_caption"}
	}
	var issues []string
	ratio, _ := parsed["aspect_ratio"].(string)
	if !containsH3ImagePE(allowedRatios, ratio) {
		issues = append(issues, "ratio_enum")
	}
	if strings.Contains(strings.TrimSpace(caption), "\n") {
		issues = append(issues, "line_breaks")
	}
	if h3ImagePECJK.MatchString(h3ImagePEUnquotedT2I.ReplaceAllString(caption, "")) {
		issues = append(issues, "non_english_prose")
	}
	return issues
}

func h3ImagePEUnquoted(text string) string {
	ranges := h3ImagePESingleQuotedRanges(text)
	if len(ranges) == 0 {
		return text
	}
	var result strings.Builder
	start := 0
	for _, quoted := range ranges {
		result.WriteString(text[start:quoted[0]])
		result.WriteString("''")
		start = quoted[1]
	}
	result.WriteString(text[start:])
	return result.String()
}

func h3ImagePESplitSentences(text string) []string {
	masked := []byte(text)
	for _, quoted := range h3ImagePESingleQuotedRanges(text) {
		for i := quoted[0]; i < quoted[1]; i++ {
			if masked[i] == '.' || masked[i] == '!' || masked[i] == '?' {
				masked[i] = '_'
			}
		}
	}
	var sentences []string
	start := 0
	for _, match := range h3ImagePESentence.FindAllSubmatchIndex(masked, -1) {
		separatorStart, separatorEnd := match[4], match[5]
		sentences = append(sentences, strings.TrimSpace(text[start:separatorStart]))
		start = separatorEnd
	}
	sentences = append(sentences, strings.TrimSpace(text[start:]))
	out := sentences[:0]
	for _, sentence := range sentences {
		if sentence != "" {
			out = append(out, sentence)
		}
	}
	return out
}

func h3ImagePESingleQuotedRanges(text string) [][2]int {
	var ranges [][2]int
	for cursor := 0; cursor < len(text); {
		relative := strings.IndexByte(text[cursor:], '\'')
		if relative < 0 {
			break
		}
		open := cursor + relative
		if open > 0 && h3ImagePEASCIIAlphaNum(text[open-1]) {
			cursor = open + 1
			continue
		}
		close := -1
		for i := open + 1; i < len(text); i++ {
			if text[i] != '\'' {
				continue
			}
			if i+1 < len(text) && h3ImagePEASCIIAlpha(text[i+1]) {
				continue
			}
			close = i
			break
		}
		if close < 0 {
			cursor = open + 1
			continue
		}
		ranges = append(ranges, [2]int{open, close + 1})
		cursor = close + 1
	}
	return ranges
}

func h3ImagePEHasUnlabeledGrid(text string) bool {
	for _, match := range h3ImagePEGrid.FindAllStringIndex(text, -1) {
		if match[1] < len(text) && h3ImagePEGridSizeSuffix.MatchString(text[match[1]:]) {
			continue
		}
		return true
	}
	return false
}

func h3ImagePESubjectIDs(text string) []int {
	var ids []int
	for _, match := range h3ImagePESubject.FindAllStringSubmatch(text, -1) {
		id, _ := strconv.Atoi(match[1])
		ids = append(ids, id)
	}
	return ids
}

func uniqueH3ImagePEInts(matches [][]string) []int {
	seen := make(map[int]bool)
	var values []int
	for _, match := range matches {
		value, _ := strconv.Atoi(match[1])
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	sort.Ints(values)
	return values
}

func h3ImagePEASCIIAlpha(value byte) bool {
	return value >= 'A' && value <= 'Z' || value >= 'a' && value <= 'z'
}

func h3ImagePEASCIIAlphaNum(value byte) bool {
	return h3ImagePEASCIIAlpha(value) || value >= '0' && value <= '9'
}

func containsH3ImagePE(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func describeH3ImagePEIssues(codes []string) []string {
	descriptions := map[string]string{
		"def_without_picture":                "a Subject definition lacks an explicit <Picture N> source",
		"subject_defined_only_to_remove":     "a Subject is defined for an element that is removed; drop that Subject and renumber",
		"no_relationship":                    "a defined Subject has no relationship sentence",
		"non_english_prose":                  "non-English characters appear outside single-quoted literals",
		"double_quoted_literal":              "literals must use single quotes, not double quotes",
		"no_target_overview":                 "missing the sentence beginning 'The target image'",
		"no_subjects":                        "no <Subject N> definitions before 'The target image'",
		"fully_preserved_not_standalone":     "a fully-preserved clause has extra words; it must be exactly '<Subject N> is fully preserved.'",
		"part1_sentence_without_new_subject": "a sentence before 'The target image' does not define a new Subject",
		"undefined_subject":                  "a <Subject N> is used without being defined",
		"multiple_relationships":             "a Subject receives more than one relationship",
		"merged_fully_preserved":             "several Subjects share one fully-preserved clause",
		"empty_caption":                      "expanded_caption must be a non-empty string",
		"line_breaks":                        "expanded_caption contains line breaks",
		"keys":                               "the object must have exactly expanded_caption and target_aspect_ratio",
		"not_pure_json":                      "the answer is not a bare JSON object",
		"ratio_enum":                         "aspect_ratio must be one of the allowed ratios",
		"ratio_ignores_user_selection":       "target_aspect_ratio must equal the user aspect ratio selection",
		"bad_ratio":                          "target_aspect_ratio must be W:H with positive integers",
		"subject_numbering":                  "Subjects must be numbered 1, 2, 3, ... in definition order",
		"def_not_natural":                    "each definition must read '... in <Picture N> is <Subject N>.'",
		"unlabeled_grid":                     "describe grids as 'R rows and C columns'",
	}
	result := make([]string, 0, len(codes))
	seen := make(map[string]bool)
	for _, issue := range codes {
		code := issue
		if i := strings.IndexByte(code, ':'); i >= 0 {
			code = code[:i]
		}
		if i := strings.IndexByte(code, '='); i >= 0 {
			code = code[:i]
		}
		description, ok := descriptions[code]
		if !ok {
			description = issue
		}
		if i := strings.IndexByte(issue, ':'); i >= 0 {
			description += " (" + issue[i+1:] + ")"
		}
		if !seen[description] {
			seen[description] = true
			result = append(result, description)
		}
	}
	return result
}

func buildH3ImagePERepair(issues []string, edit bool) string {
	if edit {
		return "Your JSON violates the output contract: " + strings.Join(issues, "; ") +
			". Rewrite the complete answer so it satisfies every rule above, keeping the correct content. " +
			"Output only the required two-field JSON object."
	}
	rawIssues := strings.Join(issues, ", ")
	if rawIssues == "" {
		rawIssues = "invalid JSON"
	}
	return "Your JSON violates the output contract: " + rawIssues +
		". Prose must be English; non-English text may appear only inside double-quoted literals. " +
		"Rewrite the complete answer; output only the two-key JSON object."
}

func h3ImagePESizeForRatio(ratio string) (string, string) {
	match := strings.Split(strings.TrimSpace(ratio), ":")
	if len(match) != 2 {
		return "", fmt.Sprintf("无法解析输出比例 %q,未设置 H3-Image size", ratio)
	}
	width, errW := strconv.Atoi(match[0])
	height, errH := strconv.Atoi(match[1])
	if errW != nil || errH != nil || width <= 0 || height <= 0 {
		return "", fmt.Sprintf("无法解析输出比例 %q,未设置 H3-Image size", ratio)
	}
	r := float64(width) / float64(height)
	bestDistance := math.Inf(1)
	bestSize := ""
	for _, entry := range h3ImagePE2KSizes {
		parts := strings.Split(entry.ratio, ":")
		w, _ := strconv.ParseFloat(parts[0], 64)
		h, _ := strconv.ParseFloat(parts[1], 64)
		distance := math.Abs(math.Log(r) - math.Log(w/h))
		if distance < bestDistance {
			bestDistance, bestSize = distance, entry.size
		}
	}
	return bestSize, ""
}
