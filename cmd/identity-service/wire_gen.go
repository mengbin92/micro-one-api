//go:build !wireinject

package main

import (
	"fmt"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"

	"micro-one-api/internal/identity/biz"
	identitycfg "micro-one-api/internal/identity/config"
	"micro-one-api/internal/identity/data"
	"micro-one-api/internal/identity/server"
	"micro-one-api/internal/identity/service"
	"micro-one-api/internal/pkg/oauth"
	appregistry "micro-one-api/internal/pkg/registry"
	"micro-one-api/internal/pkg/xconfig"
)

func loadConfig(confPath string) (*identitycfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg identitycfg.Config
	if err := kratosCfg.Scan(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// InitApp loads config and builds the Kratos application.
func InitApp(confPath string) (*kratos.App, func(), error) {
	cfg, err := loadConfig(confPath)
	if err != nil {
		return nil, nil, err
	}

	repo, err := data.NewRepositoryFromEnv(cfg.Data.Database.Source)
	if err != nil {
		return nil, nil, err
	}

	uc := biz.NewIdentityUsecase(repo)
	svc := service.NewIdentityService(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)

	// Setup OAuth providers
	oauthRegistry := setupOAuth(cfg)
	httpSrv := server.NewHTTPServerWithRegistrationPolicy(cfg.Server.HTTP.Addr, uc, oauthRegistry, registrationPolicyFromConfig(cfg))

	// Setup service registration
	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		fmt.Printf("Warning: Failed to create registrar: %v\n", rErr)
	}

	kratosOpts := []kratos.Option{
		kratos.Name("identity-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

	return app, func() {}, nil
}

func registrationPolicyFromConfig(cfg *identitycfg.Config) server.RegistrationPolicy {
	enabled := !cfg.Registration.Disabled
	if cfg.Registration.Enabled {
		enabled = true
	}
	return server.RegistrationPolicy{
		Enabled:                       enabled,
		EmailDomainRestrictionEnabled: cfg.Registration.EmailDomainRestrictionEnabled,
		EmailDomainWhitelist:          cfg.Registration.EmailDomainWhitelist,
		TurnstileCheckEnabled:         cfg.Registration.TurnstileCheckEnabled,
		TurnstileSecret:               cfg.Registration.TurnstileSecret,
	}
}

func setupOAuth(cfg *identitycfg.Config) *oauth.ProviderRegistry {
	registry := oauth.NewProviderRegistry()
	baseURL := cfg.OAuth.BaseURL
	if baseURL == "" {
		baseURL = "http://localhost:8001"
	}

	if cfg.OAuth.GitHub.Enabled && cfg.OAuth.GitHub.ClientID != "" {
		registry.Register(oauth.NewGitHubProvider(oauth.Config{
			ClientID:     cfg.OAuth.GitHub.ClientID,
			ClientSecret: cfg.OAuth.GitHub.ClientSecret,
			RedirectURL:  baseURL + "/v1/oauth/github/callback",
		}))
	}
	if cfg.OAuth.Google.Enabled && cfg.OAuth.Google.ClientID != "" {
		registry.Register(oauth.NewGoogleProvider(oauth.Config{
			ClientID:     cfg.OAuth.Google.ClientID,
			ClientSecret: cfg.OAuth.Google.ClientSecret,
			RedirectURL:  baseURL + "/v1/oauth/google/callback",
		}))
	}
	if cfg.OAuth.OIDC.Enabled && cfg.OAuth.OIDC.ClientID != "" && cfg.OAuth.OIDC.AuthorizeURL != "" && cfg.OAuth.OIDC.TokenURL != "" && cfg.OAuth.OIDC.UserInfoURL != "" {
		registry.Register(oauth.NewOIDCProvider(oauth.OIDCConfig{
			Config: oauth.Config{
				ClientID:     cfg.OAuth.OIDC.ClientID,
				ClientSecret: cfg.OAuth.OIDC.ClientSecret,
				RedirectURL:  baseURL + "/v1/oauth/oidc/callback",
			},
			AuthorizeURL: cfg.OAuth.OIDC.AuthorizeURL,
			TokenURL:     cfg.OAuth.OIDC.TokenURL,
			UserInfoURL:  cfg.OAuth.OIDC.UserInfoURL,
			Scopes:       cfg.OAuth.OIDC.Scopes,
		}))
	}
	return registry
}
