// Package gemini implements the bounded native Gemini text/function wire contract.
// It contains transport shapes only; routing and money remain with their owners.
package gemini

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"micro-one-api/pkg/jsonx"
)

const DefaultOutputTokens int64 = 8192
const MaxRequestBytes = 4 << 20

var modelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ValidModel(model string) bool {
	return modelName.MatchString(model) && model != "." && model != ".."
}

// object accepts protobuf JSON's camelCase and snake_case spellings, rejecting
// aliases supplied together. User-owned schemas/arguments are never rewritten.
func object(raw []byte, fields ...string) (map[string]jsonx.RawMessage, error) {
	var source map[string]jsonx.RawMessage
	if jsonx.Unmarshal(raw, &source) != nil || source == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	out := make(map[string]jsonx.RawMessage, len(source))
	for key, value := range source {
		canonical := ""
		for _, field := range fields {
			if key == field || key == snake(field) {
				canonical = field
				break
			}
		}
		if canonical == "" {
			return nil, fmt.Errorf("unsupported Gemini field %q", key)
		}
		if _, exists := out[canonical]; exists || string(value) == "null" {
			return nil, fmt.Errorf("duplicate alias or null Gemini field %q", key)
		}
		out[canonical] = value
	}
	return out, nil
}

func snake(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsUpper(r) {
			b.WriteByte('_')
			b.WriteRune(unicode.ToLower(r))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func text(raw []byte) (string, bool) {
	var s string
	err := jsonx.Unmarshal(raw, &s)
	return s, err == nil && string(raw) != "null"
}

// PrepareRequest validates the supported capability set before any reservation
// or supplier send and inserts a finite output limit when the client omits it.
func PrepareRequest(body []byte) ([]byte, int64, error) {
	if err := jsonx.ValidateUnique(body); err != nil {
		return nil, 0, fmt.Errorf("invalid Gemini JSON: %w", err)
	}
	root, err := object(body, "contents", "systemInstruction", "tools", "toolConfig", "generationConfig", "safetySettings")
	if err != nil {
		return nil, 0, err
	}
	var contents []jsonx.RawMessage
	if jsonx.Unmarshal(root["contents"], &contents) != nil || len(contents) == 0 {
		return nil, 0, fmt.Errorf("contents must be a non-empty array")
	}
	for _, content := range contents {
		if err := validateContent(content, false); err != nil {
			return nil, 0, err
		}
	}
	if raw, ok := root["systemInstruction"]; ok {
		if err := validateContent(raw, true); err != nil {
			return nil, 0, err
		}
	}
	if raw, ok := root["tools"]; ok {
		if err := validateTools(raw); err != nil {
			return nil, 0, err
		}
	}
	if raw, ok := root["toolConfig"]; ok {
		config, err := object(raw, "functionCallingConfig")
		if err != nil {
			return nil, 0, err
		}
		calling, err := object(config["functionCallingConfig"], "mode", "allowedFunctionNames")
		if err != nil {
			return nil, 0, err
		}
		mode, _ := text(calling["mode"])
		if mode != "AUTO" && mode != "ANY" && mode != "NONE" && mode != "VALIDATED" {
			return nil, 0, fmt.Errorf("unsupported function calling mode")
		}
		if names, ok := calling["allowedFunctionNames"]; ok {
			var list []string
			if jsonx.Unmarshal(names, &list) != nil {
				return nil, 0, fmt.Errorf("invalid allowedFunctionNames")
			}
		}
	}
	config := map[string]jsonx.RawMessage{}
	if raw, ok := root["generationConfig"]; ok {
		config, err = object(raw, "temperature", "topP", "topK", "maxOutputTokens", "stopSequences", "candidateCount", "responseMimeType", "responseSchema", "responseJsonSchema", "thinkingConfig")
		if err != nil {
			return nil, 0, err
		}
	}
	limit := DefaultOutputTokens
	if raw, ok := config["maxOutputTokens"]; ok {
		if jsonx.Unmarshal(raw, &limit) != nil || limit <= 0 || limit > 65536 {
			return nil, 0, fmt.Errorf("maxOutputTokens must be between 1 and 65536")
		}
	}
	if raw, ok := config["candidateCount"]; ok && string(raw) != "1" {
		return nil, 0, fmt.Errorf("only candidateCount=1 is supported")
	}
	if raw, ok := config["responseMimeType"]; ok {
		mime, _ := text(raw)
		if mime != "text/plain" && mime != "application/json" {
			return nil, 0, fmt.Errorf("only text output is supported")
		}
	}
	if raw, ok := config["thinkingConfig"]; ok {
		thinking, err := object(raw, "thinkingBudget", "includeThoughts", "thinkingLevel")
		if err != nil {
			return nil, 0, err
		}
		if raw, ok := thinking["thinkingBudget"]; ok {
			var budget int64
			if jsonx.Unmarshal(raw, &budget) != nil || budget < -1 || budget > 65536 {
				return nil, 0, fmt.Errorf("invalid thinkingBudget")
			}
			if _, exists := thinking["thinkingLevel"]; exists {
				return nil, 0, fmt.Errorf("choose thinkingBudget or thinkingLevel")
			}
		}
		if raw, ok := thinking["thinkingLevel"]; ok {
			level, _ := text(raw)
			if level != "MINIMAL" && level != "LOW" && level != "MEDIUM" && level != "HIGH" {
				return nil, 0, fmt.Errorf("unsupported thinkingLevel")
			}
		}
		if raw, ok := thinking["includeThoughts"]; ok {
			var include bool
			if jsonx.Unmarshal(raw, &include) != nil {
				return nil, 0, fmt.Errorf("invalid includeThoughts")
			}
		}
	}
	for _, key := range []string{"temperature", "topP"} {
		if raw, ok := config[key]; ok {
			var value float64
			limit := 1.0
			if key == "temperature" {
				limit = 2
			}
			if jsonx.Unmarshal(raw, &value) != nil || value < 0 || value > limit {
				return nil, 0, fmt.Errorf("invalid %s", key)
			}
		}
	}
	if raw, ok := config["topK"]; ok {
		var value int64
		if jsonx.Unmarshal(raw, &value) != nil || value <= 0 {
			return nil, 0, fmt.Errorf("invalid topK")
		}
	}
	if raw, ok := config["stopSequences"]; ok {
		var stops []string
		if jsonx.Unmarshal(raw, &stops) != nil || len(stops) > 5 {
			return nil, 0, fmt.Errorf("invalid stopSequences")
		}
	}
	for _, key := range []string{"responseSchema", "responseJsonSchema"} {
		if raw, ok := config[key]; ok {
			var schema map[string]jsonx.RawMessage
			if jsonx.Unmarshal(raw, &schema) != nil || schema == nil {
				return nil, 0, fmt.Errorf("invalid %s", key)
			}
		}
	}
	if raw, ok := root["safetySettings"]; ok {
		var settings []jsonx.RawMessage
		if jsonx.Unmarshal(raw, &settings) != nil {
			return nil, 0, fmt.Errorf("invalid safetySettings")
		}
		for _, setting := range settings {
			if _, err := object(setting, "category", "threshold"); err != nil {
				return nil, 0, err
			}
		}
	}
	config["maxOutputTokens"], _ = jsonx.Marshal(limit)
	root["generationConfig"], _ = jsonx.Marshal(config)
	prepared, err := jsonx.Marshal(root)
	if len(prepared) > MaxRequestBytes {
		return nil, 0, fmt.Errorf("prepared Gemini request exceeds size limit")
	}
	return prepared, limit, err
}

func validateContent(raw []byte, system bool) error {
	content, err := object(raw, "role", "parts")
	if err != nil {
		return err
	}
	role := ""
	if raw, ok := content["role"]; ok {
		var valid bool
		role, valid = text(raw)
		if !valid || (role != "user" && role != "model" && role != "system") {
			return fmt.Errorf("invalid content role")
		}
	}
	if !system && role == "system" {
		return fmt.Errorf("use systemInstruction for system content")
	}
	var parts []jsonx.RawMessage
	if jsonx.Unmarshal(content["parts"], &parts) != nil || len(parts) == 0 {
		return fmt.Errorf("parts must be a non-empty array")
	}
	for _, part := range parts {
		if err := validatePart(part, role, system); err != nil {
			return err
		}
	}
	return nil
}

func validatePart(raw []byte, role string, system bool) error {
	part, err := object(raw, "text", "functionCall", "functionResponse", "thoughtSignature", "thought")
	if err != nil {
		return err
	}
	count := 0
	for _, key := range []string{"text", "functionCall", "functionResponse"} {
		if _, ok := part[key]; ok {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("part must contain exactly one text/function value")
	}
	if raw, ok := part["text"]; ok {
		if _, valid := text(raw); !valid {
			return fmt.Errorf("invalid part text")
		}
	}
	for _, key := range []string{"functionCall", "functionResponse"} {
		if raw, ok := part[key]; ok {
			if system || key == "functionCall" && role != "model" || key == "functionResponse" && role != "user" && role != "" {
				return fmt.Errorf("invalid function part role")
			}
			fields := []string{"id", "name", "args"}
			valueKey := "args"
			if key == "functionResponse" {
				fields, valueKey = []string{"id", "name", "response"}, "response"
			}
			fn, err := object(raw, fields...)
			if err != nil {
				return err
			}
			name, valid := text(fn["name"])
			if !valid || name == "" {
				return fmt.Errorf("function name is required")
			}
			if value, ok := fn[valueKey]; ok {
				var obj map[string]jsonx.RawMessage
				if jsonx.Unmarshal(value, &obj) != nil || obj == nil {
					return fmt.Errorf("function %s must be an object", valueKey)
				}
			} else if key == "functionResponse" {
				return fmt.Errorf("function response is required")
			}
		}
	}
	if raw, ok := part["thoughtSignature"]; ok {
		if _, valid := text(raw); !valid || role != "model" || system {
			return fmt.Errorf("invalid thoughtSignature")
		}
	}
	if raw, ok := part["thought"]; ok {
		var value bool
		if jsonx.Unmarshal(raw, &value) != nil || role != "model" || system {
			return fmt.Errorf("invalid thought flag")
		}
	}
	return nil
}

func validateTools(raw []byte) error {
	var tools []jsonx.RawMessage
	if jsonx.Unmarshal(raw, &tools) != nil {
		return fmt.Errorf("invalid tools")
	}
	for _, raw := range tools {
		tool, err := object(raw, "functionDeclarations")
		if err != nil {
			return err
		}
		var declarations []jsonx.RawMessage
		if jsonx.Unmarshal(tool["functionDeclarations"], &declarations) != nil || len(declarations) == 0 {
			return fmt.Errorf("functionDeclarations is required")
		}
		for _, raw := range declarations {
			fn, err := object(raw, "name", "description", "parameters", "parametersJsonSchema", "response", "responseJsonSchema")
			if err != nil {
				return err
			}
			name, valid := text(fn["name"])
			if !valid || name == "" {
				return fmt.Errorf("function declaration name is required")
			}
		}
	}
	return nil
}
