// Package identity provides the identity-mimicry layer for subscription
// accounts (Codex / Claude Code), ported conceptually from sub2api's
// identity_service + Fingerprint system.
//
// MVP scope (plan §十): the layer is limited to three mimicry dimensions —
// request headers, metadata rewriting and a cached TLS/client fingerprint
// snapshot. It does not attempt deeper behavioural simulation.
package identity

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"runtime"

	"github.com/bytedance/sonic"
)

// Platform identifies a subscription-account platform.
type Platform string

const (
	// PlatformCodex is the ChatGPT / Codex (Responses API) subscription.
	PlatformCodex Platform = "codex"
	// PlatformClaude is the Claude Code (Anthropic Messages API) subscription.
	PlatformClaude Platform = "claude"
	// PlatformZhipu is the Zhipu GLM Coding Plan (Anthropic-compatible
	// Messages API, static API key). See
	// docs/design/cn-subscription-accounts-roadmap.md.
	PlatformZhipu Platform = "zhipu"
	// PlatformMinimax is the MiniMax Coding Plan (Anthropic-compatible
	// Messages API, static API key).
	PlatformMinimax Platform = "minimax"
	// PlatformKimi is the Kimi For Coding subscription (Anthropic-compatible
	// Messages API, OAuth refresh token).
	PlatformKimi Platform = "kimi"
)

// Fingerprint is a cached snapshot of the client identity an account presents
// to the upstream. It mirrors sub2api's Fingerprint struct: every subscription
// account maintains a stable-but-unique set of stainless SDK / User-Agent
// values so the upstream sees a population of legitimate first-party clients
// rather than a single relay.
type Fingerprint struct {
	// ClientID is the opaque client identifier sent in x-app or user_id
	// derivations (e.g. the UUID portion of the Claude metadata.user_id).
	ClientID string `json:"client_id"`

	// UserAgent is the full HTTP User-Agent header value.
	UserAgent string `json:"user_agent"`

	// Stainless* fields mirror the x-stainless-* headers emitted by the
	// official SDKs. They make a request look like it originated from the
	// native Anthropic/OpenAI SDK rather than a generic HTTP client.
	StainlessLang           string `json:"stainless_lang"`
	StainlessPackageVersion string `json:"stainless_package_version"`
	StainlessOS             string `json:"stainless_os"`
	StainlessArch           string `json:"stainless_arch"`
	StainlessRuntime        string `json:"stainless_runtime"`
	StainlessRuntimeVersion string `json:"stainless_runtime_version"`
}

// FingerprintSnapshot is an opaque, serializable representation of a
// Fingerprint cached alongside an account (e.g. in the SubscriptionAccount
// record or Redis). The relay treats it as a blob; only this package
// interprets it.
type FingerprintSnapshot string

// Snapshot serializes the fingerprint into an opaque cache token that encodes
// the *full* fingerprint (not just the ClientID). This is critical for
// stability: when the in-process cache expires or the process restarts, the
// fingerprint is reconstructed from the snapshot stored alongside the account
// (DB column / Redis). If only the ClientID survived, every cache miss would
// regenerate the Stainless*/UserAgent fields from defaults and the upstream
// would see a drifting client identity — defeating the purpose of mimicry.
//
// The token is versioned ("v2:") and contains a compact JSON blob. It is
// opaque to callers; only RestoreFromSnapshot interprets it.
func (f Fingerprint) Snapshot() FingerprintSnapshot {
	b, err := sonic.Marshal(f)
	if err != nil {
		// Marshalling a plain struct should never fail; fall back to the
		// legacy v1 token (ClientID only) so we never return an empty snapshot.
		return FingerprintSnapshot(fmt.Sprintf("v1:%s", f.ClientID))
	}
	return FingerprintSnapshot("v2:" + string(b))
}

// DefaultClaudeCodeFingerprint builds a Fingerprint that resembles a recent
// official Claude Code CLI client. The version strings track the published
// @anthropic-ai/sdk defaults.
func DefaultClaudeCodeFingerprint() Fingerprint {
	return Fingerprint{
		ClientID:                randomClientID(),
		UserAgent:               "claude-cli/1.0.128 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.52.0",
		StainlessOS:             stainlessOS(),
		StainlessArch:           stainlessArch(),
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v22.11.0",
	}
}

// DefaultCodexFingerprint builds a Fingerprint that resembles the official
// codex_cli_rs client.
func DefaultCodexFingerprint() Fingerprint {
	return Fingerprint{
		ClientID:                randomClientID(),
		UserAgent:               fmt.Sprintf("codex_cli_rs/0.39.0 (%s; %s) zsh", stainlessOS(), stainlessArch()),
		StainlessLang:           "rust",
		StainlessPackageVersion: "0.39.0",
		StainlessOS:             stainlessOS(),
		StainlessArch:           stainlessArch(),
		StainlessRuntime:        "rustc",
		StainlessRuntimeVersion: "1.82.0",
	}
}

// DefaultKimiCLIFingerprint builds a Fingerprint that resembles the Kimi CLI
// (kimi-cli) client. Kimi For Coding limits use to its own CLI, so the relay
// must present a Kimi-CLI identity rather than a Claude Code one. The version
// strings are placeholders pending capture from real Kimi CLI traffic (roadmap
// P3 §5); they are overridable via the FingerprintSnapshot stored on the
// account so operators can update them without a code change.
func DefaultKimiCLIFingerprint() Fingerprint {
	return Fingerprint{
		ClientID:                randomClientID(),
		UserAgent:               "kimi-cli/0.1.0 (external, cli)",
		StainlessLang:           "js",
		StainlessPackageVersion: "0.1.0",
		StainlessOS:             stainlessOS(),
		StainlessArch:           stainlessArch(),
		StainlessRuntime:        "node",
		StainlessRuntimeVersion: "v22.11.0",
	}
}

// DefaultFingerprintForPlatform returns a fresh default fingerprint for the
// given platform.
// DefaultFingerprintForPlatform returns a fresh default fingerprint for the
// given platform. Zhipu and MiniMax officially support Claude Code clients, so
// they reuse the Claude Code fingerprint. Kimi is intended to carry its own
// Kimi CLI fingerprint in P3; until then it falls back to the Claude Code
// default so the function stays total.
func DefaultFingerprintForPlatform(p Platform) Fingerprint {
	switch p {
	case PlatformCodex:
		return DefaultCodexFingerprint()
	case PlatformZhipu, PlatformMinimax:
		// Officially-supported Claude Code clients.
		return DefaultClaudeCodeFingerprint()
	case PlatformKimi:
		return DefaultKimiCLIFingerprint()
	default:
		return DefaultClaudeCodeFingerprint()
	}
}

// RestoreFromSnapshot is the inverse of Snapshot: it materializes a Fingerprint
// from a cached snapshot. v2 snapshots restore the full fingerprint; legacy v1
// snapshots only carry the ClientID so the remaining fields fall back to the
// platform defaults. An empty snapshot yields a fresh default fingerprint.
func RestoreFromSnapshot(snap FingerprintSnapshot, p Platform) Fingerprint {
	if snap == "" {
		return DefaultFingerprintForPlatform(p)
	}
	const v2prefix = "v2:"
	if len(snap) > len(v2prefix) && string(snap[:len(v2prefix)]) == v2prefix {
		var fp Fingerprint
		if err := sonic.UnmarshalString(string(snap[len(v2prefix):]), &fp); err == nil && fp.ClientID != "" {
			return fp
		}
	}
	const v1prefix = "v1:"
	if len(snap) > len(v1prefix) && string(snap[:len(v1prefix)]) == v1prefix {
		fp := DefaultFingerprintForPlatform(p)
		fp.ClientID = string(snap[len(v1prefix):])
		return fp
	}
	return DefaultFingerprintForPlatform(p)
}

func randomClientID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// rand.Read failing is extraordinary; fall back to a deterministic-ish
		// value so we never return an empty ClientID.
		return "0000000000000000"
	}
	return hex.EncodeToString(b)
}

func stainlessOS() string {
	switch runtime.GOOS {
	case "darwin":
		return "Mac OS X"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return runtime.GOOS
	}
}

func stainlessArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}
