package main

import (
	kconfig "github.com/go-kratos/kratos/v3/config"

	adminconf "micro-one-api/app/admin/internal/conf"
	appregistry "micro-one-api/platform/registry"
	xconfig "micro-one-api/platform/config"
)

// Config wraps the proto-generated Bootstrap and provides convenience accessors.
// It embeds the proto message so config.yaml keys map directly to the protobuf
// json_name tags.
type Config struct {
	Bootstrap *adminconf.Bootstrap
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
	var bootstrap adminconf.Bootstrap
	if err := kratosCfg.Scan(&bootstrap); err != nil {
		return nil, err
	}

	// kratos Scan does not allocate nested proto message pointers;
	// explicitly initialize nil messages to avoid nil panics.
	initBootstrap(&bootstrap)

	return &Config{Bootstrap: &bootstrap}, nil
}

// initBootstrap ensures all nested message pointers are non-nil.
func initBootstrap(b *adminconf.Bootstrap) {
	if b.Server == nil {
		b.Server = &adminconf.Server{}
	}
	if b.Server.Http == nil {
		b.Server.Http = &adminconf.HTTP{}
	}
	if b.Server.Grpc == nil {
		b.Server.Grpc = &adminconf.GRPC{}
	}
	if b.Clients == nil {
		b.Clients = &adminconf.Clients{}
	}
	if b.Clients.Identity == nil {
		b.Clients.Identity = &adminconf.Identity{}
	}
	if b.Clients.Channel == nil {
		b.Clients.Channel = &adminconf.Channel{}
	}
	if b.Clients.Billing == nil {
		b.Clients.Billing = &adminconf.Billing{}
	}
	if b.Data == nil {
		b.Data = &adminconf.Data{}
	}
	if b.Data.Database == nil {
		b.Data.Database = &adminconf.Database{}
	}
	if b.Registry == nil {
		b.Registry = &adminconf.Registry{}
	}
	if b.Registry.Consul == nil {
		b.Registry.Consul = &adminconf.Consul{}
	}
	if b.Registry.Metadata == nil {
		b.Registry.Metadata = make(map[string]string)
	}
	// no return
}
