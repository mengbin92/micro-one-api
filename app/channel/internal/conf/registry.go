package conf

import appregistry "micro-one-api/platform/registry"

// ToRegistryConfig converts the proto Registry message to appregistry.Config.
// This bridges the gap between the proto-generated config and the platform registry package.
func (r *Registry) ToRegistryConfig() appregistry.Config {
	if r == nil {
		return appregistry.Config{}
	}

	cfg := appregistry.Config{
		Type: r.Type,
	}

	if r.Consul != nil {
		cfg.Consul = appregistry.ConsulConfig{
			Address:             r.Consul.Address,
			HealthCheckInterval: int(r.Consul.HealthCheckInterval),
			HealthCheckPath:     r.Consul.HealthCheckPath,
			HealthCheckTimeout:  r.Consul.HealthCheckTimeout,
			DeregisterAfter:     r.Consul.DeregisterAfter,
			Metadata:            copyMetadataMap(r.Consul.Metadata),
		}
	}

	return cfg
}

// copyMetadataMap creates a shallow copy of the metadata map to prevent
// shared state between the proto message and the converted config.
func copyMetadataMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
