package main

import (
	kconfig "github.com/go-kratos/kratos/v3/config"

	logconf "micro-one-api/app/log/internal/conf"
	appregistry "micro-one-api/platform/registry"
	xconfig "micro-one-api/platform/config"
)

// Config wraps the proto-generated Bootstrap and provides convenience accessors.
// It embeds the proto message so config.yaml keys map directly to the protobuf
// json_name tags.
type Config struct {
	Bootstrap *logconf.Bootstrap
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
	var bootstrap logconf.Bootstrap
	if err := kratosCfg.Scan(&bootstrap); err != nil {
		return nil, err
	}

	// kratos Scan does not allocate nested proto message pointers;
	// explicitly initialize nil messages to avoid nil panics.
	initBootstrap(&bootstrap)

	return &Config{Bootstrap: &bootstrap}, nil
}

// initBootstrap ensures all nested message pointers are non-nil.
func initBootstrap(b *logconf.Bootstrap) {
	if b.Server == nil {
		b.Server = &logconf.Server{}
	}
	if b.Server.Http == nil {
		b.Server.Http = &logconf.HTTP{}
	}
	if b.Server.Grpc == nil {
		b.Server.Grpc = &logconf.GRPC{}
	}
	if b.Data == nil {
		b.Data = &logconf.Data{}
	}
	if b.Data.Database == nil {
		b.Data.Database = &logconf.Database{}
	}
	if b.Data.Redis == nil {
		b.Data.Redis = &logconf.Redis{}
	}
	if b.Registry == nil {
		b.Registry = &logconf.Registry{}
	}
	if b.Registry.Consul == nil {
		b.Registry.Consul = &logconf.Consul{}
	}
	if b.Registry.Metadata == nil {
		b.Registry.Metadata = make(map[string]string)
	}
	if b.LogSvc == nil {
		b.LogSvc = &logconf.LogSvc{}
	}
	// BatchFlushInterval uses google.protobuf.Duration, which defaults to nil.
	// No initialization needed here; the biz layer handles nil-to-zero conversion.
	if b.Partition == nil {
		b.Partition = &logconf.Partition{}
	}
	// no return
}
