// Package evidence inspects native upstream metering without changing billing.
package evidence

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"

	"micro-one-api/pkg/jsonx"
)

const (
	ParserRevision   = "native-shadow-2026-10-13.1"
	ContractRevision = "native-static-2026-10-13.1"
	MaxFrameBytes    = 1 << 20
	MaxUsageBytes    = 32 << 10
)

// BuildRevision is embedded by the relay image build because .git is excluded
// from the Docker context. Unidentified binaries remain explicitly unknown.
var BuildRevision = "unknown"

// Observation belongs to the shadow parser, not the relay/billing wire contract.
// Buckets are uncached input, cache read, creation 5m, creation 1h and output.
type Observation struct {
	Protocol         string    `json:"protocol"`
	ParserRevision   string    `json:"parser_revision"`
	ContractRevision string    `json:"contract_revision"`
	Status           string    `json:"status"`
	Reason           string    `json:"reason,omitempty"`
	Semantics        string    `json:"semantics,omitempty"`
	Buckets          *[5]int64 `json:"buckets,omitempty"`
	Present          []string  `json:"present,omitempty"`
	Fingerprint      []string  `json:"fingerprint,omitempty"`
	UsageDigest      string    `json:"usage_digest,omitempty"`
	// Strings in usage are redacted; content outside the usage subtree is absent.
	Usage             jsonx.RawMessage `json:"usage,omitempty"`
	Event             string           `json:"event,omitempty"`
	ProviderRequestID string           `json:"provider_request_id,omitempty"`
	Terminal          string           `json:"terminal,omitempty"`
}

// Parse only follows native protocol locations, never recursively searches
// model-generated content for an object that happens to look like usage.
func Parse(body []byte, protocol string) Observation {
	o := Observation{Protocol: protocol, ParserRevision: ParserRevision, ContractRevision: ContractRevision, Status: "missing", Reason: "usage_absent"}
	if len(body) > MaxFrameBytes {
		o.Status, o.Reason = "unobserved", "frame_limit"
		return o
	}
	value, err := decodeUnique(body)
	if err != nil {
		o.Status, o.Reason = "invalid", err.Error()
		if o.Reason == "json_complexity_limit" || o.Reason == "invalid_field_name" {
			o.Status = "unobserved"
		}
		return o
	}
	root, ok := value.(map[string]any)
	if !ok {
		o.Status, o.Reason = "invalid", "response_not_object"
		return o
	}
	o.Event, _ = root["type"].(string)
	if !knownEvent(o.Event) {
		o.Event = "other"
	}
	parent := root
	if protocol == "responses" {
		if nested, ok := root["response"].(map[string]any); ok {
			parent = nested
		}
	} else if protocol == "anthropic_messages" {
		if nested, ok := root["message"].(map[string]any); ok {
			parent = nested
		}
	}
	if id, ok := parent["id"].(string); ok && len(id) <= 256 {
		o.ProviderRequestID = id
	}
	o.Terminal = terminalState(root, parent, protocol, o.Event)
	key := "usage"
	if protocol == "gemini" {
		key = "usageMetadata"
	}
	raw, exists := parent[key]
	if !exists {
		return o
	}
	// Chat streaming content chunks carry usage:null; the final usage chunk
	// supplies the object. A null usage in a non-stream response stays invalid.
	if raw == nil && protocol == "openai_chat" && root["object"] == "chat.completion.chunk" {
		return o
	}
	m, ok := raw.(map[string]any)
	if !ok {
		o.Status, o.Reason = "invalid", "usage_not_object"
		return o
	}
	encoded, _ := jsonx.Marshal(m)
	o.UsageDigest = fmt.Sprintf("%x", sha256.Sum256(encoded))
	if len(encoded) > MaxUsageBytes {
		o.Status, o.Reason = "unobserved", "usage_limit"
		return o
	}
	flat := map[string]any{}
	flatten(m, "", flat, &o.Fingerprint)
	sort.Strings(o.Fingerprint)
	for path := range flat {
		o.Present = append(o.Present, path)
	}
	sort.Strings(o.Present)
	o.Usage, _ = jsonx.Marshal(redact(m))

	var b [5]int64
	var input, output string
	known := map[string]bool{}
	var invalid, unsupported string
	read := func(path string) (int64, bool) {
		known[path] = true
		v, present := flat[path]
		if !present {
			return 0, false
		}
		n, ok := v.(jsonx.Number)
		if !ok {
			invalid = "token_type"
			return 0, true
		}
		count, err := n.Int64()
		if err != nil {
			invalid = "token_integer_or_overflow"
			return 0, true
		}
		if count < 0 {
			invalid = "negative_bucket"
		}
		return count, true
	}
	switch protocol {
	case "openai_chat":
		input, output = "/prompt_tokens", "/completion_tokens"
	case "responses":
		input, output = "/input_tokens", "/output_tokens"
	case "anthropic_messages":
		input, output = "/input_tokens", "/output_tokens"
		b[1], _ = read("/cache_read_input_tokens")
		creation, hasCreation := read("/cache_creation_input_tokens")
		var has5m, has1h bool
		b[2], has5m = read("/cache_creation/ephemeral_5m_input_tokens")
		b[3], has1h = read("/cache_creation/ephemeral_1h_input_tokens")
		known["/cache_creation"] = true
		if !has5m && !has1h {
			b[2] = creation
		} else if hasCreation && (b[2] > math.MaxInt64-b[3] || b[2]+b[3] != creation) {
			invalid = "cache_creation_total_conflict"
		}
	case "gemini":
		input, output = "/promptTokenCount", "/candidatesTokenCount"
		b[1], _ = read("/cachedContentTokenCount")
		// Thinking is intentionally a candidate dimension until independently
		// verified for the endpoint/version; it cannot silently disappear.
	default:
		o.Status, o.Reason = "unsupported", "protocol_not_installed"
		return o
	}
	var hasInput, hasOutput bool
	b[0], hasInput = read(input)
	b[4], hasOutput = read(output)
	if protocol == "openai_chat" || protocol == "responses" {
		details := "/prompt_tokens_details"
		outDetails := "/completion_tokens_details"
		if protocol == "responses" {
			details, outDetails = "/input_tokens_details", "/output_tokens_details"
		}
		known[details], known[outDetails] = true, true
		for _, path := range []string{details, outDetails} {
			if value, present := flat[path]; present {
				if _, ok := value.(map[string]any); !ok {
					invalid = "detail_not_object"
				}
			}
		}
		b[1], _ = read(details + "/cached_tokens")
		// Reasoning is an output subset in these installed contracts.
		if reasoning, present := read(outDetails + "/reasoning_tokens"); present && reasoning > b[4] {
			invalid = "reasoning_exceeds_reported_output"
		}
		read(outDetails + "/accepted_prediction_tokens")
		read(outDetails + "/rejected_prediction_tokens")
		for _, p := range []string{details + "/audio_tokens", outDetails + "/audio_tokens"} {
			if n, _ := read(p); n != 0 {
				unsupported = "audio_metering"
			}
		}
	}
	totalPath := "/total_tokens"
	if protocol == "gemini" {
		totalPath = "/totalTokenCount"
	}
	read(totalPath) // Reported total remains audit-only.
	if value, present := flat["/cache_creation"]; present && protocol == "anthropic_messages" {
		if _, ok := value.(map[string]any); !ok {
			invalid = "detail_not_object"
		}
	}
	for _, path := range o.Present {
		if !known[path] {
			unsupported = "unknown_usage_field"
			break
		}
	}
	if invalid != "" {
		o.Status, o.Reason = "invalid", invalid
		return o
	}
	if unsupported != "" {
		o.Status, o.Reason = "unsupported", unsupported
		return o
	}
	if !hasInput || !hasOutput {
		o.Status, o.Reason = "missing", "required_token_field_absent"
		return o
	}
	if protocol == "anthropic_messages" {
		o.Semantics = "anthropic_exclusive"
	} else {
		o.Semantics = "openai_subset"
		if b[1] > b[0] {
			// Both conservative interpretations must fit in the wire contract.
			// An ambiguous payload cannot bypass the canonical overflow guard.
			var candidateTotal int64
			for _, count := range b {
				if count > math.MaxInt64-candidateTotal {
					o.Status, o.Reason = "invalid", "overflow"
					return o
				}
				candidateTotal += count
			}
			o.Status, o.Reason = "ambiguous", "cached_exceeds_reported_prompt"
			return o
		}
		b[0] -= b[1]
	}
	var sum int64
	for _, count := range b {
		if count > math.MaxInt64-sum {
			o.Status, o.Reason = "invalid", "overflow"
			return o
		}
		sum += count
	}
	o.Status, o.Reason, o.Buckets = "verified", "", &b
	return o
}

func terminalState(root, parent map[string]any, protocol, event string) string {
	status, _ := parent["status"].(string)
	switch status {
	case "failed", "incomplete", "cancelled", "canceled":
		return "failed"
	}
	switch event {
	case "error", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return "failed"
	case "message_stop", "response.completed", "response.done":
		return "completed"
	}
	if protocol == "openai_chat" {
		if choices, ok := root["choices"].([]any); ok {
			for _, item := range choices {
				if choice, ok := item.(map[string]any); ok {
					if reason, ok := choice["finish_reason"].(string); ok && reason != "" {
						if reason == "error" {
							return "failed"
						}
						return "completed"
					}
				}
			}
		}
	}
	if protocol == "gemini" {
		if candidates, ok := root["candidates"].([]any); ok {
			for _, item := range candidates {
				if c, ok := item.(map[string]any); ok {
					if reason, ok := c["finishReason"].(string); ok && reason != "" {
						return "completed"
					}
				}
			}
		}
	}
	return ""
}

func knownEvent(event string) bool {
	switch event {
	case "", "message_start", "message_delta", "message_stop", "error", "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}

func flatten(value any, path string, out map[string]any, shape *[]string) {
	switch v := value.(type) {
	case map[string]any:
		if path != "" {
			out[path] = v
			*shape = append(*shape, path+":object")
		}
		for k, child := range v {
			flatten(child, path+"/"+strings.ReplaceAll(strings.ReplaceAll(k, "~", "~0"), "/", "~1"), out, shape)
		}
	case []any:
		out[path] = v
		*shape = append(*shape, path+":array")
		for i, child := range v {
			flatten(child, path+"/"+strconv.Itoa(i), out, shape)
		}
	default:
		kind := "null"
		switch v.(type) {
		case jsonx.Number:
			kind = "number"
		case string:
			kind = "string"
		case bool:
			kind = "boolean"
		}
		out[path] = v
		*shape = append(*shape, path+":"+kind)
	}
}

func redact(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = redact(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = redact(child)
		}
		return out
	case string:
		return "[redacted]"
	default:
		return value
	}
}

func decodeUnique(body []byte) (any, error) {
	d := jsonx.NewTokenDecoder(bytes.NewReader(body))
	d.UseNumber()
	nodes := 0
	var parse func(int) (any, error)
	parse = func(depth int) (any, error) {
		nodes++
		if depth > 32 || nodes > 8192 {
			return nil, fmt.Errorf("json_complexity_limit")
		}
		token, err := d.Token()
		if err != nil {
			return nil, fmt.Errorf("malformed_json")
		}
		switch token {
		case jsonx.Delim('{'):
			out := map[string]any{}
			for d.More() {
				k, err := d.Token()
				if err != nil {
					return nil, fmt.Errorf("malformed_json")
				}
				key, ok := k.(string)
				if !ok || len(key) > 256 {
					return nil, fmt.Errorf("invalid_field_name")
				}
				if _, exists := out[key]; exists {
					return nil, fmt.Errorf("duplicate_key")
				}
				v, err := parse(depth + 1)
				if err != nil {
					return nil, err
				}
				out[key] = v
			}
			end, err := d.Token()
			if err != nil || end != jsonx.Delim('}') {
				return nil, fmt.Errorf("malformed_json")
			}
			return out, nil
		case jsonx.Delim('['):
			out := []any{}
			for d.More() {
				v, err := parse(depth + 1)
				if err != nil {
					return nil, err
				}
				out = append(out, v)
			}
			end, err := d.Token()
			if err != nil || end != jsonx.Delim(']') {
				return nil, fmt.Errorf("malformed_json")
			}
			return out, nil
		default:
			if _, isDelim := token.(jsonx.Delim); isDelim {
				return nil, fmt.Errorf("malformed_json")
			}
			return token, nil
		}
	}
	v, err := parse(0)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("trailing_json")
	}
	return v, nil
}
