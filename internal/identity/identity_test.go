package identity

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestFingerprintSnapshotRoundTrip(t *testing.T) {
	fp := DefaultClaudeCodeFingerprint()
	snap := fp.Snapshot()
	restored := RestoreFromSnapshot(snap, PlatformClaude)
	if restored.ClientID != fp.ClientID {
		t.Fatalf("ClientID not restored: got %q want %q", restored.ClientID, fp.ClientID)
	}
	// Non-v1 snapshot falls back to default ClientID.
	blank := RestoreFromSnapshot("", PlatformClaude)
	if blank.ClientID == "" {
		t.Fatal("RestoreFromSnapshot('') should yield a default ClientID")
	}
}

func TestDefaultFingerprintForPlatform(t *testing.T) {
	for _, p := range []Platform{PlatformCodex, PlatformClaude} {
		fp := DefaultFingerprintForPlatform(p)
		if fp.ClientID == "" || fp.UserAgent == "" {
			t.Fatalf("platform %s: fingerprint incomplete: %+v", p, fp)
		}
	}
}

func TestIsClaudeCodeClient(t *testing.T) {
	cases := []struct {
		name   string
		header http.Header
		want   bool
	}{
		{"nil header", nil, false},
		{"empty header", http.Header{}, false},
		{"claude-cli UA", header("User-Agent", "claude-cli/1.0.128"), true},
		{"x-app cli", header("x-app", "cli"), true},
		{"anthropic-beta claude-code", header("anthropic-beta", "claude-code-2025"), true},
		{"unrelated", header("User-Agent", "python-requests/2.31"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsClaudeCodeClient(c.header); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestIsCodexCLIClient(t *testing.T) {
	if !IsCodexCLIClient(header("User-Agent", "codex_cli_rs/0.39.0")) {
		t.Fatal("expected codex_cli UA to be detected")
	}
	if !IsCodexCLIClient(header("originator", "codex_cli_rs")) {
		t.Fatal("expected originator header to be detected")
	}
	if IsCodexCLIClient(header("User-Agent", "curl/8.0")) {
		t.Fatal("curl should not be detected as codex client")
	}
}

func TestShouldMimic(t *testing.T) {
	// Non-mimickable accounts never mimic.
	if ShouldMimic(PlatformClaude, false, header("User-Agent", "python")) {
		t.Fatal("non-OAuth account should not mimic")
	}
	// OAuth + third-party client => mimic.
	if !ShouldMimic(PlatformClaude, true, header("User-Agent", "python")) {
		t.Fatal("OAuth + third-party client should mimic")
	}
	// OAuth + genuine CC client => no mimic.
	if ShouldMimic(PlatformClaude, true, header("User-Agent", "claude-cli/1.0")) {
		t.Fatal("OAuth + genuine CC client should not mimic")
	}
	// Codex platform.
	if !ShouldMimic(PlatformCodex, true, header("User-Agent", "python")) {
		t.Fatal("Codex OAuth + third-party should mimic")
	}
	if ShouldMimic(PlatformCodex, true, header("originator", "codex_cli_rs")) {
		t.Fatal("Codex OAuth + genuine codex client should not mimic")
	}
	// Zhipu / MiniMax reuse the Claude Code client detection (officially
	// support Claude Code), so a genuine CC client should not be mimicked.
	if ShouldMimic(PlatformZhipu, true, header("User-Agent", "claude-cli/1.0")) {
		t.Fatal("Zhipu OAuth + genuine CC client should not mimic")
	}
	if !ShouldMimic(PlatformZhipu, true, header("User-Agent", "python")) {
		t.Fatal("Zhipu OAuth + third-party client should mimic")
	}
	if ShouldMimic(PlatformMinimax, true, header("User-Agent", "claude-cli/1.0")) {
		t.Fatal("MiniMax OAuth + genuine CC client should not mimic")
	}
	// Kimi: only a genuine Kimi CLI client skips mimicry; everything else
	// (including a Claude Code UA, which is NOT a safe passthrough for Kimi)
	// is mimicked.
	if ShouldMimic(PlatformKimi, true, header("User-Agent", "kimi-cli/0.1.0")) {
		t.Fatal("Kimi OAuth + genuine Kimi CLI client should not mimic")
	}
	if !ShouldMimic(PlatformKimi, true, header("User-Agent", "claude-cli/1.0")) {
		t.Fatal("Kimi OAuth + Claude Code UA should still mimic (not a safe passthrough)")
	}
	if !ShouldMimic(PlatformKimi, true, header("User-Agent", "python")) {
		t.Fatal("Kimi OAuth + third-party client should mimic")
	}
}

func TestIsMimickableAccountType(t *testing.T) {
	for _, tc := range []struct {
		accountType string
		want        bool
	}{
		{"", false},
		{"api_key", false},
		{"oauth", true},
		{"static_key", true},
		{"setup_token", true},
		{"unknown", false},
	} {
		if got := IsMimickableAccountType(tc.accountType); got != tc.want {
			t.Errorf("IsMimickableAccountType(%q) = %v, want %v", tc.accountType, got, tc.want)
		}
	}
}

func TestRewriteMetadataUserID(t *testing.T) {
	in := []byte(`{"model":"claude-sonnet-4","messages":[]}`)
	out, err := RewriteMetadataUserID(in, "11111111-1111-1111-1111-111111111111", "abcd1234")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	meta, ok := raw["metadata"]
	if !ok {
		t.Fatal("metadata not set")
	}
	var m map[string]string
	if err := json.Unmarshal(meta, &m); err != nil {
		t.Fatal(err)
	}
	want := "0x11111111-1111-1111-1111-111111111111::abcd1234"
	if m["user_id"] != want {
		t.Fatalf("user_id = %q want %q", m["user_id"], want)
	}
	// Non-JSON passthrough.
	passthrough := []byte("not json")
	if out, _ := RewriteMetadataUserID(passthrough, "u", "c"); string(out) != "not json" {
		t.Fatal("non-JSON body should pass through unchanged")
	}
}

func TestInjectClaudeCodeSystemPrompt(t *testing.T) {
	// No system -> create.
	out, err := InjectClaudeCodeSystemPrompt([]byte(`{"model":"x","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Claude Code") {
		t.Fatalf("expected injected system prompt, got %s", out)
	}
	// Existing string system -> prepend.
	out, err = InjectClaudeCodeSystemPrompt([]byte(`{"system":"You are helpful","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Claude Code") || !strings.Contains(string(out), "You are helpful") {
		t.Fatalf("expected merged system prompt, got %s", out)
	}
}

func TestNormalizeClaudeOAuthRequestBody(t *testing.T) {
	out, err := NormalizeClaudeOAuthRequestBody([]byte(`{"model":"x","messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"\"temperature\"", "\"max_tokens\"", "\"tools\""} {
		if !strings.Contains(string(out), key) {
			t.Fatalf("expected %s in normalized body, got %s", key, out)
		}
	}
	// Already-complete body is unchanged.
	complete := []byte(`{"model":"x","temperature":0.5,"max_tokens":1024,"tools":[]}`)
	if out, _ := NormalizeClaudeOAuthRequestBody(complete); string(out) != string(complete) {
		t.Fatal("complete body should be unchanged")
	}
}

func TestComputeAnthropicBeta(t *testing.T) {
	got := ComputeAnthropicBeta("prompt-caching-2024-07-31")
	if !strings.Contains(got, "prompt-caching-2024-07-31") {
		t.Fatalf("inbound beta should be preserved, got %s", got)
	}
	if !strings.Contains(got, "context-management-2025-06-10") {
		t.Fatalf("default beta should be merged, got %s", got)
	}
	// Duplicates removed.
	got2 := ComputeAnthropicBeta("context-management-2025-06-10,context-management-2025-06-10")
	if strings.Count(got2, "context-management-2025-06-10") != 1 {
		t.Fatalf("expected dedup, got %s", got2)
	}
}

func TestIdentityServiceGetOrCreate(t *testing.T) {
	svc := NewIdentityService(0)
	key := AccountKey{ID: 1, Platform: PlatformClaude, IsOAuth: true}
	fp1, err := svc.GetOrCreateFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	// Second call returns the same (cached) fingerprint.
	fp2, err := svc.GetOrCreateFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	if fp1.ClientID != fp2.ClientID {
		t.Fatal("expected stable cached fingerprint")
	}
	svc.Invalidate(1)
	fp3, err := svc.GetOrCreateFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	if fp3.ClientID == fp1.ClientID {
		t.Fatal("expected new fingerprint after invalidate")
	}
}

func header(key, value string) http.Header {
	h := http.Header{}
	h.Set(key, value)
	return h
}
