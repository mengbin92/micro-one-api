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
					data = append(data, bytes.TrimSpace(p)...)
				}
			}
			if len(data) > 0 && !bytes.Equal(data, []byte("[DONE]")) {
				frames = append(frames, data)
			}
		}
	}
	sawOutput, sawTool := false, false
	for _, frame := range frames {
		var root map[string]any
		if jsonx.Unmarshal(frame, &root) != nil {
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
						sawOutput = true
						if calls, ok := m["tool_calls"].([]any); ok {
							for _, call := range calls {
								o, _ := call.(map[string]any)
								f, _ := o["function"].(map[string]any)
								sawTool = sawTool || f["name"] == ProbeTool
							}
						}
					}
				}
			}
		case "responses":
			if output, ok := root["output"].([]any); ok {
				for _, v := range output {
					o, _ := v.(map[string]any)
					sawOutput = true
					sawTool = sawTool || o["type"] == "function_call" && o["name"] == ProbeTool
				}
			}
			if item, ok := root["item"].(map[string]any); ok {
				sawOutput = true
				sawTool = sawTool || item["type"] == "function_call" && item["name"] == ProbeTool
			}
			if typ, _ := root["type"].(string); strings.HasPrefix(typ, "response.output_text.") {
				sawOutput = true
			}
		case "anthropic_messages":
			if content, ok := root["content"].([]any); ok {
				for _, v := range content {
					o, _ := v.(map[string]any)
					sawOutput = true
					sawTool = sawTool || o["type"] == "tool_use" && o["name"] == ProbeTool
				}
			}
			if block, ok := root["content_block"].(map[string]any); ok {
				sawOutput = true
				sawTool = sawTool || block["type"] == "tool_use" && block["name"] == ProbeTool
			}
			if root["delta"] != nil {
				sawOutput = true
			}
		case "gemini":
			if candidates, ok := root["candidates"].([]any); ok {
				for _, v := range candidates {
					c, _ := v.(map[string]any)
					content, _ := c["content"].(map[string]any)
					parts, _ := content["parts"].([]any)
					for _, v := range parts {
						p, _ := v.(map[string]any)
						sawOutput = true
						if f, ok := p["functionCall"].(map[string]any); ok {
							sawTool = sawTool || f["name"] == ProbeTool
						}
					}
				}
			}
		}
	}
	if !sawOutput || tools && !sawTool {
		return fmt.Errorf("probe output capability missing")
	}
	return nil
}
