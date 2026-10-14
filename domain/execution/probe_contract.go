package execution

import (
	"bytes"
	"fmt"
	"micro-one-api/pkg/jsonx"
	"strings"
)

const ProbeTool = "p1_probe"

func AddProbeTool(body map[string]any, protocol string) {
	schema := map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}
	switch protocol {
	case "openai_chat":
		body["tools"] = []any{map[string]any{"type": "function", "function": map[string]any{"name": ProbeTool, "description": "Return OK", "parameters": schema}}}
		body["tool_choice"] = map[string]any{"type": "function", "function": map[string]any{"name": ProbeTool}}
	case "responses":
		body["tools"] = []any{map[string]any{"type": "function", "name": ProbeTool, "description": "Return OK", "parameters": schema}}
		body["tool_choice"] = map[string]any{"type": "function", "name": ProbeTool}
	case "anthropic_messages":
		body["tools"] = []any{map[string]any{"name": ProbeTool, "description": "Return OK", "input_schema": schema}}
		body["tool_choice"] = map[string]any{"type": "tool", "name": ProbeTool}
	case "gemini":
		body["tools"] = []any{map[string]any{"functionDeclarations": []any{map[string]any{"name": ProbeTool, "description": "Return OK", "parameters": schema}}}}
		body["toolConfig"] = map[string]any{"functionCallingConfig": map[string]any{"mode": "ANY", "allowedFunctionNames": []string{ProbeTool}}}
	}
}

// ValidateProbeOutput only follows known output locations, never searches model text for a marker.
func ValidateProbeOutput(raw []byte, protocol string, stream, tools bool) error {
	raw = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
	frames := [][]byte{raw}
	if stream {
		frames = nil
		for _, event := range bytes.Split(raw, []byte("\n\n")) {
			var data []byte
			for _, line := range bytes.Split(event, []byte("\n")) {
				if p, ok := bytes.CutPrefix(bytes.TrimSuffix(line, []byte("\r")), []byte("data:")); ok {
					if len(data) > 0 {
						data = append(data, '\n')
					}
					data = append(data, bytes.TrimSpace(p)...)
				}
			}
			if len(data) > 0 && !bytes.Equal(data, []byte("[DONE]")) {
				frames = append(frames, data)
			}
		}
	}
	sawOutput, sawTool := false, false
	type toolCall struct {
		name, arguments string
		delta           bool
	}
	chatCalls := map[string]toolCall{}
	anthropicCalls := map[string]toolCall{}
	checkTool := func(name string, input any) {
		if name == ProbeTool && probeEmptyInput(input) {
			sawOutput, sawTool = true, true
		}
	}
	checkResponseItem := func(o map[string]any) {
		if o["type"] == "function_call" {
			name, _ := o["name"].(string)
			checkTool(name, o["arguments"])
		}
		if o["type"] == "message" {
			sawOutput = sawOutput || probeText(o["content"])
		}
	}
	for _, frame := range frames {
		var root map[string]any
		if jsonx.ValidateUnique(frame) != nil || jsonx.Unmarshal(frame, &root) != nil {
			return fmt.Errorf("probe output is not JSON")
		}
		if nested, ok := root["response"].(map[string]any); ok {
			root = nested
		}
		switch protocol {
		case "openai_chat":
			if choices, ok := root["choices"].([]any); ok {
				for _, v := range choices {
					c, _ := v.(map[string]any)
					m, _ := c["message"].(map[string]any)
					if m == nil {
						m, _ = c["delta"].(map[string]any)
					}
					if m != nil {
						sawOutput = sawOutput || probeText(m["content"])
						if calls, ok := m["tool_calls"].([]any); ok {
							for idx, call := range calls {
								o, _ := call.(map[string]any)
								f, _ := o["function"].(map[string]any)
								name, _ := f["name"].(string)
								if !stream {
									checkTool(name, f["arguments"])
									continue
								}
								index := o["index"]
								if index == nil {
									index = idx
								}
								key := fmt.Sprintf("%v/%v", c["index"], index)
								merged := chatCalls[key]
								if name != "" {
									merged.name += name
								}
								args, _ := f["arguments"].(string)
								merged.arguments += args
								chatCalls[key] = merged
							}
						}
					}
				}
			}
		case "responses":
			if output, ok := root["output"].([]any); ok {
				for _, v := range output {
					o, _ := v.(map[string]any)
					checkResponseItem(o)
				}
			}
			if item, ok := root["item"].(map[string]any); ok {
				checkResponseItem(item)
			}
			if root["type"] == "response.output_text.delta" {
				sawOutput = sawOutput || probeText(root["delta"])
			} else if root["type"] == "response.output_text.done" {
				sawOutput = sawOutput || probeText(root["text"])
			}
		case "anthropic_messages":
			if content, ok := root["content"].([]any); ok {
				for _, v := range content {
					o, _ := v.(map[string]any)
					if o["type"] == "text" {
						sawOutput = sawOutput || probeText(o["text"])
					} else if o["type"] == "tool_use" {
						name, _ := o["name"].(string)
						checkTool(name, o["input"])
					}
				}
			}
			if block, ok := root["content_block"].(map[string]any); ok {
				if block["type"] == "text" {
					sawOutput = sawOutput || probeText(block["text"])
				} else if block["type"] == "tool_use" {
					name, _ := block["name"].(string)
					args, _ := jsonx.Marshal(block["input"])
					anthropicCalls[fmt.Sprint(root["index"])] = toolCall{name: name, arguments: string(args)}
				}
			}
			if delta, ok := root["delta"].(map[string]any); ok {
				if delta["type"] == "text_delta" {
					sawOutput = sawOutput || probeText(delta["text"])
				} else if delta["type"] == "input_json_delta" {
					key := fmt.Sprint(root["index"])
					merged := anthropicCalls[key]
					// input:{} on content_block_start is a placeholder.
					if !merged.delta {
						merged.arguments = ""
						merged.delta = true
					}
					part, _ := delta["partial_json"].(string)
					merged.arguments += part
					anthropicCalls[key] = merged
				}
			}
		case "gemini":
			if candidates, ok := root["candidates"].([]any); ok {
				for _, v := range candidates {
					c, _ := v.(map[string]any)
					content, _ := c["content"].(map[string]any)
					parts, _ := content["parts"].([]any)
					for _, v := range parts {
						p, _ := v.(map[string]any)
						sawOutput = sawOutput || probeText(p["text"])
						if f, ok := p["functionCall"].(map[string]any); ok {
							name, _ := f["name"].(string)
							checkTool(name, f["args"])
						}
					}
				}
			}
		}
	}
	for _, calls := range []map[string]toolCall{chatCalls, anthropicCalls} {
		for _, call := range calls {
			checkTool(call.name, call.arguments)
		}
	}
	if !sawOutput || tools && !sawTool {
		return fmt.Errorf("probe output capability missing")
	}
	return nil
}

// The local fake tool has an empty object schema with additionalProperties=false.
func probeEmptyInput(input any) bool {
	if text, ok := input.(string); ok {
		if jsonx.ValidateUnique([]byte(text)) != nil || jsonx.Unmarshal([]byte(text), &input) != nil {
			return false
		}
	}
	obj, ok := input.(map[string]any)
	return ok && obj != nil && len(obj) == 0
}

func probeText(content any) bool {
	if text, ok := content.(string); ok {
		return strings.TrimSpace(text) != ""
	}
	if parts, ok := content.([]any); ok {
		for _, part := range parts {
			if p, ok := part.(map[string]any); ok && (p["type"] == "text" || p["type"] == "output_text") {
				if text, ok := p["text"].(string); ok && strings.TrimSpace(text) != "" {
					return true
				}
			}
		}
	}
	return false
}
