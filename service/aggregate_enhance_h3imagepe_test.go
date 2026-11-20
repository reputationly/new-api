package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

func h3TestPNGDataURI(t *testing.T, width, height int) string {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	img.Set(0, 0, color.White)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())
}

func hasH3ImagePEIssue(issues []string, code string) bool {
	for _, issue := range issues {
		if issue == code || strings.HasPrefix(issue, code+":") {
			return true
		}
	}
	return false
}

func TestBuildH3ImagePEEditMessages(t *testing.T) {
	first := h3TestPNGDataURI(t, 400, 300)
	second := h3TestPNGDataURI(t, 600, 800)
	messages := buildH3ImagePEMessages("把标牌改成“晨光咖啡”", []string{first, second}, "adaptive", "")
	if len(messages) != 1 || messages[0]["role"] != "user" {
		t.Fatalf("EDIT 应只有一个 user 消息: %#v", messages)
	}
	parts, ok := messages[0]["content"].([]map[string]any)
	if !ok || len(parts) != 7 {
		t.Fatalf("EDIT content parts = %#v", messages[0]["content"])
	}
	wantTexts := map[int]string{
		0: h3ImagePEEditTask,
		1: "Reference Image Count:\n2 input image(s), labeled <Picture 1> through <Picture 2>.",
		2: "Reference Media:\n- <Picture 1> [aspect_ratio 4:3]",
		4: "Reference Media:\n- <Picture 2> [aspect_ratio 3:4]",
		6: "\nUser Editing Instruction (JSON string):\n\"把标牌改成“晨光咖啡”\"" +
			"\n\nUser Aspect Ratio Selection:\nadaptive\n\n" + strings.TrimSpace(h3ImagePEEditAppendix) +
			"\n\nApply the instructions above to the supplied inputs and reference images. Output only the required two-field JSON object.",
	}
	for index, want := range wantTexts {
		if got, _ := parts[index]["text"].(string); got != want {
			t.Errorf("part[%d] text mismatch:\n got: %q\nwant: %q", index, got, want)
		}
	}
	for index, wantURL := range map[int]string{3: first, 5: second} {
		part, ok := parts[index]["image_url"].(map[string]any)
		if !ok || part["url"] != wantURL {
			t.Errorf("part[%d] image_url = %#v", index, parts[index])
		}
	}
	finalText := parts[6]["text"].(string)
	if strings.Index(finalText, strings.TrimSpace(h3ImagePEEditAppendix)) >
		strings.Index(finalText, "Apply the instructions above") {
		t.Fatal("EDIT appendix must precede the final Apply instruction")
	}

	unreadable := "data:image/png;base64,invalid"
	unreadableParts := buildH3ImagePEMessages("edit", []string{unreadable}, "adaptive", "")[0]["content"].([]map[string]any)
	if got := unreadableParts[2]["text"]; got != "Reference Media:\n- <Picture 1> [aspect_ratio unavailable]" {
		t.Fatalf("unreadable image ratio = %q", got)
	}
}

func TestBuildH3ImagePET2IMessages(t *testing.T) {
	prompt := "咖啡店开业海报，标题“晨光咖啡”，副标题“Grand Opening 12.01”，竖版"
	messages := buildH3ImagePEMessages(prompt, nil, "adaptive", "")
	if len(messages) != 2 || messages[0]["role"] != "system" || messages[1]["role"] != "user" {
		t.Fatalf("T2I messages = %#v", messages)
	}
	if got := messages[0]["content"]; got != strings.TrimRight(h3ImagePET2ISystem, "\n") {
		t.Fatalf("T2I system prompt mismatch")
	}
	wantUser := formatH3ImagePET2ITask(prompt) + "\n\n" + strings.TrimSpace(h3ImagePET2IAppendix)
	if got := messages[1]["content"]; got != wantUser {
		t.Fatalf("T2I user prompt mismatch")
	}
	if !strings.Contains(wantUser, "input_text: "+prompt+"\noutput_text:") {
		t.Fatal("Python format placeholder was not replaced")
	}

	braced := "a {literal} and {{escaped}}"
	formatted := formatH3ImagePET2ITask(braced)
	if strings.Count(formatted, braced) != 1 || !strings.Contains(formatted, "{literal} and {{escaped}}") {
		t.Fatalf("T2I braces were not handled like str.format: %q", formatted)
	}

	selected := buildH3ImagePEMessages(prompt, nil, "adaptive", "2368x1760")[1]["content"].(string)
	selection := "User Aspect Ratio Selection: 4:3. Compose the image for exactly this final canvas and output it as aspect_ratio."
	if !strings.HasSuffix(selected, selection) ||
		strings.Index(selected, strings.TrimSpace(h3ImagePET2IAppendix)) > strings.Index(selected, "User Aspect Ratio Selection:") {
		t.Fatalf("explicit ratio selection missing or misplaced: %q", selected[len(selected)-len(selection)-2:])
	}
}

func TestH3ImagePEOfficialCaptionsPass(t *testing.T) {
	files, err := filepath.Glob("testdata/h3imagepe/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 26 {
		t.Fatalf("official fixtures = %d files, want 26", len(files))
	}
	type sample struct {
		RevisedPrompt     string `json:"revised_prompt"`
		AspectRatio       string `json:"aspect_ratio"`
		TargetAspectRatio string `json:"target_aspect_ratio"`
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".json"), func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var got sample
			if err := common.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			name := strings.TrimSuffix(filepath.Base(file), ".json")
			if strings.HasPrefix(name, "t2i_") {
				issues := checkH3ImagePET2I(map[string]any{
					"expanded_caption": got.RevisedPrompt,
					"aspect_ratio":     got.AspectRatio,
				}, h3ImagePERatios)
				if len(issues) > 0 {
					t.Fatalf("T2I caption issues: %v", issues)
				}
				return
			}
			ratio := got.TargetAspectRatio
			if ratio == "" {
				ratio = got.AspectRatio
			}
			userRatio := "adaptive"
			if name == "fox_3x4" {
				userRatio = "3:4"
				if ratio == "" {
					ratio = userRatio
				}
			}
			imageCount := 1
			switch name {
			case "cat_dog", "tryon", "logo_tee", "palette_ref":
				imageCount = 2
			case "ex_backpack", "ex_bookstore", "ex_card", "ex_tea":
				for _, match := range h3ImagePEPicture.FindAllStringSubmatch(got.RevisedPrompt, -1) {
					picture, _ := strconv.Atoi(match[1])
					if picture > imageCount {
						imageCount = picture
					}
				}
			}
			issues := checkH3ImagePEEditCaption(got.RevisedPrompt, ratio, imageCount, userRatio)
			if len(issues) > 0 {
				t.Fatalf("EDIT caption issues: %v", issues)
			}
		})
	}
}

func TestH3ImagePECheckerNegatives(t *testing.T) {
	valid := "The red sign in <Picture 1> is <Subject 1>. The target image shows the sign. <Subject 1> is fully preserved."
	tests := []struct {
		name  string
		text  string
		ratio string
		want  string
	}{
		{"fully preserved extra words", strings.Replace(valid, "is fully preserved.", "is fully preserved and remains bright.", 1), "adaptive", "fully_preserved_not_standalone"},
		{"missing picture", strings.Replace(valid, " in <Picture 1>", "", 1), "adaptive", "def_without_picture"},
		{"picture out of range", strings.Replace(valid, "<Picture 1>", "<Picture 2>", 1), "adaptive", "picture_out_of_range"},
		{"Chinese prose", strings.Replace(valid, "The target image shows the sign.", "The target image shows the sign 中文。", 1), "adaptive", "non_english_prose"},
		{"double quoted literal", strings.Replace(valid, "red sign", `red sign called "SALE"`, 1), "adaptive", "double_quoted_literal"},
		{"subject only removed", strings.Replace(valid, "is fully preserved.", "is removed.", 1), "adaptive", "subject_defined_only_to_remove"},
		{"explicit ratio mismatch", valid, "3:4", "ratio_ignores_user_selection"},
		{"single quoted sentence punctuation", "The sign reading 'Hello. Welcome' in <Picture 1> is <Subject 1>. The target image shows the sign. <Subject 1> is fully preserved.", "adaptive", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			issues := checkH3ImagePEEditCaption(test.text, "16:9", 1, test.ratio)
			if test.want == "" {
				if len(issues) != 0 {
					t.Fatalf("unexpected issues: %v", issues)
				}
				return
			}
			if !hasH3ImagePEIssue(issues, test.want) {
				t.Fatalf("issues %v do not contain %q", issues, test.want)
			}
		})
	}
}

func TestH3ImagePET2IRejectsEmptyCaptionAndRepairs(t *testing.T) {
	for _, raw := range []string{
		`{"expanded_caption":null,"aspect_ratio":"1:1"}`,
		`{"expanded_caption":"","aspect_ratio":"1:1"}`,
	} {
		_, issues := validateH3ImagePE(raw, false, 0, "adaptive", h3ImagePERatios)
		if !hasH3ImagePEIssue(issues, "empty_caption") {
			t.Fatalf("empty caption %s issues: %v", raw, issues)
		}
	}

	got := fakeSequence(t,
		chatResponse(`{"expanded_caption":"","aspect_ratio":"1:1"}`),
		chatResponse(`{"expanded_caption":"The image is a red poster.","aspect_ratio":"1:1"}`),
	)
	result := EnhancePrompt(context.Background(), h3ImagePEAggregateForTest(), "Bearer test", EnhanceInput{Prompt: "poster"})
	if result.Degraded || !result.Repaired || result.EnhancedPrompt != "The image is a red poster." {
		t.Fatalf("repair result: %+v", result)
	}
	if result.Usage == nil || result.Usage.PromptTokens != 20 ||
		result.Usage.CompletionTokens != 40 || result.Usage.TotalTokens != 60 {
		t.Fatalf("repair usage should include both calls: %+v", result.Usage)
	}
	if len(*got) != 2 {
		t.Fatalf("calls = %d, want repair call", len(*got))
	}
	secondBody := h3ImagePERequestBody(t, (*got)[1])
	secondMessages := secondBody["messages"].([]any)
	wantFeedback := "Your JSON violates the output contract: empty_caption. Prose must be English; " +
		"non-English text may appear only inside double-quoted literals. Rewrite the complete answer; " +
		"output only the two-key JSON object."
	if last := secondMessages[len(secondMessages)-1].(map[string]any)["content"]; last != wantFeedback {
		t.Fatalf("T2I repair feedback = %q", last)
	}
}

func TestH3ImagePEEnhancementRetriesAndDegrades(t *testing.T) {
	t.Run("first try success and size", func(t *testing.T) {
		got := fakeSequence(t, chatResponse(`{"expanded_caption":"The image is a red poster.","aspect_ratio":"16:9"}`))
		result := EnhancePrompt(context.Background(), h3ImagePEAggregateForTest(), "Bearer test", EnhanceInput{Prompt: "poster"})
		if result.Degraded || result.Repaired || result.Mode != common.EnhanceModeH3ImagePE ||
			result.Size != "2720x1536" || result.Usage == nil || result.Usage.TotalTokens != 30 {
			t.Fatalf("result: %+v", result)
		}
		body := h3ImagePERequestBody(t, (*got)[0])
		if _, ok := body["response_format"]; ok {
			t.Fatal("request must not contain response_format")
		}
		if body["temperature"] != float64(1) || body["top_p"] != 0.95 {
			t.Fatalf("T2I sampling params: %#v", body)
		}
	})

	t.Run("empty content reuses messages", func(t *testing.T) {
		got := fakeSequence(t,
			chatResponse(""),
			chatResponse(`{"expanded_caption":"The image is a blue poster.","aspect_ratio":"1:1"}`),
		)
		result := EnhancePrompt(context.Background(), h3ImagePEAggregateForTest(), "Bearer test", EnhanceInput{Prompt: "poster"})
		if result.Degraded || !result.Repaired || len(*got) != 2 {
			t.Fatalf("result=%+v, calls=%d", result, len(*got))
		}
		first := h3ImagePERequestBody(t, (*got)[0])["messages"]
		second := h3ImagePERequestBody(t, (*got)[1])["messages"]
		firstJSON, _ := common.Marshal(first)
		secondJSON, _ := common.Marshal(second)
		if !bytes.Equal(firstJSON, secondJSON) {
			t.Fatal("empty-content retry should reuse the original messages")
		}
	})

	t.Run("exhausted repair degrades", func(t *testing.T) {
		got := fakeSequence(t,
			chatResponse(`{"expanded_caption":"The image is a red poster.","aspect_ratio":"2:1"}`),
			chatResponse(`{"expanded_caption":"The image is a blue poster.","aspect_ratio":"2:1"}`),
			chatResponse(`{"expanded_caption":"The image is a green poster.","aspect_ratio":"2:1"}`),
		)
		result := EnhancePrompt(context.Background(), h3ImagePEAggregateForTest(), "Bearer test", EnhanceInput{Prompt: "original"})
		if !result.Degraded || result.EnhancedPrompt != "original" || !strings.Contains(result.DegradeReason, "h3_image_pe") {
			t.Fatalf("degraded result: %+v", result)
		}
		if len(*got) != h3ImagePEMaxRepairRounds+1 {
			t.Fatalf("calls = %d, want %d", len(*got), h3ImagePEMaxRepairRounds+1)
		}
		thirdMessages := h3ImagePERequestBody(t, (*got)[2])["messages"].([]any)
		if len(thirdMessages) != 4 {
			t.Fatalf("second repair should include only the latest repair context: %#v", thirdMessages)
		}
	})
}

func TestH3ImagePEEditRepairUsesReadableIssues(t *testing.T) {
	validCaption := "The red sign in <Picture 1> is <Subject 1>. The target image shows the sign. <Subject 1> is fully preserved."
	got := fakeSequence(t,
		chatResponse(`{"expanded_caption":"`+validCaption+`","target_aspect_ratio":"0:0"}`),
		chatResponse(`{"expanded_caption":"`+validCaption+`","target_aspect_ratio":"4:3"}`),
	)
	result := EnhancePrompt(context.Background(), h3ImagePEAggregateForTest(), "Bearer test", EnhanceInput{
		Prompt: "edit", ImageURLs: []string{h3TestPNGDataURI(t, 400, 300)},
	})
	if result.Degraded || !result.Repaired {
		t.Fatalf("EDIT repair result: %+v", result)
	}
	secondBody := h3ImagePERequestBody(t, (*got)[1])
	messages := secondBody["messages"].([]any)
	wantFeedback := "Your JSON violates the output contract: target_aspect_ratio must be W:H with positive integers. " +
		"Rewrite the complete answer so it satisfies every rule above, keeping the correct content. " +
		"Output only the required two-field JSON object."
	if last := messages[len(messages)-1].(map[string]any)["content"]; last != wantFeedback {
		t.Fatalf("EDIT repair feedback = %q", last)
	}
}

func TestH3ImagePEEditRequestOmitsSamplingParameters(t *testing.T) {
	got := fakeSequence(t, chatResponse(`{"expanded_caption":"The red sign in <Picture 1> is <Subject 1>. The target image shows the sign. <Subject 1> is fully preserved.","target_aspect_ratio":"4:3"}`))
	result := EnhancePrompt(context.Background(), h3ImagePEAggregateForTest(), "Bearer test", EnhanceInput{
		Prompt: "edit", ImageURLs: []string{h3TestPNGDataURI(t, 400, 300)},
	})
	if result.Degraded || result.Size != "2368x1760" {
		t.Fatalf("EDIT result: %+v", result)
	}
	body := h3ImagePERequestBody(t, (*got)[0])
	for _, key := range []string{"temperature", "top_p", "response_format"} {
		if _, ok := body[key]; ok {
			t.Errorf("EDIT request must omit %s", key)
		}
	}
}

func TestH3ImagePEReasoningEffortAndClientSize(t *testing.T) {
	t.Run("reasoning effort is passed verbatim", func(t *testing.T) {
		got := fakeSequence(t, chatResponse(`{"expanded_caption":"The image is a poster.","aspect_ratio":"4:3"}`))
		cfg := h3ImagePEAggregateForTest()
		cfg.PromptEnhance.ReasoningEffort = " low "
		cfg.PromptEnhance.Thinking = boolPtr(false)
		EnhancePrompt(context.Background(), cfg, "Bearer test", EnhanceInput{Prompt: "poster"})
		body := h3ImagePERequestBody(t, (*got)[0])
		if body["reasoning_effort"] != " low " {
			t.Fatalf("reasoning_effort = %#v", body["reasoning_effort"])
		}
		if _, ok := body["chat_template_kwargs"]; ok {
			t.Fatal("reasoning_effort must bypass applyThinking")
		}
	})

	t.Run("empty reasoning effort uses thinking setting", func(t *testing.T) {
		got := fakeSequence(t, chatResponse(`{"expanded_caption":"The image is a poster.","aspect_ratio":"4:3"}`))
		cfg := h3ImagePEAggregateForTest()
		cfg.PromptEnhance.Thinking = boolPtr(false)
		EnhancePrompt(context.Background(), cfg, "Bearer test", EnhanceInput{Prompt: "poster"})
		body := h3ImagePERequestBody(t, (*got)[0])
		kwargs, ok := body["chat_template_kwargs"].(map[string]any)
		if !ok || kwargs["enable_thinking"] != false {
			t.Fatalf("empty reasoning_effort should call applyThinking: %#v", body)
		}
	})

	t.Run("explicit ratio is enforced and size is preserved", func(t *testing.T) {
		got := fakeSequence(t,
			chatResponse(`{"expanded_caption":"The image is a poster.","aspect_ratio":"16:9"}`),
			chatResponse(`{"expanded_caption":"The image is a poster.","aspect_ratio":"4:3"}`),
		)
		result := EnhancePrompt(context.Background(), h3ImagePEAggregateForTest(), "Bearer test", EnhanceInput{
			Prompt: "poster", ClientSize: "2368x1760",
		})
		if result.Degraded || !result.Repaired || result.Size != "" {
			t.Fatalf("client size result: %+v", result)
		}
		body := h3ImagePERequestBody(t, (*got)[0])
		messages := body["messages"].([]any)
		user := messages[1].(map[string]any)["content"].(string)
		if !strings.Contains(user, "User Aspect Ratio Selection: 4:3. Compose the image for exactly this final canvas and output it as aspect_ratio.") {
			t.Fatalf("user ratio instruction missing: %s", user[len(user)-120:])
		}
	})
}

func TestH3ImagePEDisabledImageForwardingUsesT2I(t *testing.T) {
	got := fakeSequence(t, chatResponse(`{"expanded_caption":"The image is a poster.","aspect_ratio":"1:1"}`))
	cfg := h3ImagePEAggregateForTest()
	cfg.PromptEnhance.SendInputImages = boolPtr(false)
	result := EnhancePrompt(context.Background(), cfg, "Bearer test", EnhanceInput{
		Prompt: "poster", ImageURLs: []string{h3TestPNGDataURI(t, 400, 300)},
	})
	if result.Degraded {
		t.Fatalf("disabled-image T2I result: %+v", result)
	}
	messages := h3ImagePERequestBody(t, (*got)[0])["messages"].([]any)
	if len(messages) != 2 || messages[0].(map[string]any)["role"] != "system" ||
		messages[1].(map[string]any)["role"] != "user" {
		t.Fatalf("disabled input image should use T2I messages: %#v", messages)
	}
	if _, ok := messages[1].(map[string]any)["content"].(string); !ok {
		t.Fatalf("T2I user content must be text: %#v", messages[1])
	}
}

func TestH3ImagePETimeoutDefaultAndOverride(t *testing.T) {
	if got := h3ImagePEBudget(nil); got != 120*time.Second {
		t.Fatalf("default timeout = %s, want 2m", got)
	}
	cfg := &common.AggregatePromptEnhance{TimeoutSeconds: 75}
	if got := h3ImagePEBudget(cfg); got != 75*time.Second {
		t.Fatalf("configured timeout = %s, want 75s", got)
	}
}

func TestH3ImagePESizeMapping(t *testing.T) {
	for ratio, want := range map[string]string{
		"1600:1189": "2368x1760",
		"16:9":      "2720x1536",
	} {
		got, warning := h3ImagePESizeForRatio(ratio)
		if got != want || warning != "" {
			t.Errorf("h3ImagePESizeForRatio(%q) = %q, %q; want %q", ratio, got, warning, want)
		}
	}
	if size, warning := h3ImagePESizeForRatio("invalid"); size != "" || warning == "" {
		t.Fatalf("unparseable ratio result = %q, %q", size, warning)
	}
}

func h3ImagePEAggregateForTest() *common.AggregateModel {
	return &common.AggregateModel{
		PromptEnhance: &common.AggregatePromptEnhance{
			Model: "enhancer", Mode: common.EnhanceModeH3ImagePE,
		},
	}
}

func h3ImagePERequestBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	return body
}
