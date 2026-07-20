package main

import (
	kconfig "github.com/go-kratos/kratos/v3/config"

	notifyconf "micro-one-api/app/notify/internal/conf"
	appregistry "micro-one-api/platform/registry"
	xconfig "micro-one-api/platform/config"
)

// Config wraps the proto-generated Bootstrap and provides convenience accessors.
// It embeds the proto message so config.yaml keys map directly to the protobuf
// json_name tags.
type Config struct {
	Bootstrap *notifyconf.Bootstrap
}

// Registry returns the converted registry configuration.
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
	var bootstrap notifyconf.Bootstrap
	if err := kratosCfg.Scan(&bootstrap); err != nil {
		return nil, err
	}

	// kratos Scan does not allocate nested proto message pointers;
	// explicitly initialize nil messages to avoid nil panics.
	initBootstrap(&bootstrap)

	return &Config{Bootstrap: &bootstrap}, nil
}

// initBootstrap ensures all nested message pointers are non-nil.
func initBootstrap(b *notifyconf.Bootstrap) {
	if b.Server == nil {
		b.Server = &notifyconf.Server{}
	}
	if b.Server.Http == nil {
		b.Server.Http = &notifyconf.HTTP{}
	}
	if b.Server.Grpc == nil {
		b.Server.Grpc = &notifyconf.GRPC{}
	}
	if b.Data == nil {
		b.Data = &notifyconf.Data{}
	}
	if b.Data.Database == nil {
		b.Data.Database = &notifyconf.Database{}
	}
	if b.Data.Redis == nil {
		b.Data.Redis = &notifyconf.Redis{}
	}
	if b.Registry == nil {
		b.Registry = &notifyconf.Registry{}
	}
	if b.Registry.Consul == nil {
		b.Registry.Consul = &notifyconf.Consul{}
	}
	if b.Registry.Metadata == nil {
		b.Registry.Metadata = make(map[string]string)
	}
	if b.NotifySvc == nil {
		b.NotifySvc = &notifyconf.Notify{}
	}
	// DispatchInterval uses google.protobuf.Duration, which defaults to nil.
	// No initialization needed here; the biz layer handles nil-to-zero conversion.
	// no return
}
