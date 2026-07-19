package main

import (
	kconfig "github.com/go-kratos/kratos/v2/config"

	channelcfg "micro-one-api/app/channel/internal/conf"
	xconfig "micro-one-api/platform/config"
	appregistry "micro-one-api/platform/registry"
)

// Config wraps the proto-generated Bootstrap with convenience methods.
// This maintains backward compatibility with existing wire.go code that
// expects cfg.Server, cfg.Data, cfg.Registry access patterns.
type Config struct {
	*channelcfg.Bootstrap
}

// Registry returns the converted appregistry.Config for platform compatibility.
func (c *Config) Registry() appregistry.Config {
	if c.Bootstrap == nil || c.Bootstrap.Registry == nil {
		return appregistry.Config{}
	}
	return c.Bootstrap.Registry.ToRegistryConfig()
}

// loadConfig reads and parses the service configuration file.
// It is declared here (not in wire_gen.go) so it is visible under both
// the wireinject and default build tags.
func loadConfig(confPath string) (*Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var bootstrap channelcfg.Bootstrap
	if err := kratosCfg.Scan(&bootstrap); err != nil {
		return nil, err
	}

	// Initialize nil nested messages that are required by wire functions
	// Kratos config.Scan doesn't allocate nested proto messages even when
	// the YAML has the corresponding fields.
	if bootstrap.Server == nil {
		bootstrap.Server = &channelcfg.Server{}
	}
	if bootstrap.Server.Http == nil {
		bootstrap.Server.Http = &channelcfg.HTTP{}
	}
	if bootstrap.Server.Grpc == nil {
		bootstrap.Server.Grpc = &channelcfg.GRPC{}
	}
	if bootstrap.Data == nil {
		bootstrap.Data = &channelcfg.Data{}
	}
	if bootstrap.Data.Database == nil {
		bootstrap.Data.Database = &channelcfg.Database{}
	}
	if bootstrap.Data.Redis == nil {
		bootstrap.Data.Redis = &channelcfg.Redis{}
	}
	if bootstrap.Registry == nil {
		bootstrap.Registry = &channelcfg.Registry{}
	}
	if bootstrap.Registry.Consul == nil {
		bootstrap.Registry.Consul = &channelcfg.Consul{}
	}

	return &Config{Bootstrap: &bootstrap}, nil
}
