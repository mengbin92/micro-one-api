package conf

import (
	"testing"
)

// TestToRegistryConfig covers the proto → platform Config conversion for the
// Registry/Consul messages. This package's Consul only carries Address and
// HealthCheckInterval, so the conversion must map those two and copy metadata
// defensively.
func TestToRegistryConfig(t *testing.T) {
	t.Run("nil Consul returns Config with Type only", func(t *testing.T) {
		got := (&Registry{Type: "consul"}).ToRegistryConfig()
		if got.Type != "consul" {
			t.Fatalf("Type = %q, want consul", got.Type)
		}
		if got.Consul.Address != "" || got.Consul.HealthCheckInterval != 0 {
			t.Fatalf("Consul = %+v, want empty", got.Consul)
		}
	})

	t.Run("full registry maps address and interval", func(t *testing.T) {
		got := (&Registry{
			Type: "consul",
			Consul: &Consul{
				Address:             "127.0.0.1:8500",
				HealthCheckInterval: 10,
			},
		}).ToRegistryConfig()
		if got.Consul.Address != "127.0.0.1:8500" {
			t.Errorf("Address = %q, want 127.0.0.1:8500", got.Consul.Address)
		}
		if got.Consul.HealthCheckInterval != 10 {
			t.Errorf("HealthCheckInterval = %d, want 10", got.Consul.HealthCheckInterval)
		}
	})

	t.Run("metadata map is copied not shared", func(t *testing.T) {
		src := &Registry{
			Type: "consul",
			Consul: &Consul{
				Address: "127.0.0.1:8500",
			},
			Metadata: map[string]string{"env": "prod"},
		}
		got := src.ToRegistryConfig()
		if got.Consul.Metadata["env"] != "prod" {
			t.Fatalf("metadata not copied: %+v", got.Consul.Metadata)
		}
		// Mutate the result; the source must be unaffected.
		got.Consul.Metadata["env"] = "mutated"
		if src.Metadata["env"] != "prod" {
			t.Fatal("metadata map shared between source and result instead of copied")
		}
	})
}
