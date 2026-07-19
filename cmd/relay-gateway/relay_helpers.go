package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"

	relaybiz "micro-one-api/internal/biz"
	relaycfg "micro-one-api/internal/conf"
	"micro-one-api/internal/server"
	applogger "micro-one-api/platform/logging"
	appauth "micro-one-api/platform/security/auth"
	apptls "micro-one-api/platform/tls"
	appws "micro-one-api/platform/websocket"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
	xhttp "micro-one-api/platform/http"
)

// newModelMapper creates a ModelMapper from config, returning nil on error.
func newModelMapper(cfg *relaycfg.Config) *relaybiz.ModelMapper {
	path := cfg.Models.Path
	if path == "" {
		for _, candidate := range []string{"/configs/models.yaml", "configs/models.yaml"} {
			if _, err := os.Stat(candidate); err == nil {
				path = candidate
				break
			}
		}
	}
	mapper, err := relaybiz.NewModelMapper(path)
	if err != nil {
		applogger.Log.Warn("failed to load models config", zap.String("path", path), zap.Error(err))
		return nil
	}
	return mapper
}

// newRetryPolicy creates a RetryPolicy from config.
func newRetryPolicy(cfg *relaycfg.Config) *relaybiz.RetryPolicy {
	retryCfg := cfg.Retry
	statuses := make(map[int]bool)
	for _, s := range retryCfg.GetRetryableStatus() {
		statuses[s] = true
	}
	initialInterval, err := time.ParseDuration(retryCfg.GetInitialInterval())
	if err != nil {
		initialInterval = 500 * time.Millisecond
	}
	maxInterval, err := time.ParseDuration(retryCfg.GetMaxInterval())
	if err != nil {
		maxInterval = 5 * time.Second
	}
	return &relaybiz.RetryPolicy{
		MaxAttempts:     retryCfg.GetMaxAttempts(),
		InitialInterval: initialInterval,
		MaxInterval:     maxInterval,
		Multiplier:      retryCfg.GetMultiplier(),
		RetryableStatus: statuses,
	}
}

// newKratosHTTPServer creates a kratos HTTP server for relay-gateway.
// It wraps the internal HTTPServer and registers its routes.
func newKratosHTTPServer(cfg *relaycfg.Config, httpServer *server.HTTPServer, providerTimeout time.Duration) *khttp.Server {
	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(cfg.Server.HTTP.Addr), khttp.Timeout(providerTimeout))...)
	httpServer.RegisterRoutes(srv)
	return srv
}

// createAuthenticatedClient creates a gRPC client connection with mTLS and
// optional service-auth token credentials.
func createAuthenticatedClient(
	endpoint string,
	tlsConfig *apptls.TLSConfig,
	serviceAuth *appauth.ServiceAuthConfig,
) (*grpc.ClientConn, error) {
	creds, err := apptls.CreateClientCredentials(tlsConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS credentials: %w", err)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	if serviceAuth != nil && serviceAuth.Token != "" {
		tokenCreds := &tokenAuth{token: serviceAuth.Token}
		opts = append(opts, grpc.WithPerRPCCredentials(tokenCreds))
	}
	conn, err := grpc.NewClient(endpoint, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %s: %w", endpoint, err)
	}
	return conn, nil
}

type tokenAuth struct {
	token string
}

func (t *tokenAuth) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	return map[string]string{
		"authorization": "Bearer " + t.token,
	}, nil
}

func (t *tokenAuth) RequireTransportSecurity() bool {
	return true
}

// parseDurationOrDefault parses a duration string, returning the default on error.
func parseDurationOrDefault(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return def
}

// suppress unused import warnings
var _ = http.Client{}

// appwsDrainConfig builds a platform/websocket.DrainConfig from a drain
// timeout. CloseTimeout and NotifyBeforeClose use sensible fractions of the
// drain window; MaxConcurrentClose caps the goroutine fan-out on force close.
func appwsDrainConfig(drainTimeout time.Duration) *appws.DrainConfig {
	closeTimeout := drainTimeout / 3
	if closeTimeout < 5*time.Second {
		closeTimeout = 5 * time.Second
	}
	notifyBefore := drainTimeout / 6
	if notifyBefore < 2*time.Second {
		notifyBefore = 2 * time.Second
	}
	return &appws.DrainConfig{
		DrainTimeout:       drainTimeout,
		CloseTimeout:       closeTimeout,
		NotifyBeforeClose:  notifyBefore,
		MaxConcurrentClose: 100,
	}
}

// modelsConfigPath returns the resolved path to models.yaml: cfg.Models.Path
// when set, otherwise the first existing well-known default. Phase 2.5: used
// by the hot-reload subscriber so it watches the same file newModelMapper
// actually loaded.
func modelsConfigPath(cfg *relaycfg.Config) string {
	if cfg != nil && cfg.Models.Path != "" {
		return cfg.Models.Path
	}
	for _, candidate := range []string{"/configs/models.yaml", "configs/models.yaml"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
