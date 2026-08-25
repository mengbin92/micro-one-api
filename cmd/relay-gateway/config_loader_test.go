package main

import (
	"os"
	"path/filepath"
	"strings"
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
		configuredDigest := strings.Repeat("a", 64)
		t.Setenv("MOA_RELAY_TOKEN_SHA256", configuredDigest)
		cfg, err := loadConfig(configPath)
		if err != nil {
			t.Fatalf("loadConfig() error = %v", err)
		}
		got := cfg.Bootstrap.RelayOrchestrator.GetAllowlistTokenSha256()
		if len(got) != 1 || got[0] != configuredDigest {
			t.Fatalf("allowlist = %#v, want configured digest", got)
		}
	})
}

func TestLoadConfigRelayAddressesFromEnv(t *testing.T) {
	configPath := filepath.Join("..", "..", "configs", "config.yaml")
	t.Setenv("RELAY_HTTP_ADDR", "127.0.0.1:18080")
	t.Setenv("RELAY_GRPC_ADDR", "127.0.0.1:19003")
	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}
	if got := cfg.Bootstrap.Server.Http.GetAddr(); got != "127.0.0.1:18080" {
		t.Fatalf("HTTP addr = %q, want 127.0.0.1:18080", got)
	}
	if got := cfg.Bootstrap.Server.Grpc.GetAddr(); got != "127.0.0.1:19003" {
		t.Fatalf("gRPC addr = %q, want 127.0.0.1:19003", got)
	}
}
