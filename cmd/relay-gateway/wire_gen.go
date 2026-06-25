//go:build !wireinject

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2"
	kconfig "github.com/go-kratos/kratos/v2/config"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	appauth "micro-one-api/internal/pkg/auth"
	applogger "micro-one-api/internal/pkg/logger"
	appregistry "micro-one-api/internal/pkg/registry"
	apptimeout "micro-one-api/internal/pkg/timeout"
	apptls "micro-one-api/internal/pkg/tls"
	"micro-one-api/internal/pkg/xconfig"
	"micro-one-api/internal/pkg/xhttp"
	relaybiz "micro-one-api/internal/relay/biz"
	relaycfg "micro-one-api/internal/relay/config"
	relaydata "micro-one-api/internal/relay/data"
	relayprovider "micro-one-api/internal/relay/provider"
	"micro-one-api/internal/relay/server"
	relayservice "micro-one-api/internal/relay/service"
)

func loadConfig(confPath string) (*relaycfg.Config, error) {
	source := xconfig.NewEnvFileSource(confPath)
	kratosCfg := kconfig.New(kconfig.WithSource(source), kconfig.WithResolveActualTypes(true))
	defer kratosCfg.Close()
	if err := kratosCfg.Load(); err != nil {
		return nil, err
	}
	var cfg relaycfg.Config
	if err := kratosCfg.Scan(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

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

// InitApp loads config and builds the Kratos application.
func InitApp(confPath string) (*kratos.App, func(), error) {
	cfg, err := loadConfig(confPath)
	if err != nil {
		return nil, nil, err
	}

	tlsConfig := apptls.LoadTLSConfig()
	enableAuth := os.Getenv("ENABLE_AUTH") != "false" // Default to true; set ENABLE_AUTH=false to disable
	var serviceAuth *appauth.ServiceAuthConfig
	if enableAuth {
		serviceAuth, err = appauth.LoadServiceAuthConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load service auth config (set ENABLE_AUTH=false to skip): %w", err)
		}
	}

	providerTimeout := apptimeout.GetUpstreamTimeout()
	if timeoutStr := os.Getenv("RELAY_PROVIDER_TIMEOUT"); timeoutStr != "" {
		if duration, err := time.ParseDuration(timeoutStr); err == nil {
			providerTimeout = duration
		}
	}

	// Setup service discovery
	discovery, err := appregistry.NewDiscovery(cfg.Registry)
	if err != nil {
		applogger.Log.Warn("failed to create service discovery", zap.Error(err))
	}
	registrar, err := appregistry.NewRegistrar(cfg.Registry)
	if err != nil {
		applogger.Log.Warn("failed to create registrar", zap.Error(err))
	}

	resolver := appregistry.NewResolver(discovery)
	resolver.SetStatic("identity-service", cfg.Clients.Identity.Endpoint)
	resolver.SetStatic("channel-service", cfg.Clients.Channel.Endpoint)
	resolver.SetStatic("billing-service", cfg.Clients.Billing.Endpoint)
	resolver.SetStatic("log-service", cfg.Clients.Log.Endpoint)

	var identityConn, channelConn, billingConn, logConn *grpc.ClientConn
	var identityClient identityv1.IdentityServiceClient
	var channelClient channelv1.ChannelServiceClient
	var billingClient billingv1.BillingServiceClient
	var logClient logv1.LogServiceClient

	identityEndpoint, _ := resolver.ResolveGRPC(context.Background(), "identity-service")
	channelEndpoint, _ := resolver.ResolveGRPC(context.Background(), "channel-service")
	billingEndpoint, _ := resolver.ResolveGRPC(context.Background(), "billing-service")
	logEndpoint, _ := resolver.ResolveGRPC(context.Background(), "log-service")

	if enableAuth && tlsConfig.Enabled {
		identityConn, err = createAuthenticatedClient(identityEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to create identity client: %w", err)
		}
		channelConn, err = createAuthenticatedClient(channelEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			identityConn.Close()
			return nil, nil, fmt.Errorf("failed to create channel client: %w", err)
		}
		billingConn, err = createAuthenticatedClient(billingEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			identityConn.Close()
			channelConn.Close()
			return nil, nil, fmt.Errorf("failed to create billing client: %w", err)
		}
		logConn, err = createAuthenticatedClient(logEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			identityConn.Close()
			channelConn.Close()
			billingConn.Close()
			return nil, nil, fmt.Errorf("failed to create log client: %w", err)
		}
	} else {
		identityConn, err = grpc.NewClient(identityEndpoint,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, fmt.Errorf("failed to connect to identity: %w", err)
		}
		channelConn, err = grpc.NewClient(channelEndpoint,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			identityConn.Close()
			return nil, nil, fmt.Errorf("failed to connect to channel: %w", err)
		}
		billingConn, err = grpc.NewClient(billingEndpoint,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			identityConn.Close()
			channelConn.Close()
			return nil, nil, fmt.Errorf("failed to connect to billing: %w", err)
		}
		logConn, err = grpc.NewClient(logEndpoint,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			identityConn.Close()
			channelConn.Close()
			billingConn.Close()
			return nil, nil, fmt.Errorf("failed to connect to log: %w", err)
		}
	}

	identityClient = identityv1.NewIdentityServiceClient(identityConn)
	channelClient = channelv1.NewChannelServiceClient(channelConn)
	billingClient = billingv1.NewBillingServiceClient(billingConn)
	logClient = logv1.NewLogServiceClient(logConn)

	providerFactory := relayprovider.NewProviderFactory(providerTimeout)
	modelMapper := newModelMapper(cfg)
	retryPolicy := newRetryPolicy(cfg)

	// Create biz-layer RelayUsecase with model mapping and retry policy
	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, modelMapper, retryPolicy)

	httpServer := server.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase, logClient)

	// Configure Responses WebSocket relay timeouts from config (with defaults).
	{
		wsWrite, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSWriteTimeout())
		wsIdle, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSIdleTimeout())
		wsDial, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSDialTimeout())
		wsFirst, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSFirstMessageTimeout())
		httpServer.SetOpenAIWSTimeouts(wsWrite, wsIdle, wsDial, wsFirst)
	}

	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(cfg.Server.HTTP.Addr), khttp.Timeout(providerTimeout))...)
	httpServer.RegisterRoutes(srv)

	grpcSvc := relayservice.NewRelayGrpcService(identityClient, channelClient, billingClient, providerFactory, relayUsecase)
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, grpcSvc)

	kratosOpts := []kratos.Option{
		kratos.Name("relay-gateway"),
		kratos.Server(srv, grpcSrv),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

	applogger.Log.Info("relay-gateway starting", zap.String("http_addr", cfg.Server.HTTP.Addr))

	cleanup := func() {
		identityConn.Close()
		channelConn.Close()
		billingConn.Close()
		logConn.Close()
	}

	return app, cleanup, nil
}

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
