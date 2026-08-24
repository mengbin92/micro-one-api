package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigRelayOrchestratorAllowlistEmptyAndConfigured(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := "relay_orchestrator:\n  enabled: false\n  allowlist_token_sha256: [${MOA_RELAY_TOKEN_SHA256:-}]\n"
	if err := os.WriteFile(configPath, []byte(configYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("empty", func(t *testing.T) {
		t.Setenv("MOA_RELAY_TOKEN_SHA256", "")
		cfg, err := loadConfig(configPath)
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		if got := cfg.Bootstrap.RelayOrchestrator.GetAllowlistTokenSha256(); len(got) != 0 {
			t.Fatalf("allowlist = %#v, want empty", got)
		}
	})

	t.Run("configured", func(t *testing.T) {
		t.Setenv("MOA_RELAY_TOKEN_SHA256", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
		cfg, err := loadConfig(configPath)
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		got := cfg.Bootstrap.RelayOrchestrator.GetAllowlistTokenSha256()
		if len(got) != 1 || got[0] != "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
			t.Fatalf("allowlist = %#v, want configured digest", got)
		}
	})
}
