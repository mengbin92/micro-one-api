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

// IsKimiCLIClient inspects the inbound request headers to determine whether
// the caller is a genuine Kimi CLI client. Kimi For Coding is limited to the
// Kimi CLI, so only a genuine Kimi CLI request is forwarded as-is; all other
// clients are mimicked to look like a Kimi CLI session.
//
// Detection signals (any one is sufficient):
//   - User-Agent contains "kimi-cli"
//   - x-app: "kimi" header
func IsKimiCLIClient(header http.Header) bool {
	if header == nil {
		return false
	}
	if ua := header.Get("User-Agent"); strings.Contains(strings.ToLower(ua), "kimi-cli") {
		return true
	}
	if app := strings.ToLower(strings.TrimSpace(header.Get("x-app"))); app == "kimi" {
		return true
	}
	return false
}

// IsMimickableAccountType reports whether the account credential shape
// carries an upstream-recognised client identity that the relay should
// present to the upstream. OAuth accounts (Claude/Codex/Kimi) and static-key
// Coding-Plan accounts (GLM/MiniMax) both need mimicry; plain API-key accounts
// (empty or "api_key") do not. This is the single source of truth for the
// accountIsOAuth parameter ShouldMimic consumes.
func IsMimickableAccountType(accountType string) bool {
	switch accountType {
	case "", "api_key":
		return false
	case "oauth", "static_key", "setup_token":
		return true
	default:
		return false
	}
}

// ShouldMimic decides whether mimicry should be applied for the given
// platform. Mimicry is applied when the account is an OAuth subscription and
// the inbound client is NOT a genuine first-party client.
//
// Platform dispatch:
//   - Codex: mimic unless the inbound is a genuine codex_cli_rs client.
//   - Claude / Zhipu / MiniMax: these upstreams officially support Claude
//     Code, so the Claude Code client-detection + fingerprint path applies
//     unchanged. Mimic unless the inbound is a genuine Claude Code client.
//   - Kimi: the upstream limits use to its own CLI, so the Claude Code UA is
//     NOT a safe passthrough. P3 will mint a Kimi-CLI detector; until then
//     we always mimic Kimi accounts (no first-party passthrough) so we never
//     forward a raw third-party UA.
// ShouldMimic decides whether mimicry should be applied for the given
// platform/account. Mimicry is applied when the account uses a credential
// shape that carries an upstream-recognised client identity (OAuth refresh or
// static Coding-Plan key) AND the inbound client is NOT a genuine first-party
// client. Plain API-key accounts (account_type empty/"api_key") are never
// mimicked.
func ShouldMimic(platform Platform, accountIsOAuth bool, header http.Header) bool {
	if !accountIsOAuth {
		return false
	}
	switch platform {
	case PlatformCodex:
		return !IsCodexCLIClient(header)
	case PlatformKimi:
		return !IsKimiCLIClient(header)
	case PlatformClaude, PlatformZhipu, PlatformMinimax:
		return !IsClaudeCodeClient(header)
	default:
		return !IsClaudeCodeClient(header)
	}
}
