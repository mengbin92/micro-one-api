package conf

import "maps"

import appregistry "micro-one-api/platform/registry"

// ToRegistryConfig converts the proto Registry to the platform registry Config.
func (r *Registry) ToRegistryConfig() appregistry.Config {
	cfg := appregistry.Config{
		Type: r.Type,
	}

	if r.Consul != nil {
		metadata := make(map[string]string)
		maps.Copy(metadata, r.Metadata)

		cfg.Consul = appregistry.ConsulConfig{
			Address:             r.Consul.Address,
			HealthCheckInterval: int(r.Consul.HealthCheckInterval),
			Metadata:            metadata,
		}
	}

	return cfg
}
