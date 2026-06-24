//go:build !wireinject

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/internal/identity/biz"
	identitycfg "micro-one-api/internal/identity/config"
	"micro-one-api/internal/identity/data"
	"micro-one-api/internal/identity/server"
	"micro-one-api/internal/identity/service"
	applogger "micro-one-api/internal/pkg/logger"
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
	bootstrapAdmin(uc)
	svc := service.NewIdentityService(uc)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, svc)

	// Setup OAuth providers
	oauthRegistry := setupOAuth(cfg)
	var billingClient billingv1.BillingServiceClient
	var billingConn *grpc.ClientConn
	if cfg.Clients.Billing.Endpoint != "" {
		billingConn, err = grpc.NewClient(cfg.Clients.Billing.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to connect to billing service: %w", err)
		}
		billingClient = billingv1.NewBillingServiceClient(billingConn)
	}
	httpSrv := server.NewHTTPServerWithRegistrationPolicy(cfg.Server.HTTP.Addr, uc, oauthRegistry, registrationPolicyFromConfig(cfg), billingClient)

	// Setup service registration
	registrar, rErr := appregistry.NewRegistrar(cfg.Registry)
	if rErr != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(rErr))
	}

	kratosOpts := []kratos.Option{
		kratos.Name("identity-service"),
		kratos.Server(grpcSrv, httpSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

	cleanup := func() {
		if billingConn != nil {
			billingConn.Close()
		}
	}

	return app, cleanup, nil
}

// bootstrapAdmin creates the initial admin user if the users table is empty.
// Failures are logged but do not abort startup; operators can run the
// admin-reset tool or retry later. Generated passwords are never logged.
func bootstrapAdmin(uc *biz.IdentityUsecase) {
	result, err := uc.EnsureRootAdmin(context.Background())
	if err != nil {
		applogger.Log.Warn("admin bootstrap skipped", zap.Error(err))
		return
	}
	if !result.Created {
		return
	}
	if result.Generated {
		logGeneratedAdminPassword(result)
		return
	}
	applogger.Log.Info("initial admin created from INITIAL_ADMIN_PASSWORD env var", zap.String("username", result.Username))
}

func logGeneratedAdminPassword(result *biz.BootstrapResult) {
	passwordFile := strings.TrimSpace(os.Getenv("INITIAL_ADMIN_PASSWORD_FILE"))
	if passwordFile == "" {
		applogger.Log.Warn("initial admin created with generated password; password was not logged",
			zap.String("username", result.Username),
			zap.String("email", result.Email),
			zap.String("notice", "Set INITIAL_ADMIN_PASSWORD_FILE to write generated passwords to a private file, or use admin-reset to set a new password."),
		)
		return
	}
	if err := writeGeneratedPasswordFile(passwordFile, result.PlainPassword); err != nil {
		applogger.Log.Error("failed to write generated initial admin password file",
			zap.String("username", result.Username),
			zap.Error(err),
		)
		return
	}
	applogger.Log.Warn("initial admin created with generated password written to private file",
		zap.String("username", result.Username),
		zap.String("email", result.Email),
		zap.String("notice", "Read the file once, then log in and change the password immediately."),
	)
}

func writeGeneratedPasswordFile(path, password string) error {
	cleanPath, err := validateGeneratedPasswordFilePath(path)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(cleanPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304,G703 -- operator selects this absolute one-time password output file; path is cleaned, must not be a directory, and O_EXCL/0600 prevent overwrite and broad reads.
	if err != nil {
		return err
	}
	if _, err := file.WriteString(password + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func validateGeneratedPasswordFilePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("password file path is required")
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("password file path must be absolute")
	}
	cleanPath := filepath.Clean(path)
	if info, err := os.Stat(cleanPath); err == nil && info.IsDir() {
		return "", fmt.Errorf("password file path must not be a directory")
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return cleanPath, nil
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
	if cfg.OAuth.Lark.Enabled && cfg.OAuth.Lark.ClientID != "" {
		registry.Register(oauth.NewLarkProvider(oauth.EndpointConfig{
			Config: oauth.Config{
				ClientID:     cfg.OAuth.Lark.ClientID,
				ClientSecret: cfg.OAuth.Lark.ClientSecret,
				RedirectURL:  baseURL + "/v1/oauth/lark/callback",
			},
		}))
	}
	if cfg.OAuth.WeChat.Enabled && cfg.OAuth.WeChat.ClientID != "" {
		registry.Register(oauth.NewWeChatProvider(oauth.EndpointConfig{
			Config: oauth.Config{
				ClientID:     cfg.OAuth.WeChat.ClientID,
				ClientSecret: cfg.OAuth.WeChat.ClientSecret,
				RedirectURL:  baseURL + "/v1/oauth/wechat/callback",
			},
		}))
	}
	return registry
}
