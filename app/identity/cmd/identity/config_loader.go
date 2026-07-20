package main

import (
	kconfig "github.com/go-kratos/kratos/v3/config"

	identityconf "micro-one-api/app/identity/internal/conf"
	appregistry "micro-one-api/platform/registry"
	xconfig "micro-one-api/platform/config"
)

// Config wraps the proto-generated Bootstrap and provides convenience accessors.
// It embeds the proto message so config.yaml keys map directly to the protobuf
// json_name tags.
type Config struct {
	Bootstrap *identityconf.Bootstrap
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
	var bootstrap identityconf.Bootstrap
	if err := kratosCfg.Scan(&bootstrap); err != nil {
		return nil, err
	}

	// kratos Scan does not allocate nested proto message pointers;
	// explicitly initialize nil messages to avoid nil panics.
	initBootstrap(&bootstrap)

	return &Config{Bootstrap: &bootstrap}, nil
}

// initBootstrap ensures all nested message pointers are non-nil.
func initBootstrap(b *identityconf.Bootstrap) {
	if b.Server == nil {
		b.Server = &identityconf.Server{}
	}
	if b.Server.Http == nil {
		b.Server.Http = &identityconf.HTTP{}
	}
	if b.Server.Grpc == nil {
		b.Server.Grpc = &identityconf.GRPC{}
	}
	if b.Data == nil {
		b.Data = &identityconf.Data{}
	}
	if b.Data.Database == nil {
		b.Data.Database = &identityconf.Database{}
	}
	if b.Data.Redis == nil {
		b.Data.Redis = &identityconf.Redis{}
	}
	if b.Clients == nil {
		b.Clients = &identityconf.Clients{}
	}
	if b.Clients.Billing == nil {
		b.Clients.Billing = &identityconf.Billing{}
	}
	if b.Oauth == nil {
		b.Oauth = &identityconf.OAuth{}
	}
	if b.Oauth.Github == nil {
		b.Oauth.Github = &identityconf.OAuthProvider{}
	}
	if b.Oauth.Google == nil {
		b.Oauth.Google = &identityconf.OAuthProvider{}
	}
	if b.Oauth.Oidc == nil {
		b.Oauth.Oidc = &identityconf.OIDCProvider{}
	}
	if b.Oauth.Lark == nil {
		b.Oauth.Lark = &identityconf.OAuthProvider{}
	}
	if b.Oauth.Wechat == nil {
		b.Oauth.Wechat = &identityconf.OAuthProvider{}
	}
	if b.Registration == nil {
		b.Registration = &identityconf.Registration{}
	}
	if b.Registry == nil {
		b.Registry = &identityconf.Registry{}
	}
	if b.Registry.Consul == nil {
		b.Registry.Consul = &identityconf.Consul{}
	}
	if b.Registry.Metadata == nil {
		b.Registry.Metadata = make(map[string]string)
	}
	// no return
}
