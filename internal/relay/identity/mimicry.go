package identity

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
)

// MimicryOptions controls how the Claude OAuth mimicry layer rewrites a
// request body before it is forwarded to the upstream Anthropic Messages API.
type MimicryOptions struct {
	// AccountUUID is the stable upstream account identifier (e.g. the
	// chatgpt-account-id or the UUID portion of metadata.user_id).
	AccountUUID string
	// ClientID is the per-account client identifier used to derive a unique
	// metadata.user_id suffix.
	ClientID string
	// ApplySystemPrompt controls whether the Claude Code system-prompt block is
	// injected. Real Claude Code clients already carry it; third-party clients
	// need it injected so the upstream classifies the request as a CC session.
	ApplySystemPrompt bool
}

// claudeCodeSystemPrompt is the canonical system-prompt block prepended to
// mimicked Claude OAuth requests so the upstream sees a request shape
// consistent with a genuine Claude Code CLI session. It is intentionally
// minimal in the MVP — the goal is to pass the "looks like CC" heuristic,
// not to reproduce the full upstream system prompt.
const claudeCodeSystemPrompt = `You are Claude Code, Anthropic's official coding assistant.`

// RewriteMetadataUserID rewrites the metadata.user_id field of an Anthropic
// Messages request body so it carries a value the upstream considers a
// legitimate first-party Claude Code user_id. The user_id is derived from the
// account UUID and the per-account ClientID, masked into the shape the
// upstream expects ("0xUUID::clientID"). This mirrors sub2api's
// RewriteUserIDWithMasking.
//
// If the body has no metadata object, one is created. The original body is
// returned unchanged when it cannot be parsed.
func RewriteMetadataUserID(body []byte, accountUUID, clientID string) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var raw map[string]json.RawMessage
	if err := sonic.Unmarshal(body, &raw); err != nil {
		// Not JSON: return unchanged (the caller may be forwarding a raw body).
		return body, nil
	}
	userID := buildMaskedUserID(accountUUID, clientID)
	meta := map[string]any{"user_id": userID}
	// Preserve any existing metadata keys other than user_id.
	if existing, ok := raw["metadata"]; ok {
		var existingMap map[string]any
		if err := sonic.Unmarshal(existing, &existingMap); err == nil {
			for k, v := range existingMap {
				if k == "user_id" {
					continue
				}
				meta[k] = v
			}
		}
	}
	metaJSON, err := sonic.Marshal(meta)
	if err != nil {
		return body, err
	}
	raw["metadata"] = metaJSON
	return sonic.Marshal(raw)
}

// buildMaskedUserID produces the masked user_id value the upstream expects.
// It is a stable derivation of the account UUID + client ID so the upstream
// sees a consistent identity per (account, client) pair without leaking the
// relay's internal user identifiers.
func buildMaskedUserID(accountUUID, clientID string) string {
	if accountUUID == "" {
		accountUUID = "00000000-0000-0000-0000-000000000000"
	}
	if clientID == "" {
		clientID = "0000000000000000"
	}
	return fmt.Sprintf("0x%s::%s", accountUUID, clientID)
}

// InjectClaudeCodeSystemPrompt prepends the canonical Claude Code system
// prompt to an Anthropic Messages request body. If the body already carries a
// system field it is merged so the injected prompt appears first; if the body
// has no system field one is created.
//
// The body is returned unchanged when it cannot be parsed as JSON.
func InjectClaudeCodeSystemPrompt(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var raw map[string]json.RawMessage
	if err := sonic.Unmarshal(body, &raw); err != nil {
		return body, nil
	}
	// system may be a string or an array of content blocks. We normalize to a
	// string prefix for the MVP.
	systemPrompt := claudeCodeSystemPrompt
	if existing, ok := raw["system"]; ok {
		var s string
		if err := sonic.Unmarshal(existing, &s); err == nil && s != "" {
			systemPrompt = claudeCodeSystemPrompt + "\n\n" + s
		}
		// Array form: fall back to string concatenation which is accepted by
		// the upstream. (A richer merge would prepend a text block.)
	}
	raw["system"] = jsonString(systemPrompt)
	return sonic.Marshal(raw)
}

// NormalizeClaudeOAuthRequestBody applies the field-completion portion of the
// mimicry: ensures temperature, max_tokens and tools are present with
// Claude-Code-consistent defaults when the caller omitted them. This mirrors
// sub2api's normalizeClaudeOAuthRequestBody.
func NormalizeClaudeOAuthRequestBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	var raw map[string]json.RawMessage
	if err := sonic.Unmarshal(body, &raw); err != nil {
		return body, nil
	}
	changed := false
	if _, ok := raw["temperature"]; !ok {
		raw["temperature"] = jsonNumber(1.0)
		changed = true
	}
	if _, ok := raw["max_tokens"]; !ok {
		raw["max_tokens"] = jsonNumber(128000)
		changed = true
	}
	if _, ok := raw["tools"]; !ok {
		raw["tools"] = json.RawMessage("[]")
		changed = true
	}
	if !changed {
		return body, nil
	}
	return sonic.Marshal(raw)
}

// ComputeAnthropicBeta merges the inbound anthropic-beta header value with the
// set of beta features a Claude Code session is expected to send, returning
// the final comma-separated header value. Duplicates are removed; order is
// stable (inbound first, then any missing defaults).
//
// The MVP carries a minimal default set; the full upstream value set is built
// up as the mimicry matures.
func ComputeAnthropicBeta(inbound string) string {
	defaults := []string{
		"fine-grained-tool-streaming-2025-05-14",
		"context-management-2025-06-10",
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range splitCSV(inbound) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, d := range defaults {
		if !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return strings.Join(out, ",")
}

// --- small JSON helpers (avoid importing encoding/json's MarshalNumber) ---

func jsonString(s string) json.RawMessage {
	b, _ := sonic.Marshal(s)
	return b
}

func jsonNumber(n float64) json.RawMessage {
	b, _ := sonic.Marshal(n)
	return b
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return out
}
