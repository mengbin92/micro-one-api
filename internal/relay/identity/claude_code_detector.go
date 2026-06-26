package identity

import (
	"net/http"
	"strings"
)

// IsClaudeCodeClient inspects the inbound request headers and body shape to
// determine whether the caller is a genuine Claude Code CLI client. This
// controls whether mimicry is applied: real Claude Code clients are forwarded
// as-is, while third-party clients (the common case) are mimicked to look like
// a first-party client so the upstream treats them as a legitimate Claude
// Code session.
//
// Detection signals (any one is sufficient):
//   - User-Agent contains "claude-cli"
//   - x-app: "cli" header
//   - anthropic-beta contains "claude-code" or a code-specific feature flag
func IsClaudeCodeClient(header http.Header) bool {
	if header == nil {
		return false
	}
	if ua := header.Get("User-Agent"); strings.Contains(strings.ToLower(ua), "claude-cli") {
		return true
	}
	if app := strings.ToLower(strings.TrimSpace(header.Get("x-app"))); app == "cli" {
		return true
	}
	if beta := strings.ToLower(header.Get("anthropic-beta")); strings.Contains(beta, "claude-code") {
		return true
	}
	return false
}

// IsCodexCLIClient inspects the inbound request to determine whether the
// caller is a genuine codex_cli_rs client. Real Codex CLI clients are
// forwarded as-is; third-party clients are mimicked.
func IsCodexCLIClient(header http.Header) bool {
	if header == nil {
		return false
	}
	if ua := header.Get("User-Agent"); strings.Contains(strings.ToLower(ua), "codex_cli") {
		return true
	}
	if originator := strings.ToLower(strings.TrimSpace(header.Get("originator"))); originator == "codex_cli_rs" {
		return true
	}
	return false
}

// ShouldMimic decides whether mimicry should be applied for the given
// platform. Mimicry is applied when the account is an OAuth subscription and
// the inbound client is NOT a genuine first-party client.
func ShouldMimic(platform Platform, accountIsOAuth bool, header http.Header) bool {
	if !accountIsOAuth {
		return false
	}
	switch platform {
	case PlatformCodex:
		return !IsCodexCLIClient(header)
	default:
		return !IsClaudeCodeClient(header)
	}
}
