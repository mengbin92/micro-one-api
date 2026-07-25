//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v3"
	"github.com/google/wire"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	relayidentity "micro-one-api/internal/identity"
	"micro-one-api/internal/server"
	relayservice "micro-one-api/internal/service"
	apptimeout "micro-one-api/pkg/timeout"
	appaudit "micro-one-api/platform/audit"
	appcache "micro-one-api/platform/cache"
	"micro-one-api/platform/database/xdb"
	"micro-one-api/platform/events"
	appgrpc "micro-one-api/platform/grpc"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
	appmiddleware "micro-one-api/platform/middleware"
	appregistry "micro-one-api/platform/registry"
	appauth "micro-one-api/platform/security/auth"
	apptls "micro-one-api/platform/tls"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
	relaycredential "micro-one-api/domain/upstream/credential"
	relayadaptor "micro-one-api/internal/adaptor"

	"github.com/go-kratos/kratos/v3/config"
	xconfig "micro-one-api/platform/config"
)

// ProviderSet declares the relay-gateway providers. loadConfig lives in
// config_loader.go and the helper functions (newModelMapper, newRetryPolicy,
// createAuthenticatedClient, etc.) live in relay_helpers.go so they are
// visible under both build tags.
//
// relay-gateway's wiring is more complex than the other services (conditional
// client construction, environment-variable-driven configuration, etc.), so
// newApp constructs the provider factory, relay usecase, and HTTP server
// internally rather than declaring them as separate Wire providers.
var ProviderSet = wire.NewSet(
	loadConfig,
	newApp,
)

func InitApp(confPath string) (*kratos.App, func(), error) {
	panic(wire.Build(
		ProviderSet,
	))
}

func newApp(cfg *Config) (*kratos.App, func(), error) {
	tlsConfig := apptls.LoadTLSConfig()
	enableAuth := os.Getenv("ENABLE_AUTH") != "false"
	var serviceAuth *appauth.ServiceAuthConfig
	if enableAuth {
		var err error
		serviceAuth, err = appauth.LoadServiceAuthConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("load service auth config: %w", err)
		}
	}

	providerTimeout := apptimeout.GetUpstreamTimeout()
	if timeoutStr := os.Getenv("RELAY_PROVIDER_TIMEOUT"); timeoutStr != "" {
		if duration, err := time.ParseDuration(timeoutStr); err == nil {
			providerTimeout = duration
		}
	}

	discovery, err := appregistry.NewDiscovery(cfg.Registry())
	if err != nil {
		return nil, nil, fmt.Errorf("create discovery: %w", err)
	}
	registrar, err := appregistry.NewRegistrar(cfg.Registry())
	if err != nil {
		return nil, nil, fmt.Errorf("create registrar: %w", err)
	}

	resolver := appregistry.NewResolver(discovery)
	resolver.SetStatic("identity-service", cfg.Bootstrap.Clients.Identity.Endpoint)
	resolver.SetStatic("channel-service", cfg.Bootstrap.Clients.Channel.Endpoint)
	resolver.SetStatic("billing-service", cfg.Bootstrap.Clients.Billing.Endpoint)
	resolver.SetStatic("log-service", cfg.Bootstrap.Clients.Log.Endpoint)

	var identityConn, channelConn, billingConn, logConn *grpc.ClientConn
	var identityClient identityv1.IdentityServiceClient
	var channelClient channelv1.ChannelServiceClient
	var billingClient billingv1.BillingServiceClient
	var logClient logv1.LogServiceClient

	identityEndpoint, err := resolver.ResolveGRPC(context.Background(), "identity-service")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve identity-service endpoint: %w", err)
	}
	channelEndpoint, err := resolver.ResolveGRPC(context.Background(), "channel-service")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve channel-service endpoint: %w", err)
	}
	billingEndpoint, err := resolver.ResolveGRPC(context.Background(), "billing-service")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve billing-service endpoint: %w", err)
	}
	logEndpoint, err := resolver.ResolveGRPC(context.Background(), "log-service")
	if err != nil {
		return nil, nil, fmt.Errorf("resolve log-service endpoint: %w", err)
	}

	if enableAuth && tlsConfig.Enabled {
		identityConn, err = createAuthenticatedClient(identityEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			return nil, nil, fmt.Errorf("create identity gRPC client: %w", err)
		}
		channelConn, err = createAuthenticatedClient(channelEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			return nil, nil, fmt.Errorf("create channel gRPC client: %w", err)
		}
		billingConn, err = createAuthenticatedClient(billingEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			return nil, nil, fmt.Errorf("create billing gRPC client: %w", err)
		}
		logConn, err = createAuthenticatedClient(logEndpoint, tlsConfig, serviceAuth)
		if err != nil {
			return nil, nil, fmt.Errorf("create log gRPC client: %w", err)
		}
	} else {
		identityConn, err = grpc.NewClient(identityEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, fmt.Errorf("create identity gRPC client: %w", err)
		}
		channelConn, err = grpc.NewClient(channelEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, fmt.Errorf("create channel gRPC client: %w", err)
		}
		billingConn, err = grpc.NewClient(billingEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, fmt.Errorf("create billing gRPC client: %w", err)
		}
		logConn, err = grpc.NewClient(logEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, nil, fmt.Errorf("create log gRPC client: %w", err)
		}
	}

	identityClient = identityv1.NewIdentityServiceClient(identityConn)
	channelClient = channelv1.NewChannelServiceClient(channelConn)
	billingClient = billingv1.NewBillingServiceClient(billingConn)
	logClient = logv1.NewLogServiceClient(logConn)

	resilienceTimeout := parseDurationOrDefault(cfg.Bootstrap.Resilience.Timeout, 3*time.Second)
	if cfg.Bootstrap.Resilience.Enabled {
		identityClient = relaydata.NewResilientIdentityClient(identityClient, resilienceTimeout)
		channelClient = relaydata.NewResilientChannelClient(channelClient, resilienceTimeout)
		billingClient = relaydata.NewResilientBillingClient(billingClient, resilienceTimeout)
		logClient = relaydata.NewResilientLogClient(logClient, resilienceTimeout)
	}

	providerFactory := relayprovider.NewProviderFactory(providerTimeout)
	relayadaptor.SetProviderFactory(providerFactory)

	identityTTL := parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.GetIdentityTtl(), 24*time.Hour)
	identityService := relayidentity.NewIdentityService(identityTTL)
	relayadaptor.SetIdentityService(identityService)

	accountLookup := relaydata.NewChannelSubscriptionAccountStore(channelClient)
	claudeTokenProvider := relaycredential.NewClaudeTokenProvider(accountLookup)
	codexTokenProvider := relaycredential.NewOpenAITokenProvider(accountLookup)
	// StaticTokenProvider serves the domestic "Coding Plan" vendors that use a
	// long-lived API key with no refresh flow (Zhipu GLM, MiniMax). One shared
	// instance serves all static platforms; the platform tag lives on the
	// account record, not on the provider. It is deliberately NOT enrolled in
	// the RefreshTask map (no refresh token, no expiry).
	// See docs/design/cn-subscription-accounts-roadmap.md (P1/P2).
	staticTokenProvider := relaycredential.NewStaticTokenProvider(accountLookup)
	// Kimi uses an OAuth refresh flow; its provider is structurally identical
	// to the Claude/Codex providers. The client ID + refresh URL are exported
	// vars on the credential package and are overridable from config because
	// Kimi's CLI token endpoint is not a stable public API. See roadmap P3.
	kimiTokenProvider := relaycredential.NewKimiTokenProvider(accountLookup)
	if override := strings.TrimSpace(cfg.Bootstrap.HybridAdaptor.GetKimi().GetTokenRefreshUrl()); override != "" {
		relaycredential.KimiTokenRefreshURL = override
	}
	if override := strings.TrimSpace(cfg.Bootstrap.HybridAdaptor.GetKimi().GetClientId()); override != "" {
		relaycredential.KimiOAuthClientID = override
	}

	// tokenFactory is a table-driven platform -> TokenProvider dispatch. Adding
	// a new platform is one case here + (for refreshable platforms) one entry
	// in the refreshProviders map below.
	tokenFactory := func(platform relayidentity.Platform) relaycredential.TokenProvider {
		switch platform {
		case relayidentity.PlatformClaude:
			return claudeTokenProvider
		case relayidentity.PlatformCodex:
			return codexTokenProvider
		case relayidentity.PlatformZhipu, relayidentity.PlatformMinimax:
			return staticTokenProvider
		case relayidentity.PlatformKimi:
			return kimiTokenProvider
		default:
			return nil
		}
	}
	relayadaptor.SetTokenProviderFactory(tokenFactory)

	accountResolver := accountLookup
	oauthHTTPClient := &http.Client{Timeout: providerTimeout}

	// refreshProviders maps a platform to the provider that can refresh its
	// tokens. Static platforms (zhipu/minimax) are intentionally absent: their
	// keys do not expire, so enrolling them would only generate no-op sweep
	// work.
	var refreshTask *relaycredential.RefreshTask
	if cfg.Bootstrap.HybridAdaptor.GetTokenRefreshEnabled() {
		refreshTask = relaycredential.NewRefreshTask(
			map[relaycredential.Platform]relaycredential.TokenProvider{
				relaycredential.PlatformClaude: claudeTokenProvider,
				relaycredential.PlatformCodex:  codexTokenProvider,
				relaycredential.PlatformKimi:   kimiTokenProvider,
			},
			accountLookup,
			func(accountID int64) relaycredential.Platform {
				return accountLookup.PlatformOf(context.Background(), accountID)
			},
			relaycredential.RefreshTaskConfig{
				Interval:                  parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.GetRefreshInterval(), 10*time.Minute),
				Lookahead:                 parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.GetRefreshLookahead(), 24*time.Hour),
				MaxRetries:                int(cfg.Bootstrap.HybridAdaptor.TokenRefresh.MaxRetries),
				RetryBackoff:              time.Duration(cfg.Bootstrap.HybridAdaptor.TokenRefresh.RetryBackoffSeconds) * time.Second,
				TempUnschedulableDuration: parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.TokenRefresh.TempUnschedDuration, 10*time.Minute),
				Hook:                      accountLookup,
			},
		)
		refreshTask.Start()
	}

	redisAddr := cfg.Bootstrap.Redis.Addr
	redisPassword := cfg.Bootstrap.Redis.Password
	if redisAddr == "" {
		redisAddr = cfg.Bootstrap.OpenaiWs.RedisAddr
		redisPassword = cfg.Bootstrap.OpenaiWs.RedisPassword
	}
	redisClient := xdb.NewRedisClient(redisAddr, redisPassword)
	eventBus := events.NewConfiguredEventBus(redisClient, "relay-gateway")
	authLoader := appcache.NewAuthCacheLoader(identityClient, nil, resilienceTimeout)
	authCache, err := appcache.NewAuthCache(redisClient, nil, authLoader.Load)
	if err != nil {
		return nil, nil, fmt.Errorf("create auth cache: %w", err)
	}
	identityClient = relaydata.NewCachedIdentityClient(identityClient, authCache)

	if cfg.Bootstrap.ChannelCache.GetChannelCacheEnabled() {
		channelLoader := appcache.NewChannelCacheLoader(channelClient, nil, resilienceTimeout)
		channelCache, err := appcache.NewChannelCache(redisClient, nil, channelLoader.Load)
		if err != nil {
			return nil, nil, fmt.Errorf("create channel cache: %w", err)
		}
		channelClient = relaydata.NewCachedChannelClient(channelClient, channelCache)
	}

	modelMapper := newModelMapper(cfg)
	var modelReloadStop func()
	if mm := modelMapper; mm != nil {
		// Phase 2.5 — models.yaml hot reload. The callback re-reads the file
		// and atomically swaps the mapper's snapshot; a parse/validation
		// failure is logged and the previous snapshot remains in effect.
		if mp := modelsConfigPath(cfg); mp != "" {
			modelReloadStop, _ = xconfig.SubscribeFile(mp, func(_ *config.KeyValue) {
				if err := mm.Reload(); err != nil {
					applogger.Log.Warn("models.yaml hot reload failed; keeping previous snapshot", zap.String("path", mp), zap.Error(err))
					return
				}
				applogger.Log.Info("models.yaml hot-reloaded", zap.String("path", mp))
			})
		}
	}
	retryPolicy := newRetryPolicy(cfg)

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, modelMapper, retryPolicy)
	relayUsecase.SetRuntimeBlocker(relaybiz.NewMemoryRuntimeBlocker())

	httpServer := server.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase, logClient)
	httpServer.SetHybridAdaptorEnabled(cfg.Bootstrap.HybridAdaptor.GetHybridAdaptorEnabled())
	httpServer.SetSubscriptionSessionStickyEnabled(cfg.Bootstrap.SessionSticky.GetSessionStickyEnabled())
	httpServer.SetRelayOrchestratorEnabled(cfg.Bootstrap.RelayOrchestrator.GetRelayOrchestratorEnabled())
	httpServer.SetSubscriptionAccountResolver(accountResolver)
	httpServer.SetOAuthHTTPClient(oauthHTTPClient)
	httpServer.SetSubscriptionAccountQuotaRecorder(accountLookup)
	httpServer.SetUserRPMLimit(cfg.Bootstrap.Subscription.GetUserRPMLimit())
	// P3 #6: wire the billing-model-name source (requested/upstream/channel_mapped).
	if cfg.Bootstrap.BillingModelSource != nil {
		httpServer.SetBillingModelSource(cfg.Bootstrap.BillingModelSource.GetSource())
	}
	httpServer.SetRuntimeBlockDurations(
		parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.RuntimeBlock.GetRateLimitedDuration(), 5*time.Second),
		parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.RuntimeBlock.GetUnauthorizedDuration(), 2*time.Minute),
		parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.RuntimeBlock.GetServerErrorDuration(), 2*time.Minute),
		parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.RuntimeBlock.GetOverloadedDuration(), 30*time.Second),
	)
	stopBlockerReporter := func() {}
	if redisClient != nil {
		redisBlocker := relaybiz.NewRedisRuntimeBlocker(redisClient)
		httpServer.SetRuntimeBlocker(redisBlocker)
		stopBlockerReporter = redisBlocker.StartActiveGaugeReporter(
			parseDurationOrDefault(cfg.Bootstrap.HybridAdaptor.RuntimeBlock.GetActiveGaugeInterval(), 30*time.Second),
			func(v float64) { metrics.RelayRuntimeBlockActive.Set(v) },
		)
		if redisLimiter := relaybiz.NewRedisAccountConcurrencyLimiter(redisClient); redisLimiter != nil {
			httpServer.SetAccountConcurrencyLimiter(redisLimiter)
		}
		if redisRPMLimiter := relaybiz.NewRedisAccountRPMLimiter(redisClient); redisRPMLimiter != nil {
			httpServer.SetAccountRPMLimiter(redisRPMLimiter)
		}
		if redisUserRPMLimiter := relaybiz.NewRedisUserRPMLimiter(redisClient); redisUserRPMLimiter != nil {
			httpServer.SetUserRPMLimiter(redisUserRPMLimiter)
		}
	}

	var routeMiddleware []func(http.Handler) http.Handler
	if cfg.Bootstrap.Subscription.GetSubscriptionEnabled() {
		subscriptionRepo, subErr := subscriptiondata.NewRepositoryFromEnv(os.Getenv("SQL_DRIVER"))
		if subErr != nil {
			return nil, nil, fmt.Errorf("create subscription repository: %w", subErr)
		}
		subscriptionUc := subscriptionbiz.NewSubscriptionUsecase(subscriptionRepo, subscriptionRepo)
		httpServer.SetSubscriptionUsecase(subscriptionUc)
		routeMiddleware = append(routeMiddleware, httpServer.SubscriptionQuotaMiddleware)
	}
	if cfg.Bootstrap.Idempotency.Enabled {
		ttl := parseDurationOrDefault(cfg.Bootstrap.Idempotency.Ttl, 24*time.Hour)
		routeMiddleware = append(routeMiddleware, appmiddleware.NewIdempotencyMiddleware(redisClient, &appmiddleware.IdempotencyConfig{
			Header:    "Idempotency-Key",
			TTL:       ttl,
			CacheKeys: true,
		}).Handler)
	}
	if cfg.Bootstrap.Audit.Enabled {
		routeMiddleware = append(routeMiddleware, appaudit.NewMiddleware(appaudit.NewAuditor(true)).Handler)
	}
	httpServer.UseRouteMiddleware(routeMiddleware...)

	{
		// WebSocket timeouts are optional: an unparseable value defaults to
		// the zero duration, which the HTTP server treats as "use builtin
		// default". This is an explicit, safe degradation.
		wsWrite, _ := time.ParseDuration(cfg.Bootstrap.OpenaiWs.GetOpenAIWSWriteTimeout())
		wsIdle, _ := time.ParseDuration(cfg.Bootstrap.OpenaiWs.GetOpenAIWSIdleTimeout())
		wsDial, _ := time.ParseDuration(cfg.Bootstrap.OpenaiWs.GetOpenAIWSDialTimeout())
		wsFirst, _ := time.ParseDuration(cfg.Bootstrap.OpenaiWs.GetOpenAIWSFirstMessageTimeout())
		// Phase 3.3: graceful-drain config must be set before the connection
		// pool is built so the tracker is created with the configured
		// DrainTimeout / CloseTimeout. Empty / unparseable values fall back to
		// the platform defaults (DrainTimeout=30s, CloseTimeout=10s).
		wsDrain, _ := time.ParseDuration(cfg.Bootstrap.OpenaiWs.GetOpenAIWSDrainTimeout())
		if wsDrain > 0 {
			httpServer.SetOpenAIWSDrainConfig(appwsDrainConfig(wsDrain))
		}
		httpServer.SetOpenAIWSTimeouts(wsWrite, wsIdle, wsDial, wsFirst)
		httpServer.SetOpenAIWSConnPool()
		httpServer.SetOpenAIWSPoolConfig(
			cfg.Bootstrap.OpenaiWs.GetOpenAIWSMaxConnsPerChannel(),
			cfg.Bootstrap.OpenaiWs.GetOpenAIWSFailoverMaxSwitches(),
			parseDurationOrDefault(cfg.Bootstrap.OpenaiWs.GetOpenAIWSStickyTTL(), time.Hour),
		)
		httpServer.SetOpenAIWSStickyStore(redisClient)
	}

	srv := newKratosHTTPServer(cfg, httpServer, providerTimeout)

	grpcSvc := relayservice.NewRelayGrpcService(identityClient, channelClient, billingClient, providerFactory, relayUsecase)
	var relayGRPCOpts []grpc.ServerOption
	if cfg.Bootstrap.Mtls.Enabled {
		mtlsOpts, mtlsErr := appgrpc.MTLSServerOptions(cfg.Bootstrap.Mtls.CertFile, cfg.Bootstrap.Mtls.KeyFile, cfg.Bootstrap.Mtls.CaFile)
		if mtlsErr != nil {
			return nil, nil, fmt.Errorf("create relay mTLS server options: %w", mtlsErr)
		}
		relayGRPCOpts = append(relayGRPCOpts, mtlsOpts...)
	}
	grpcSrv := server.NewGRPCServer(cfg.Bootstrap.Server.Grpc.Addr, grpcSvc, relayGRPCOpts...)

	// Phase 3.3: graceful drain. On SIGTERM the BeforeStop hook drains the
	// tracked WebSocket connections for up to drain_timeout, then the kratos
	// StopTimeout (slightly larger than the drain window) bounds the whole
	// shutdown so an unresponsive client cannot stall termination indefinitely.
	drainTimeout := parseDurationOrDefault(cfg.Bootstrap.OpenaiWs.GetOpenAIWSDrainTimeout(), 30*time.Second)
	stopTimeout := drainTimeout + 10*time.Second

	kratosOpts := []kratos.Option{
		kratos.Name("relay-gateway"),
		kratos.Server(srv, grpcSrv),
		kratos.StopTimeout(stopTimeout),
		kratos.BeforeStop(func(ctx context.Context) error {
			drainCtx, cancel := context.WithTimeout(ctx, drainTimeout)
			defer cancel()
			_ = httpServer.DrainWSConnections(drainCtx)
			return nil
		}),
	}
	if registrar != nil {
		kratosOpts = append(kratosOpts, kratos.Registrar(registrar))
	}
	app := kratos.New(kratosOpts...)

	applogger.Log.Info("relay-gateway starting", zap.String("http_addr", cfg.Bootstrap.Server.Http.Addr))

	cleanup := func() {
		if modelReloadStop != nil {
			modelReloadStop()
		}
		if refreshTask != nil {
			refreshTask.Stop()
		}
		stopBlockerReporter()
		if authCache != nil {
			_ = authCache.Close()
		}
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
