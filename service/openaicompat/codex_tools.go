package openaicompat

import (
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// This file ports CodexPlusPlus's Codex tool-adaptation layer: freeform/custom
// tools (apply_patch, local_shell, …) are exposed to Chat-only models as plain
// JSON function tools, and the model's tool calls are mapped back to the
// original Responses tool shapes. See docs/responses-via-chat-deployment.md.

// ---------- small JSON helpers (map[string]any is canonical via common.Marshal) ----------

func mapGetString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

func mapGetSlice(m map[string]any, key string) []any {
	if m == nil {
		return nil
	}
	switch v := m[key].(type) {
	case []any:
		return v
	case []map[string]any:
		// parseApplyPatchOperations builds concrete slices; after a JSON
		// round-trip they become []any. Accept both.
		out := make([]any, len(v))
		for i := range v {
			out[i] = v[i]
		}
		return out
	}
	return nil
}

func marshalString(v any) string {
	b, err := common.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}

func boolRaw(b bool) json.RawMessage {
	if b {
		return json.RawMessage("true")
	}
	return json.RawMessage("false")
}

// parseToolsRaw parses the Responses `tools` field into a slice of raw elements
// (each is a map, or a bare string for shorthand custom tools).
func parseToolsRaw(raw json.RawMessage) []any {
	if len(raw) == 0 {
		return nil
	}
	var tools []any
	if err := common.Unmarshal(raw, &tools); err != nil {
		return nil
	}
	return tools
}

// ---------- context construction ----------

// BuildCodexToolContext builds the tool taxonomy from the Responses request
// `tools` so the response side can map upstream tool-call names back.
func BuildCodexToolContext(rawTools json.RawMessage) *dto.ResponsesToolContext {
	ctx := dto.NewResponsesToolContext()
	for _, el := range parseToolsRaw(rawTools) {
		switch tool := el.(type) {
		case string:
			if tool == "" {
				continue
			}
			if action := proxyActionFromUpstreamName(tool); action != "" {
				ctx.CustomTools[tool] = dto.CodexCustomToolSpec{OpenAIName: "apply_patch", Kind: dto.CodexToolKindApplyPatch, ProxyAction: action}
			} else {
				ctx.CustomTools[tool] = dto.CodexCustomToolSpec{OpenAIName: tool, Kind: dto.CodexToolKindRaw}
			}
			ctx.HasCustomTools = true
		case map[string]any:
			name := mapGetString(tool, "name")
			switch mapGetString(tool, "type") {
			case "custom":
				if name == "" {
					continue
				}
				kind := detectCodexCustomToolKind(tool, name)
				ctx.CustomTools[name] = dto.CodexCustomToolSpec{OpenAIName: name, Kind: kind}
				if kind == dto.CodexToolKindApplyPatch {
					for _, action := range applyPatchActions() {
						ctx.CustomTools[name+"_"+action] = dto.CodexCustomToolSpec{OpenAIName: name, Kind: dto.CodexToolKindApplyPatch, ProxyAction: action}
					}
				}
				ctx.HasCustomTools = true
			case "function":
				if name == "" {
					continue
				}
				ctx.FunctionTools[name] = dto.CodexFunctionToolSpec{Name: name}
			case "namespace":
				addNamespaceToolsToContext(ctx, tool)
			case "web_search", "local_shell", "computer_use":
				if name == "" {
					name = mapGetString(tool, "type")
				}
				ctx.CustomTools[name] = dto.CodexCustomToolSpec{OpenAIName: name, Kind: dto.CodexToolKindBuiltIn}
				ctx.HasCustomTools = true
			}
		}
	}
	return ctx
}

func addNamespaceToolsToContext(ctx *dto.ResponsesToolContext, nsTool map[string]any) {
	namespace := mapGetString(nsTool, "name")
	for _, childAny := range mapGetSlice(nsTool, "tools") {
		child, ok := childAny.(map[string]any)
		if !ok || mapGetString(child, "type") != "function" {
			continue
		}
		childName := mapGetString(child, "name")
		if childName == "" {
			continue
		}
		flat := flattenNamespaceToolName(namespace, childName)
		if namespace == "" {
			ctx.FunctionTools[flat] = dto.CodexFunctionToolSpec{Namespace: "", Name: childName}
			continue
		}
		if existing, ok := ctx.FunctionTools[flat]; !ok || existing.Namespace != "" {
			ctx.FunctionTools[flat] = dto.CodexFunctionToolSpec{Namespace: namespace, Name: childName}
			ctx.HasNamespaceTools = true
		}
	}
}

// ---------- request: Responses tools -> Chat tools ----------

func responsesToolsToChatToolsWithContext(rawTools json.RawMessage, ctx *dto.ResponsesToolContext) []map[string]any {
	var out []map[string]any
	for _, el := range parseToolsRaw(rawTools) {
		switch tool := el.(type) {
		case string:
			if tool == "" {
				continue
			}
			out = append(out, genericCustomProxyTool(tool, ""))
		case map[string]any:
			switch mapGetString(tool, "type") {
			case "function":
				if t := responsesFunctionToolToChatTool(tool); t != nil {
					out = append(out, t)
				}
			case "custom", "web_search", "local_shell", "computer_use":
				name := mapGetString(tool, "name")
				if name == "" {
					name = mapGetString(tool, "type")
				}
				desc := mapGetString(tool, "description")
				if detectCodexCustomToolKind(tool, name) == dto.CodexToolKindApplyPatch {
					out = append(out, applyPatchProxyTools(name, desc)...)
				} else {
					out = append(out, genericCustomProxyTool(name, desc))
				}
			case "namespace":
				out = append(out, namespaceToolToChatTools(tool, ctx)...)
			}
		}
	}
	return out
}

func detectCodexCustomToolKind(tool map[string]any, name string) string {
	if name == "apply_patch" {
		return dto.CodexToolKindApplyPatch
	}
	if format, ok := tool["format"].(map[string]any); ok {
		if def, ok := format["definition"].(string); ok {
			if strings.Contains(def, "begin_patch") && strings.Contains(def, "end_patch") && strings.Contains(def, "add_hunk") {
				return dto.CodexToolKindApplyPatch
			}
		}
	}
	switch mapGetString(tool, "type") {
	case "web_search", "local_shell", "computer_use":
		return dto.CodexToolKindBuiltIn
	}
	return dto.CodexToolKindRaw
}

func functionTool(name, description string, parameters any) map[string]any {
	fn := map[string]any{"name": name, "parameters": parameters}
	if description != "" {
		fn["description"] = description
	}
	return map[string]any{"type": "function", "function": fn}
}

func genericCustomProxyTool(name, description string) map[string]any {
	desc := ""
	if strings.TrimSpace(description) == "" {
		desc = "FREEFORM custom tool: " + name + ". Put only the tool input text here."
	} else {
		desc = strings.TrimSpace(description) + "\n\nThis is a FREEFORM tool. Do not wrap the input in JSON or markdown."
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        name,
			"description": desc,
			"parameters": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"input": map[string]any{"type": "string", "description": "Raw freeform input for this custom tool."},
				},
				"required": []any{"input"},
			},
		},
	}
}

func responsesFunctionToolToChatTool(tool map[string]any) map[string]any {
	if mapGetString(tool, "type") != "function" {
		return nil
	}
	// Already chat-shaped: {type:function, function:{...}}
	if fnAny, ok := tool["function"]; ok {
		fn, _ := fnAny.(map[string]any)
		clone := map[string]any{"type": "function"}
		newFn := map[string]any{}
		for k, v := range fn {
			newFn[k] = v
		}
		if strict, ok := tool["strict"]; ok {
			if _, exists := newFn["strict"]; !exists {
				newFn["strict"] = strict
			}
		}
		newFn["parameters"] = normalizeChatToolParameters(newFn["parameters"])
		clone["function"] = newFn
		for k, v := range tool {
			if k == "type" || k == "function" || k == "strict" {
				continue
			}
			clone[k] = v
		}
		return clone
	}
	// Responses-shaped (flat): {type:function, name, description, parameters}
	fn := map[string]any{
		"name":       mapGetString(tool, "name"),
		"parameters": normalizeChatToolParameters(tool["parameters"]),
	}
	if desc, ok := tool["description"]; ok {
		fn["description"] = desc
	}
	if strict, ok := tool["strict"]; ok {
		fn["strict"] = strict
	}
	return map[string]any{"type": "function", "function": fn}
}

func normalizeChatToolParameters(parameters any) map[string]any {
	params, ok := parameters.(map[string]any)
	if !ok || params == nil {
		params = map[string]any{}
	}
	out := map[string]any{}
	for k, v := range params {
		out[k] = v
	}
	if _, ok := out["type"]; !ok {
		out["type"] = "object"
	}
	if _, ok := out["properties"]; !ok {
		out["properties"] = map[string]any{}
	}
	if _, ok := out["required"]; !ok {
		out["required"] = []any{}
	}
	return out
}

func namespaceToolToChatTools(nsTool map[string]any, ctx *dto.ResponsesToolContext) []map[string]any {
	namespace := mapGetString(nsTool, "name")
	nsDesc := mapGetString(nsTool, "description")
	var out []map[string]any
	for _, childAny := range mapGetSlice(nsTool, "tools") {
		child, ok := childAny.(map[string]any)
		if !ok || mapGetString(child, "type") != "function" {
			continue
		}
		childName := mapGetString(child, "name")
		if childName == "" {
			continue
		}
		flat := flattenNamespaceToolName(namespace, childName)
		// Conflict skip: a real top-level function tool already claimed this flat name.
		if namespace != "" {
			if existing, ok := ctx.FunctionTools[flat]; ok && existing.Namespace == "" {
				continue
			}
		}
		fn := map[string]any{
			"name":       flat,
			"parameters": normalizeChatToolParameters(child["parameters"]),
		}
		if desc := combineNamespaceDescription(nsDesc, mapGetString(child, "description")); desc != "" {
			fn["description"] = desc
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

func flattenNamespaceToolName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	if name == "" {
		return namespace
	}
	if strings.HasSuffix(namespace, "__") || strings.HasPrefix(name, "__") {
		return namespace + name
	}
	return namespace + "__" + name
}

func combineNamespaceDescription(ns, child string) string {
	ns = strings.TrimSpace(ns)
	child = strings.TrimSpace(child)
	switch {
	case ns == "" && child == "":
		return ""
	case ns == "":
		return child
	case child == "":
		return ns
	default:
		return ns + "\n\n" + child
	}
}

// ---------- apply_patch proxy tools ----------

func applyPatchActions() []string {
	return []string{dto.CodexPatchActionAddFile, dto.CodexPatchActionDeleteFile, dto.CodexPatchActionUpdateFile, dto.CodexPatchActionReplaceFile, dto.CodexPatchActionBatch}
}

func patchProxyDescription(description, action, def string) string {
	if strings.TrimSpace(description) == "" {
		return def
	}
	return strings.TrimSpace(description) + " (proxy action: " + action + ")"
}

func applyPatchProxyTools(name, description string) []map[string]any {
	return []map[string]any{
		functionTool(name+"_add_file", patchProxyDescription(description, "add_file", "Create one new file by providing a target path and full file content."), applyPatchAddFileSchema("Full file content without patch '+' prefixes.")),
		functionTool(name+"_delete_file", patchProxyDescription(description, "delete_file", "Delete one file by providing a target path."), applyPatchDeleteFileSchema()),
		functionTool(name+"_update_file", patchProxyDescription(description, "update_file", "Edit one existing file with structured hunks."), applyPatchUpdateFileSchema()),
		functionTool(name+"_replace_file", patchProxyDescription(description, "replace_file", "Replace one existing file by providing a target path and full new file content."), applyPatchAddFileSchema("Full replacement content.")),
		functionTool(name+"_batch", patchProxyDescription(description, "batch", "Edit files by providing structured JSON patch operations."), applyPatchBatchSchema()),
	}
}

func applyPatchAddFileSchema(contentDesc string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Target file path."},
			"content": map[string]any{"type": "string", "description": contentDesc},
		},
		"required": []any{"path", "content"},
	}
}

func applyPatchDeleteFileSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           map[string]any{"path": map[string]any{"type": "string", "description": "Target file path."}},
		"required":             []any{"path"},
	}
}

func applyPatchUpdateFileSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "Target file path."},
			"move_to": map[string]any{"type": "string", "description": "Optional destination path for move operations."},
			"hunks":   applyPatchHunksSchema(),
		},
		"required": []any{"path", "hunks"},
	}
}

func applyPatchBatchSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"operations": map[string]any{
				"type":        "array",
				"description": "Ordered list of file patch operations.",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"type":    map[string]any{"type": "string", "enum": []any{"add_file", "delete_file", "update_file", "replace_file"}},
						"path":    map[string]any{"type": "string"},
						"move_to": map[string]any{"type": "string", "description": "Optional destination path for move operations (update_file only)."},
						"content": map[string]any{"type": "string", "description": "Full file content for add_file / replace_file."},
						"hunks":   applyPatchHunksSchema(),
					},
					"required": []any{"type", "path"},
				},
			},
		},
		"required": []any{"operations"},
	}
}

func applyPatchHunksSchema() map[string]any {
	return map[string]any{
		"type":        "array",
		"description": "Structured update hunks (required when type=update_file).",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"context": map[string]any{"type": "string", "description": "Optional @@ context header text."},
				"lines": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type":                 "object",
						"additionalProperties": false,
						"properties": map[string]any{
							"op":   map[string]any{"type": "string", "enum": []any{"context", "add", "remove"}},
							"text": map[string]any{"type": "string"},
						},
						"required": []any{"op", "text"},
					},
				},
			},
			"required": []any{"lines"},
		},
	}
}

// ---------- tool_choice ----------

func responsesToolChoiceToChatWithContext(raw json.RawMessage, ctx *dto.ResponsesToolContext) any {
	if len(raw) == 0 {
		return nil
	}
	if common.GetJsonType(raw) == "string" {
		var s string
		_ = common.Unmarshal(raw, &s)
		return s
	}
	var m map[string]any
	if err := common.Unmarshal(raw, &m); err != nil || m == nil {
		return nil
	}
	switch mapGetString(m, "type") {
	case "function":
		if ns, ok := m["namespace"].(string); ok && ns != "" {
			return map[string]any{"type": "function", "function": map[string]any{"name": flattenNamespaceToolName(ns, mapGetString(m, "name"))}}
		}
		if fn, ok := m["function"].(map[string]any); ok {
			if ns, ok := fn["namespace"].(string); ok && ns != "" {
				return map[string]any{"type": "function", "function": map[string]any{"name": flattenNamespaceToolName(ns, mapGetString(fn, "name"))}}
			}
		}
		// Accept both the flat Responses shape ({type,name}) and the chat shape
		// ({type,function:{name}}) — tool definitions accept both, so must tool_choice.
		name := mapGetString(m, "name")
		if name == "" {
			if fn, ok := m["function"].(map[string]any); ok {
				name = mapGetString(fn, "name")
			}
		}
		return map[string]any{"type": "function", "function": map[string]any{"name": name}}
	case "custom":
		name := mapGetString(m, "name")
		if name == "" {
			return nil
		}
		spec, ok := ctx.LookupCustomTool(name)
		if !ok {
			return nil
		}
		upstream := spec.OpenAIName
		if spec.Kind == dto.CodexToolKindApplyPatch {
			upstream = spec.OpenAIName + "_batch"
		}
		return map[string]any{"type": "function", "function": map[string]any{"name": upstream}}
	default:
		return m
	}
}

// ---------- apply_patch text <-> structured JSON ----------

func proxyActionFromUpstreamName(name string) string {
	switch {
	case strings.HasSuffix(name, "_add_file"):
		return dto.CodexPatchActionAddFile
	case strings.HasSuffix(name, "_delete_file"):
		return dto.CodexPatchActionDeleteFile
	case strings.HasSuffix(name, "_update_file"):
		return dto.CodexPatchActionUpdateFile
	case strings.HasSuffix(name, "_replace_file"):
		return dto.CodexPatchActionReplaceFile
	case strings.HasSuffix(name, "_batch"):
		return dto.CodexPatchActionBatch
	default:
		return ""
	}
}

func singleApplyPatchAction(opType string) string {
	switch opType {
	case "add_file":
		return dto.CodexPatchActionAddFile
	case "delete_file":
		return dto.CodexPatchActionDeleteFile
	case "update_file":
		return dto.CodexPatchActionUpdateFile
	case "replace_file":
		return dto.CodexPatchActionReplaceFile
	default:
		return ""
	}
}

// parseApplyPatchOperations parses apply_patch text into structured ops.
func parseApplyPatchOperations(input string) []map[string]any {
	var ops []map[string]any
	var cur map[string]any
	var curType string
	var contentLines []string
	var hunks []map[string]any
	var curHunk map[string]any
	var curLines []map[string]any

	flushHunk := func() {
		if curHunk != nil {
			curHunk["lines"] = curLines
			hunks = append(hunks, curHunk)
			curHunk = nil
			curLines = nil
		}
	}
	flushOp := func() {
		if cur == nil {
			return
		}
		switch curType {
		case "add_file":
			cur["content"] = strings.Join(contentLines, "\n")
		case "update_file":
			flushHunk()
			if hunks == nil {
				hunks = []map[string]any{}
			}
			cur["hunks"] = hunks
		}
		ops = append(ops, cur)
		cur = nil
		curType = ""
		contentLines = nil
		hunks = nil
	}

	for _, line := range strings.Split(input, "\n") {
		switch {
		case strings.HasPrefix(line, "*** Begin Patch"), strings.HasPrefix(line, "*** End Patch"):
			continue
		case strings.HasPrefix(line, "*** Add File: "):
			flushOp()
			cur = map[string]any{"type": "add_file", "path": strings.TrimPrefix(line, "*** Add File: ")}
			curType = "add_file"
		case strings.HasPrefix(line, "*** Delete File: "):
			flushOp()
			cur = map[string]any{"type": "delete_file", "path": strings.TrimPrefix(line, "*** Delete File: ")}
			curType = "delete_file"
		case strings.HasPrefix(line, "*** Update File: "):
			flushOp()
			cur = map[string]any{"type": "update_file", "path": strings.TrimPrefix(line, "*** Update File: ")}
			curType = "update_file"
		case strings.HasPrefix(line, "*** Move to: "):
			if cur != nil {
				cur["move_to"] = strings.TrimPrefix(line, "*** Move to: ")
			}
		case strings.HasPrefix(line, "@@"):
			if curType == "update_file" {
				flushHunk()
				curHunk = map[string]any{"context": strings.TrimSpace(strings.TrimPrefix(line, "@@"))}
				curLines = nil
			}
		default:
			switch curType {
			case "add_file":
				if strings.HasPrefix(line, "+") {
					contentLines = append(contentLines, line[1:])
				}
			case "update_file":
				if curHunk == nil {
					curHunk = map[string]any{"context": ""}
				}
				op, text := "context", line
				if len(line) > 0 {
					switch line[0] {
					case '+':
						op, text = "add", line[1:]
					case '-':
						op, text = "remove", line[1:]
					case ' ':
						op, text = "context", line[1:]
					}
				}
				curLines = append(curLines, map[string]any{"op": op, "text": text})
			}
		}
	}
	flushOp()
	return ops
}

// buildApplyPatchText renders structured ops back into apply_patch text.
func buildApplyPatchText(ops []map[string]any) string {
	var b strings.Builder
	b.WriteString("*** Begin Patch")
	writeContent := func(content string) {
		for _, ln := range strings.Split(content, "\n") {
			b.WriteString("\n+" + ln)
		}
	}
	for _, op := range ops {
		path := mapGetString(op, "path")
		switch mapGetString(op, "type") {
		case "add_file":
			b.WriteString("\n*** Add File: " + path)
			writeContent(mapGetString(op, "content"))
		case "delete_file":
			b.WriteString("\n*** Delete File: " + path)
		case "update_file":
			b.WriteString("\n*** Update File: " + path)
			if mv := mapGetString(op, "move_to"); mv != "" {
				b.WriteString("\n*** Move to: " + mv)
			}
			for _, h := range mapGetSlice(op, "hunks") {
				hunk, _ := h.(map[string]any)
				if ctx := mapGetString(hunk, "context"); ctx == "" {
					b.WriteString("\n@@")
				} else {
					b.WriteString("\n@@ " + ctx)
				}
				for _, l := range mapGetSlice(hunk, "lines") {
					lm, _ := l.(map[string]any)
					prefix := " "
					switch mapGetString(lm, "op") {
					case "add":
						prefix = "+"
					case "remove", "delete":
						prefix = "-"
					}
					b.WriteString("\n" + prefix + mapGetString(lm, "text"))
				}
			}
		case "replace_file":
			b.WriteString("\n*** Delete File: " + path)
			b.WriteString("\n*** Add File: " + path)
			writeContent(mapGetString(op, "content"))
		}
	}
	b.WriteString("\n*** End Patch")
	return b.String()
}

func hunksOrEmpty(op map[string]any) []any {
	if h := mapGetSlice(op, "hunks"); h != nil {
		return h
	}
	return []any{}
}

// buildApplyPatchOperationArguments produces the sub-tool JSON arguments for one op.
func buildApplyPatchOperationArguments(op map[string]any, action string) string {
	switch action {
	case dto.CodexPatchActionAddFile, dto.CodexPatchActionReplaceFile:
		return marshalString(map[string]any{"content": mapGetString(op, "content"), "path": mapGetString(op, "path")})
	case dto.CodexPatchActionDeleteFile:
		return marshalString(map[string]any{"path": mapGetString(op, "path")})
	case dto.CodexPatchActionUpdateFile:
		m := map[string]any{"hunks": hunksOrEmpty(op), "path": mapGetString(op, "path")}
		if mv := mapGetString(op, "move_to"); mv != "" {
			m["move_to"] = mv
		}
		return marshalString(m)
	default:
		return marshalString(map[string]any{"operations": []any{op}})
	}
}

// buildCustomToolCallHistory rewrites a Responses custom_tool_call (from input
// history) into the fanned-out sub-tool name + JSON arguments.
func buildCustomToolCallHistory(name string, input json.RawMessage) (string, string) {
	text := responsesOutputTextRaw(input)
	if name == "apply_patch" || strings.HasPrefix(text, "*** Begin Patch") {
		ops := parseApplyPatchOperations(text)
		if len(ops) == 1 {
			action := singleApplyPatchAction(mapGetString(ops[0], "type"))
			if action == "" {
				action = dto.CodexPatchActionBatch
			}
			return name + "_" + action, buildApplyPatchOperationArguments(ops[0], action)
		}
		opsAny := make([]any, len(ops))
		for i := range ops {
			opsAny[i] = ops[i]
		}
		return name + "_batch", marshalString(map[string]any{"operations": opsAny, "raw_patch": text})
	}
	return name, marshalString(map[string]any{"input": text})
}

// reconstructApplyPatchInput rebuilds apply_patch text from sub-tool arguments.
func reconstructApplyPatchInput(action, argsStr string) string {
	var v map[string]any
	if err := common.UnmarshalJsonStr(argsStr, &v); err != nil {
		return argsStr
	}
	for _, k := range []string{"raw_patch", "patch", "input"} {
		if s, ok := v[k].(string); ok && s != "" {
			return s
		}
	}
	var ops []map[string]any
	switch action {
	case dto.CodexPatchActionAddFile:
		ops = []map[string]any{{"type": "add_file", "path": mapGetString(v, "path"), "content": mapGetString(v, "content")}}
	case dto.CodexPatchActionDeleteFile:
		ops = []map[string]any{{"type": "delete_file", "path": mapGetString(v, "path")}}
	case dto.CodexPatchActionUpdateFile:
		op := map[string]any{"type": "update_file", "path": mapGetString(v, "path"), "hunks": hunksOrEmpty(v)}
		if mv := mapGetString(v, "move_to"); mv != "" {
			op["move_to"] = mv
		}
		ops = []map[string]any{op}
	case dto.CodexPatchActionReplaceFile:
		ops = []map[string]any{{"type": "replace_file", "path": mapGetString(v, "path"), "content": mapGetString(v, "content")}}
	default:
		for _, o := range mapGetSlice(v, "operations") {
			if om, ok := o.(map[string]any); ok {
				ops = append(ops, om)
			}
		}
	}
	return buildApplyPatchText(ops)
}

func reconstructCustomToolCallInput(argsStr string) string {
	var v map[string]any
	if err := common.UnmarshalJsonStr(argsStr, &v); err != nil {
		return argsStr
	}
	if s, ok := v["input"].(string); ok {
		return s
	}
	return argsStr
}

func reconstructCustomToolCallInputWithContext(ctx *dto.ResponsesToolContext, upstreamName, argsStr string) string {
	if spec, ok := ctx.LookupCustomTool(upstreamName); ok && spec.Kind == dto.CodexToolKindApplyPatch {
		return reconstructApplyPatchInput(spec.ProxyAction, argsStr)
	}
	return reconstructCustomToolCallInput(argsStr)
}

// ---------- argument string normalization ----------

func responsesArgumentsToChat(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	switch common.GetJsonType(raw) {
	case "string":
		var s string
		_ = common.Unmarshal(raw, &s)
		return normalizeChatToolArgumentsString(s)
	case "object":
		var v any
		_ = common.Unmarshal(raw, &v)
		return marshalString(v)
	default:
		var v any
		_ = common.Unmarshal(raw, &v)
		return marshalString(map[string]any{"input": v})
	}
}

func normalizeChatToolArgumentsString(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return "{}"
	}
	var v any
	if err := common.UnmarshalJsonStr(text, &v); err != nil {
		return marshalString(map[string]any{"input": text})
	}
	if _, ok := v.(map[string]any); ok {
		return text
	}
	return marshalString(map[string]any{"input": v})
}

func responsesOutputTextRaw(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if common.GetJsonType(raw) == "string" {
		var s string
		_ = common.Unmarshal(raw, &s)
		return s
	}
	var v any
	if err := common.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return marshalString(v)
}

// ---------- response: chat tool call -> Responses output item (name back-mapping) ----------

// ResponseToolCallItem maps an upstream (chat) tool call back to a Responses
// output item: a custom_tool_call (apply_patch / freeform / builtin) or a
// function_call (with namespace un-flattening). arguments is the chat arguments
// JSON string.
func ResponseToolCallItem(callID, name, arguments string, ctx *dto.ResponsesToolContext) map[string]any {
	if ctx.IsCustomToolProxy(name) {
		return map[string]any{
			"type":    "custom_tool_call",
			"id":      "ctc_" + callID,
			"status":  "completed",
			"call_id": callID,
			"name":    ctx.OriginalCustomToolName(name),
			"input":   reconstructCustomToolCallInputWithContext(ctx, name, arguments),
		}
	}
	display, namespace := ctx.OpenAINameForFunctionTool(name)
	item := map[string]any{
		"type":      "function_call",
		"id":        "fc_" + callID,
		"status":    "completed",
		"call_id":   callID,
		"name":      display,
		"arguments": arguments,
	}
	if namespace != "" {
		item["namespace"] = namespace
	}
	return item
}

// ---------- reasoning (per-provider) ----------

const (
	styleOpenRouter     = "openrouter"
	styleDeepSeek       = "deepseek"
	styleEnableThinking = "enable_thinking"
	styleThinking       = "thinking"
	styleReasoningSplit = "reasoning_split"
	styleLowHigh        = "low_high"
	styleDefault        = "default"
)

func isOpenAIOSeries(model string) bool {
	return len(model) > 1 && model[0] == 'o' && model[1] >= '0' && model[1] <= '9'
}

func inferChatReasoningStyle(model string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "openrouter"):
		return styleOpenRouter
	case strings.Contains(m, "deepseek"):
		return styleDeepSeek
	case strings.Contains(m, "qwen"), strings.Contains(m, "dashscope"), strings.Contains(m, "bailian"):
		return styleEnableThinking
	case strings.Contains(m, "kimi"), strings.Contains(m, "moonshot"), strings.Contains(m, "glm"),
		strings.Contains(m, "zhipu"), strings.Contains(m, "z.ai"), strings.Contains(m, "mimo"):
		return styleThinking
	case strings.Contains(m, "minimax"):
		return styleReasoningSplit
	case strings.Contains(m, "siliconflow"):
		return styleEnableThinking
	case strings.Contains(m, "stepfun"), strings.Contains(m, "step-3.5-flash-2603"):
		return styleLowHigh
	default:
		return styleDefault
	}
}

func mapChatReasoningEffort(effort, style string) string {
	e := strings.ToLower(strings.TrimSpace(effort))
	if e == "none" || e == "off" || e == "disabled" {
		return ""
	}
	switch style {
	case styleDeepSeek:
		if e == "max" || e == "xhigh" {
			return "max"
		}
		return "high"
	case styleLowHigh:
		if e == "minimal" || e == "low" {
			return "low"
		}
		return "high"
	case styleOpenRouter:
		switch e {
		case "max", "xhigh":
			return "xhigh"
		case "high", "medium", "low", "minimal":
			return e
		default:
			return ""
		}
	default:
		switch e {
		case "minimal", "low", "medium", "high", "xhigh", "max":
			return e
		default:
			return ""
		}
	}
}

func supportsReasoningEffort(model, style string) bool {
	if isOpenAIOSeries(model) {
		return true
	}
	m := strings.ToLower(model)
	if strings.HasPrefix(m, "gpt-") && len(m) > 4 && m[4] >= '5' && m[4] <= '9' {
		return true
	}
	return style == styleDeepSeek || style == styleLowHigh
}

func reasoningRequested(r *dto.Reasoning) (bool, bool) {
	if r == nil {
		return false, false
	}
	if r.Effort != "" {
		e := strings.ToLower(strings.TrimSpace(r.Effort))
		if e == "none" || e == "off" || e == "disabled" {
			return false, true
		}
		return true, true
	}
	return true, true
}

// applyChatReasoningOptions maps the Responses reasoning request onto the
// provider-specific Chat reasoning field(s) inferred from the model name.
func applyChatReasoningOptions(out *dto.GeneralOpenAIRequest, reasoning *dto.Reasoning, model string) {
	enabled, ok := reasoningRequested(reasoning)
	if !ok {
		return
	}
	style := inferChatReasoningStyle(model)
	switch style {
	case styleThinking:
		if enabled {
			out.THINKING = json.RawMessage(`{"type":"enabled"}`)
		} else {
			out.THINKING = json.RawMessage(`{"type":"disabled"}`)
		}
	case styleEnableThinking:
		out.EnableThinking = boolRaw(enabled)
	case styleReasoningSplit:
		out.ReasoningSplit = boolRaw(enabled)
	}
	if !enabled {
		if style == styleOpenRouter {
			out.Reasoning = json.RawMessage(`{"effort":"none"}`)
		}
		return
	}
	if reasoning == nil || reasoning.Effort == "" {
		return
	}
	mapped := mapChatReasoningEffort(reasoning.Effort, style)
	if mapped == "" {
		return
	}
	switch style {
	case styleOpenRouter:
		out.Reasoning = json.RawMessage(marshalString(map[string]any{"effort": mapped}))
	case styleDeepSeek, styleLowHigh, styleDefault:
		if supportsReasoningEffort(model, style) {
			out.ReasoningEffort = mapped
		}
	}
}
