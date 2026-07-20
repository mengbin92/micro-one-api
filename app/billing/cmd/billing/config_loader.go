package main

import (
	kconfig "github.com/go-kratos/kratos/v3/config"

	billingconf "micro-one-api/app/billing/internal/conf"
	appregistry "micro-one-api/platform/registry"
	xconfig "micro-one-api/platform/config"
)

// Config wraps the proto-generated Bootstrap and provides convenience accessors.
// It embeds the proto message so config.yaml keys map directly to the protobuf
// json_name tags.
type Config struct {
	Bootstrap *billingconf.Bootstrap
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
	var bootstrap billingconf.Bootstrap
	if err := kratosCfg.Scan(&bootstrap); err != nil {
		return nil, err
	}

	// kratos Scan does not allocate nested proto message pointers;
	// explicitly initialize nil messages to avoid nil panics.
	initBootstrap(&bootstrap)

	return &Config{Bootstrap: &bootstrap}, nil
}

// initBootstrap ensures all nested message pointers are non-nil.
func initBootstrap(b *billingconf.Bootstrap) {
	if b.Server == nil {
		b.Server = &billingconf.Server{}
	}
	if b.Server.Http == nil {
		b.Server.Http = &billingconf.HTTP{}
	}
	if b.Server.Grpc == nil {
		b.Server.Grpc = &billingconf.GRPC{}
	}
	if b.Data == nil {
		b.Data = &billingconf.Data{}
	}
	if b.Data.Database == nil {
		b.Data.Database = &billingconf.Database{}
	}
	if b.Data.Redis == nil {
		b.Data.Redis = &billingconf.Redis{}
	}
	if b.Billing == nil {
		b.Billing = &billingconf.Billing{}
	}
	if b.Billing.Async == nil {
		b.Billing.Async = &billingconf.AsyncBilling{}
	}
	if b.Billing.GroupRatios == nil {
		b.Billing.GroupRatios = make(map[string]float64)
	}
	if b.Billing.ModelRatios == nil {
		b.Billing.ModelRatios = make(map[string]float64)
	}
	if b.Billing.CompletionRatios == nil {
		b.Billing.CompletionRatios = make(map[string]float64)
	}
	if b.Payment == nil {
		b.Payment = &billingconf.Payment{}
	}
	if b.Payment.Alipay == nil {
		b.Payment.Alipay = &billingconf.Alipay{}
	}
	if b.Clients == nil {
		b.Clients = &billingconf.Clients{}
	}
	if b.Clients.Notify == nil {
		b.Clients.Notify = &billingconf.Notify{}
	}
	if b.Recon == nil {
		b.Recon = &billingconf.Recon{}
	}
	if b.Recon.Recipients == nil {
		b.Recon.Recipients = []string{}
	}
	if b.Partition == nil {
		b.Partition = &billingconf.Partition{}
	}
	if b.Partition.Tables == nil {
		b.Partition.Tables = []string{}
	}
	if b.Registry == nil {
		b.Registry = &billingconf.Registry{}
	}
	if b.Registry.Consul == nil {
		b.Registry.Consul = &billingconf.Consul{}
	}
	if b.Registry.Metadata == nil {
		b.Registry.Metadata = make(map[string]string)
	}
	// no return
}
