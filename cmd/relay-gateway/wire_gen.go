//go:build !wireinject

package main

import (
	"context"
	"fmt"
	"net/http"
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
	appaudit "micro-one-api/internal/pkg/audit"
	appauth "micro-one-api/internal/pkg/auth"
	appcache "micro-one-api/internal/pkg/cache"
	"micro-one-api/internal/pkg/events"
	appgrpc "micro-one-api/internal/pkg/grpc"
	applogger "micro-one-api/internal/pkg/logger"
	appmiddleware "micro-one-api/internal/pkg/middleware"
	appregistry "micro-one-api/internal/pkg/registry"
	apptimeout "micro-one-api/internal/pkg/timeout"
	apptls "micro-one-api/internal/pkg/tls"
	"micro-one-api/internal/pkg/xconfig"
	"micro-one-api/internal/pkg/xdb"
	"micro-one-api/internal/pkg/xhttp"
	relayadaptor "micro-one-api/internal/relay/adaptor"
	relaybiz "micro-one-api/internal/relay/biz"
	relaycfg "micro-one-api/internal/relay/config"
	relaycredential "micro-one-api/internal/relay/credential"
	relaydata "micro-one-api/internal/relay/data"
	relayidentity "micro-one-api/internal/relay/identity"
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

	resilienceTimeout := parseDurationOrDefault(cfg.Resilience.Timeout, 3*time.Second)
	if cfg.Resilience.Enabled {
		identityClient = relaydata.NewResilientIdentityClient(identityClient, resilienceTimeout)
		channelClient = relaydata.NewResilientChannelClient(channelClient, resilienceTimeout)
		billingClient = relaydata.NewResilientBillingClient(billingClient, resilienceTimeout)
		logClient = relaydata.NewResilientLogClient(logClient, resilienceTimeout)
	}

	providerFactory := relayprovider.NewProviderFactory(providerTimeout)
	// Wire the adaptor registry with the shared provider factory so the
	// hybrid adaptor layer (relay/adaptor) can dispatch to the same upstream
	// providers. Existing server code still calls providerFactory directly;
	// the registry is exercised by the feature-flag-controlled new path.
	relayadaptor.SetProviderFactory(providerFactory)

	// --- Hybrid adaptor: identity + credential layers (plan §4.4/§4.5) ---
	// The identity service caches subscription-account fingerprints; the
	// credential layer resolves OAuth access tokens (refreshing on demand).
	// When the hybrid_adaptor feature flag is off these are still constructed
	// (cheap) but never used on the request path.
	identityTTL := parseDurationOrDefault(cfg.HybridAdaptor.GetIdentityTTL(), 24*time.Hour)
	identityService := relayidentity.NewIdentityService(identityTTL)
	relayadaptor.SetIdentityService(identityService)

	accountLookup := relaydata.NewChannelSubscriptionAccountStore(channelClient)

	// Construct each platform's TokenProvider exactly once so the request-path
	// factory and the background refresh task share the same instance (and
	// therefore the same in-process token cache + per-account mutex). Creating
	// two separate instances per platform would defeat the refresh task's
	// pre-warming, since the cache is per-instance.
	claudeTokenProvider := relaycredential.NewClaudeTokenProvider(accountLookup)
	codexTokenProvider := relaycredential.NewOpenAITokenProvider(accountLookup)

	tokenFactory := func(platform relayidentity.Platform) relaycredential.TokenProvider {
		switch platform {
		case relayidentity.PlatformClaude:
			return claudeTokenProvider
		case relayidentity.PlatformCodex:
			return codexTokenProvider
		default:
			return nil
		}
	}
	relayadaptor.SetTokenProviderFactory(tokenFactory)

	accountResolver := accountLookup

	// OAuth upstream HTTP client: shares the gateway's upstream timeout so
	// subscription-account calls do not outlive the configured provider
	// timeout. Mirrors the client the provider factory builds internally.
	oauthHTTPClient := &http.Client{Timeout: providerTimeout}

	// Background token-refresh task. Started only when the feature flag is on.
	var refreshTask *relaycredential.RefreshTask
	if cfg.HybridAdaptor.GetHybridAdaptorEnabled() {
		refreshTask = relaycredential.NewRefreshTask(
			map[relaycredential.Platform]relaycredential.TokenProvider{
				relaycredential.PlatformClaude: claudeTokenProvider,
				relaycredential.PlatformCodex:  codexTokenProvider,
			},
			accountLookup,
			func(accountID int64) relaycredential.Platform {
				return accountLookup.PlatformOf(context.Background(), accountID)
			},
			relaycredential.RefreshTaskConfig{
				Interval:  parseDurationOrDefault(cfg.HybridAdaptor.GetRefreshInterval(), 10*time.Minute),
				Lookahead: parseDurationOrDefault(cfg.HybridAdaptor.GetRefreshLookahead(), 24*time.Hour),
			},
		)
		refreshTask.Start()
	}
	_ = refreshTask // referenced in cleanup below
	redisAddr := cfg.Redis.Addr
	redisPassword := cfg.Redis.Password
	if redisAddr == "" {
		redisAddr = cfg.OpenAIWS.RedisAddr
		redisPassword = cfg.OpenAIWS.RedisPassword
	}
	redisClient := xdb.NewRedisClient(redisAddr, redisPassword)
	eventBus := events.NewConfiguredEventBus(redisClient, "relay-gateway")
	authLoader := appcache.NewAuthCacheLoader(identityClient, nil, resilienceTimeout)
	authCache, err := appcache.NewAuthCache(redisClient, nil, authLoader.Load)
	if err != nil {
		return nil, nil, fmt.Errorf("create auth cache: %w", err)
	}
	identityClient = relaydata.NewCachedIdentityClient(identityClient, authCache)

	// ChannelCache fronts the channel-service SelectChannel RPC. It is gated
	// by the channel_cache feature flag (default off); when off the client is
	// used unchanged. Failover selections bypass the cache, so retry/failover
	// never replays a failed top-priority channel.
	if cfg.ChannelCache.GetChannelCacheEnabled() {
		channelLoader := appcache.NewChannelCacheLoader(channelClient, nil, resilienceTimeout)
		channelCache, cErr := appcache.NewChannelCache(redisClient, nil, channelLoader.Load)
		if cErr != nil {
			return nil, nil, fmt.Errorf("create channel cache: %w", cErr)
		}
		channelClient = relaydata.NewCachedChannelClient(channelClient, channelCache)
	}

	modelMapper := newModelMapper(cfg)
	retryPolicy := newRetryPolicy(cfg)

	// Create biz-layer RelayUsecase with model mapping and retry policy
	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, modelMapper, retryPolicy)

	httpServer := server.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase, logClient)
	httpServer.SetHybridAdaptorEnabled(cfg.HybridAdaptor.GetHybridAdaptorEnabled())
	httpServer.SetRelayOrchestratorEnabled(cfg.RelayOrchestrator.GetRelayOrchestratorEnabled())
	httpServer.SetSubscriptionAccountResolver(accountResolver)
	httpServer.SetOAuthHTTPClient(oauthHTTPClient)
	var routeMiddleware []func(http.Handler) http.Handler
	if cfg.Idempotency.Enabled {
		ttl := parseDurationOrDefault(cfg.Idempotency.TTL, 24*time.Hour)
		routeMiddleware = append(routeMiddleware, appmiddleware.NewIdempotencyMiddleware(redisClient, &appmiddleware.IdempotencyConfig{
			Header:    "Idempotency-Key",
			TTL:       ttl,
			CacheKeys: true,
		}).Handler)
	}
	if cfg.Audit.Enabled {
		routeMiddleware = append(routeMiddleware, appaudit.NewMiddleware(appaudit.NewAuditor(true)).Handler)
	}
	httpServer.UseRouteMiddleware(routeMiddleware...)

	// Configure Responses WebSocket relay timeouts from config (with defaults).
	{
		wsWrite, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSWriteTimeout())
		wsIdle, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSIdleTimeout())
		wsDial, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSDialTimeout())
		wsFirst, _ := time.ParseDuration(cfg.OpenAIWS.GetOpenAIWSFirstMessageTimeout())
		httpServer.SetOpenAIWSTimeouts(wsWrite, wsIdle, wsDial, wsFirst)
		httpServer.SetOpenAIWSConnPool()
		httpServer.SetOpenAIWSPoolConfig(
			cfg.OpenAIWS.GetOpenAIWSMaxConnsPerChannel(),
			cfg.OpenAIWS.GetOpenAIWSFailoverMaxSwitches(),
			parseDurationOrDefault(cfg.OpenAIWS.GetOpenAIWSStickyTTL(), time.Hour),
		)
		httpServer.SetOpenAIWSStickyStore(redisClient)
	}

	srv := khttp.NewServer(xhttp.SafeKratosServerOptions(khttp.Address(cfg.Server.HTTP.Addr), khttp.Timeout(providerTimeout))...)
	httpServer.RegisterRoutes(srv)

	grpcSvc := relayservice.NewRelayGrpcService(identityClient, channelClient, billingClient, providerFactory, relayUsecase)
	var relayGRPCOpts []grpc.ServerOption
	if cfg.MTLS.Enabled {
		mtlsOpts, err := appgrpc.MTLSServerOptions(cfg.MTLS.CertFile, cfg.MTLS.KeyFile, cfg.MTLS.CAFile)
		if err != nil {
			return nil, nil, fmt.Errorf("create relay mTLS server options: %w", err)
		}
		relayGRPCOpts = append(relayGRPCOpts, mtlsOpts...)
	}
	grpcSrv := server.NewGRPCServer(cfg.Server.GRPC.Addr, grpcSvc, relayGRPCOpts...)

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
		if refreshTask != nil {
			refreshTask.Stop()
		}
		_ = authCache.Close()
		if closer, ok := eventBus.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		if redisClient != nil {
			_ = redisClient.Close()
		}
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

// parseDurationOrDefault parses a duration string, returning the default on error.
func parseDurationOrDefault(s string, def time.Duration) time.Duration {
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	return def
}
