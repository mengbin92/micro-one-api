//go:build wireinject
// +build wireinject

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"

	"micro-one-api/internal/identity/biz"
	identitycfg "micro-one-api/internal/identity/config"
	"micro-one-api/internal/identity/data"
	"micro-one-api/internal/identity/server"
	"micro-one-api/internal/identity/service"
	"micro-one-api/internal/pkg/oauth"
)

var ProviderSet = wire.NewSet(
	data.NewRepositoryFromEnv,
	biz.NewIdentityUsecase,
	service.NewIdentityService,
	server.NewGRPCServer,
	server.NewHTTPServer,
)

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		loadConfig,
		ProviderSet,
		setupOAuth,
		newApp,
	))
}

func newApp(cfg *identitycfg.Config, svc *service.IdentityService, uc *biz.IdentityUsecase, oauthRegistry *oauth.ProviderRegistry) (*kratos.App, func()) {
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)
	httpSrv := server.NewHTTPServerWithRegistrationPolicy(cfg.Server.HTTP.Addr, uc, oauthRegistry, registrationPolicyFromConfig(cfg))
	app := kratos.New(
		kratos.Name("identity-service"),
		kratos.Server(grpcSrv, httpSrv),
	)
	return app, func() {}
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
