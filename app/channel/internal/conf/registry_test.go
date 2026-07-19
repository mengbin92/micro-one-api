package conf

import (
	appregistry "micro-one-api/platform/registry"
	"testing"
)

func TestRegistryToRegistryConfig(t *testing.T) {
	tests := []struct {
		name     string
		registry *Registry
		want     appregistry.Config
	}{
		{
			name:     "nil receiver returns empty Config",
			registry: nil,
			want:     appregistry.Config{},
		},
		{
			name:     "nil Consul returns Config with Type only",
			registry: &Registry{Type: "consul"},
			want: appregistry.Config{
				Type: "consul",
			},
		},
		{
			name: "full registry maps all fields correctly",
			registry: &Registry{
				Type: "consul",
				Consul: &Consul{
					Address:             "127.0.0.1:8500",
					HealthCheckInterval: 10,
					HealthCheckPath:     "/healthz",
					HealthCheckTimeout:  "5s",
					DeregisterAfter:     "30m",
					Metadata: map[string]string{
						"version": "1.0.0",
						"env":     "prod",
					},
				},
			},
			want: appregistry.Config{
				Type: "consul",
				Consul: appregistry.ConsulConfig{
					Address:             "127.0.0.1:8500",
					HealthCheckInterval: 10,
					HealthCheckPath:     "/healthz",
					HealthCheckTimeout:  "5s",
					DeregisterAfter:     "30m",
					Metadata: map[string]string{
						"version": "1.0.0",
						"env":     "prod",
					},
				},
			},
		},
		{
			name: "metadata map is not shared (copy)",
			registry: &Registry{
				Type: "consul",
				Consul: &Consul{
					Address: "127.0.0.1:8500",
					Metadata: map[string]string{
						"key": "value",
					},
				},
			},
			want: appregistry.Config{
				Type: "consul",
				Consul: appregistry.ConsulConfig{
					Address: "127.0.0.1:8500",
					Metadata: map[string]string{
						"key": "value",
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.registry.ToRegistryConfig()

			if got.Type != tt.want.Type {
				t.Errorf("ToRegistryConfig().Type = %v, want %v", got.Type, tt.want.Type)
			}
			if got.Consul.Address != tt.want.Consul.Address {
				t.Errorf("ToRegistryConfig().Consul.Address = %v, want %v", got.Consul.Address, tt.want.Consul.Address)
			}
			if got.Consul.HealthCheckInterval != tt.want.Consul.HealthCheckInterval {
				t.Errorf("ToRegistryConfig().Consul.HealthCheckInterval = %v, want %v", got.Consul.HealthCheckInterval, tt.want.Consul.HealthCheckInterval)
			}
			if got.Consul.HealthCheckPath != tt.want.Consul.HealthCheckPath {
				t.Errorf("ToRegistryConfig().Consul.HealthCheckPath = %v, want %v", got.Consul.HealthCheckPath, tt.want.Consul.HealthCheckPath)
			}
			if got.Consul.HealthCheckTimeout != tt.want.Consul.HealthCheckTimeout {
				t.Errorf("ToRegistryConfig().Consul.HealthCheckTimeout = %v, want %v", got.Consul.HealthCheckTimeout, tt.want.Consul.HealthCheckTimeout)
			}
			if got.Consul.DeregisterAfter != tt.want.Consul.DeregisterAfter {
				t.Errorf("ToRegistryConfig().Consul.DeregisterAfter = %v, want %v", got.Consul.DeregisterAfter, tt.want.Consul.DeregisterAfter)
			}

			// Verify metadata is not shared (modify result, check original)
			if tt.registry != nil && tt.registry.Consul != nil && tt.registry.Consul.Metadata != nil {
				originalLen := len(tt.registry.Consul.Metadata)
				if got.Consul.Metadata != nil {
					got.Consul.Metadata["new"] = "injected"
					if len(tt.registry.Consul.Metadata) != originalLen {
						t.Errorf("ToRegistryConfig() shared metadata map instead of copying; original has %d items, now %d", originalLen, len(tt.registry.Consul.Metadata))
					}
				}
			}
		})
	}
}
