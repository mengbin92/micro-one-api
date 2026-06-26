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
)

// Platform identifies a subscription-account platform.
type Platform string

const (
	// PlatformCodex is the ChatGPT / Codex (Responses API) subscription.
	PlatformCodex Platform = "codex"
	// PlatformClaude is the Claude Code (Anthropic Messages API) subscription.
	PlatformClaude Platform = "claude"
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
	StainlessLang          string `json:"stainless_lang"`
	StainlessPackageVersion string `json:"stainless_package_version"`
	StainlessOS            string `json:"stainless_os"`
	StainlessArch          string `json:"stainless_arch"`
	StainlessRuntime       string `json:"stainless_runtime"`
	StainlessRuntimeVersion string `json:"stainless_runtime_version"`
}

// FingerprintSnapshot is an opaque, serializable representation of a
// Fingerprint cached alongside an account (e.g. in the SubscriptionAccount
// record or Redis). The relay treats it as a blob; only this package
// interprets it.
type FingerprintSnapshot string

// Snapshot serializes the fingerprint into an opaque cache token. The MVP
// uses a compact hex encoding of the ClientID + a version tag; a full
// implementation would encrypt/sign this. It is stable for a given ClientID.
func (f Fingerprint) Snapshot() FingerprintSnapshot {
	return FingerprintSnapshot(fmt.Sprintf("v1:%s", f.ClientID))
}

// DefaultClaudeCodeFingerprint builds a Fingerprint that resembles a recent
// official Claude Code CLI client. The version strings track the published
// @anthropic-ai/sdk defaults.
func DefaultClaudeCodeFingerprint() Fingerprint {
	return Fingerprint{
		ClientID:               randomClientID(),
		UserAgent:              "claude-cli/1.0.128 (external, cli)",
		StainlessLang:          "js",
		StainlessPackageVersion: "0.52.0",
		StainlessOS:            stainlessOS(),
		StainlessArch:          stainlessArch(),
		StainlessRuntime:       "node",
		StainlessRuntimeVersion: "v22.11.0",
	}
}

// DefaultCodexFingerprint builds a Fingerprint that resembles the official
// codex_cli_rs client.
func DefaultCodexFingerprint() Fingerprint {
	return Fingerprint{
		ClientID:               randomClientID(),
		UserAgent:              fmt.Sprintf("codex_cli_rs/0.39.0 (%s; %s) zsh", stainlessOS(), stainlessArch()),
		StainlessLang:          "rust",
		StainlessPackageVersion: "0.39.0",
		StainlessOS:            stainlessOS(),
		StainlessArch:          stainlessArch(),
		StainlessRuntime:       "rustc",
		StainlessRuntimeVersion: "1.82.0",
	}
}

// DefaultFingerprintForPlatform returns a fresh default fingerprint for the
// given platform.
func DefaultFingerprintForPlatform(p Platform) Fingerprint {
	switch p {
	case PlatformCodex:
		return DefaultCodexFingerprint()
	default:
		return DefaultClaudeCodeFingerprint()
	}
}

// RestoreFromSnapshot is a best-effort inverse of Snapshot: it materializes a
// Fingerprint from a cached snapshot. Fields that are not encoded in the
// snapshot (everything except ClientID) are filled with the platform defaults.
func RestoreFromSnapshot(snap FingerprintSnapshot, p Platform) Fingerprint {
	fp := DefaultFingerprintForPlatform(p)
	if snap == "" {
		return fp
	}
	const prefix = "v1:"
	if len(snap) > len(prefix) && string(snap[:len(prefix)]) == prefix {
		fp.ClientID = string(snap[len(prefix):])
	}
	return fp
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
