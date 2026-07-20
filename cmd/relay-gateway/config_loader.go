package main

import (
	kconfig "github.com/go-kratos/kratos/v3/config"

	relayconf "micro-one-api/internal/conf"
	appregistry "micro-one-api/platform/registry"
	xconfig "micro-one-api/platform/config"
)

// Config wraps the proto-generated Bootstrap and provides convenience accessors.
// It embeds the proto message so config.yaml keys map directly to the protobuf
// json_name tags.
type Config struct {
	Bootstrap *relayconf.Bootstrap
}

// Registry returns the converted registry configuration.
func (c *Config) Registry() appregistry.Config {
	if c.Bootstrap == nil || c.Bootstrap.Registry == nil {
		return appregistry.Config{}
	}
	return c.Bootstrap.Registry.ToRegistryConfig()
}

// loadConfig reads and parses the relay-gateway configuration file.
// It is declared here (not in wire_gen.go) so it is visible under both
// the wireinject and default build tags.
func loadConfig(confPath string) (*Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var bootstrap relayconf.Bootstrap
	if err := kratosCfg.Scan(&bootstrap); err != nil {
		return nil, err
	}

	// kratos Scan does not allocate nested proto message pointers;
	// explicitly initialize nil messages to avoid nil panics.
	initBootstrap(&bootstrap)

	return &Config{Bootstrap: &bootstrap}, nil
}

// initBootstrap ensures all nested message pointers are non-nil.
// It modifies the Bootstrap in-place to avoid copying proto messages
// (which contain sync.Mutex and cannot be copied by value).
func initBootstrap(b *relayconf.Bootstrap) {
	if b.Server == nil {
		b.Server = &relayconf.Server{}
	}
	if b.Server.Http == nil {
		b.Server.Http = &relayconf.HTTP{}
	}
	if b.Server.Grpc == nil {
		b.Server.Grpc = &relayconf.GRPC{}
	}
	if b.Clients == nil {
		b.Clients = &relayconf.Clients{}
	}
	if b.Clients.Identity == nil {
		b.Clients.Identity = &relayconf.Identity{}
	}
	if b.Clients.Channel == nil {
		b.Clients.Channel = &relayconf.Channel{}
	}
	if b.Clients.Billing == nil {
		b.Clients.Billing = &relayconf.Billing{}
	}
	if b.Clients.Log == nil {
		b.Clients.Log = &relayconf.Log{}
	}
	if b.Retry == nil {
		b.Retry = &relayconf.Retry{}
	}
	if b.Models == nil {
		b.Models = &relayconf.Models{}
	}
	if b.Redis == nil {
		b.Redis = &relayconf.Redis{}
	}
	if b.Registry == nil {
		b.Registry = &relayconf.Registry{}
	}
	if b.Registry.Consul == nil {
		b.Registry.Consul = &relayconf.Consul{}
	}
	if b.Registry.Metadata == nil {
		b.Registry.Metadata = make(map[string]string)
	}
	if b.OpenaiWs == nil {
		b.OpenaiWs = &relayconf.OpenaiWS{}
	}
	if b.HybridAdaptor == nil {
		b.HybridAdaptor = &relayconf.HybridAdaptor{}
	}
	if b.HybridAdaptor.TokenRefresh == nil {
		b.HybridAdaptor.TokenRefresh = &relayconf.TokenRefresh{}
	}
	if b.HybridAdaptor.RuntimeBlock == nil {
		b.HybridAdaptor.RuntimeBlock = &relayconf.RuntimeBlock{}
	}
	if b.SessionSticky == nil {
		b.SessionSticky = &relayconf.SessionSticky{}
	}
	if b.Subscription == nil {
		b.Subscription = &relayconf.Subscription{}
	}
	if b.RelayOrchestrator == nil {
		b.RelayOrchestrator = &relayconf.RelayOrchestrator{}
	}
	if b.ChannelCache == nil {
		b.ChannelCache = &relayconf.ChannelCache{}
	}
	if b.Idempotency == nil {
		b.Idempotency = &relayconf.Idempotency{}
	}
	if b.Audit == nil {
		b.Audit = &relayconf.Audit{}
	}
	if b.Resilience == nil {
		b.Resilience = &relayconf.Resilience{}
	}
	if b.Mtls == nil {
		b.Mtls = &relayconf.Mtls{}
	}
}
