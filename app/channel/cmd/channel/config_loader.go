package main

import (
	kconfig "github.com/go-kratos/kratos/v3/config"

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
	initBootstrap(&bootstrap)

	return &Config{Bootstrap: &bootstrap}, nil
}

// initBootstrap ensures all nested message pointers are non-nil.
// It modifies the Bootstrap in-place to avoid copying proto messages
// (which contain sync.Mutex and cannot be copied by value).
func initBootstrap(b *channelcfg.Bootstrap) {
	if b.Server == nil {
		b.Server = &channelcfg.Server{}
	}
	if b.Server.Http == nil {
		b.Server.Http = &channelcfg.HTTP{}
	}
	if b.Server.Grpc == nil {
		b.Server.Grpc = &channelcfg.GRPC{}
	}
	if b.Data == nil {
		b.Data = &channelcfg.Data{}
	}
	if b.Data.Database == nil {
		b.Data.Database = &channelcfg.Database{}
	}
	if b.Data.Redis == nil {
		b.Data.Redis = &channelcfg.Redis{}
	}
	if b.Registry == nil {
		b.Registry = &channelcfg.Registry{}
	}
	if b.Registry.Consul == nil {
		b.Registry.Consul = &channelcfg.Consul{}
	}
}
