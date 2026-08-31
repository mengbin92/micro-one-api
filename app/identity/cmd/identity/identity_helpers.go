package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/server"
	grpcauth "micro-one-api/platform/grpc"
	"micro-one-api/platform/grpc/xgrpc"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/security" // package name is `oauth`
)

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

func registrationPolicyFromConfig(cfg *Config) server.RegistrationPolicy {
	if cfg == nil || cfg.Bootstrap == nil || cfg.Bootstrap.Registration == nil {
		return server.RegistrationPolicy{Enabled: true}
	}
	reg := cfg.Bootstrap.Registration
	enabled := !reg.Disabled
	if reg.Enabled {
		enabled = true
	}
	return server.RegistrationPolicy{
		Enabled:                       enabled,
		EmailDomainRestrictionEnabled: reg.EmailDomainRestrictionEnabled,
		EmailDomainWhitelist:          reg.EmailDomainWhitelist,
		TurnstileCheckEnabled:         reg.TurnstileCheckEnabled,
		TurnstileSecret:               reg.TurnstileSecret,
	}
}

func setupOAuth(cfg *Config) *oauth.ProviderRegistry {
	if cfg == nil || cfg.Bootstrap == nil || cfg.Bootstrap.Oauth == nil {
		return oauth.NewProviderRegistry()
	}
	registry := oauth.NewProviderRegistry()
	oauthCfg := cfg.Bootstrap.Oauth

	baseURL := oauthCfg.BaseUrl
	if baseURL == "" {
		baseURL = "http://localhost:8001"
	}

	if oauthCfg.Github != nil && oauthCfg.Github.Enabled && oauthCfg.Github.ClientId != "" {
		registry.Register(oauth.NewGitHubProvider(oauth.Config{
			ClientID:     oauthCfg.Github.ClientId,
			ClientSecret: oauthCfg.Github.ClientSecret,
			RedirectURL:  baseURL + "/v1/oauth/github/callback",
		}))
	}
	if oauthCfg.Google != nil && oauthCfg.Google.Enabled && oauthCfg.Google.ClientId != "" {
		registry.Register(oauth.NewGoogleProvider(oauth.Config{
			ClientID:     oauthCfg.Google.ClientId,
			ClientSecret: oauthCfg.Google.ClientSecret,
			RedirectURL:  baseURL + "/v1/oauth/google/callback",
		}))
	}
	if oauthCfg.Oidc != nil && oauthCfg.Oidc.Enabled && oauthCfg.Oidc.ClientId != "" &&
		oauthCfg.Oidc.AuthorizeUrl != "" && oauthCfg.Oidc.TokenUrl != "" && oauthCfg.Oidc.UserInfoUrl != "" {
		registry.Register(oauth.NewOIDCProvider(oauth.OIDCConfig{
			ClientID:     oauthCfg.Oidc.ClientId,
			ClientSecret: oauthCfg.Oidc.ClientSecret,
			RedirectURL:  baseURL + "/v1/oauth/oidc/callback",
			AuthorizeURL: oauthCfg.Oidc.AuthorizeUrl,
			TokenURL:     oauthCfg.Oidc.TokenUrl,
			UserInfoURL:  oauthCfg.Oidc.UserInfoUrl,
			Scopes:       oauthCfg.Oidc.Scopes,
		}))
	}
	if oauthCfg.Lark != nil && oauthCfg.Lark.Enabled && oauthCfg.Lark.ClientId != "" {
		registry.Register(oauth.NewLarkProvider(oauth.EndpointConfig{
			ClientID:     oauthCfg.Lark.ClientId,
			ClientSecret: oauthCfg.Lark.ClientSecret,
			RedirectURL:  baseURL + "/v1/oauth/lark/callback",
		}))
	}
	if oauthCfg.Wechat != nil && oauthCfg.Wechat.Enabled && oauthCfg.Wechat.ClientId != "" {
		registry.Register(oauth.NewWeChatProvider(oauth.EndpointConfig{
			ClientID:     oauthCfg.Wechat.ClientId,
			ClientSecret: oauthCfg.Wechat.ClientSecret,
			RedirectURL:  baseURL + "/v1/oauth/wechat/callback",
		}))
	}
	return registry
}

// newBillingClient creates the optional billing-service gRPC client. Returns
// a nil client and nil conn when no endpoint is configured.
func newBillingClient(cfg *Config) (billingv1.BillingServiceClient, *grpc.ClientConn, error) {
	if cfg == nil || cfg.Bootstrap == nil || cfg.Bootstrap.Clients == nil || cfg.Bootstrap.Clients.Billing == nil {
		return nil, nil, nil
	}
	if cfg.Bootstrap.Clients.Billing.Endpoint == "" {
		return nil, nil, nil
	}
	serviceToken := os.Getenv("SERVICE_TOKEN")
	if serviceToken == "" {
		return nil, nil, fmt.Errorf("SERVICE_TOKEN environment variable is required")
	}
	conn, err := grpc.NewClient(
		cfg.Bootstrap.Clients.Billing.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(serviceToken)),
		grpc.WithChainUnaryInterceptor(xgrpc.UnaryClientMetricsInterceptor("billing-service")),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to billing service: %w", err)
	}
	return billingv1.NewBillingServiceClient(conn), conn, nil
}
