//go:build wireinject
// +build wireinject

package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-kratos/kratos/v2"
	"github.com/google/wire"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	relaybiz "micro-one-api/internal/biz"
	relaycfg "micro-one-api/internal/conf"
	relaydata "micro-one-api/internal/data"
	relayidentity "micro-one-api/internal/identity"
	relayprovider "micro-one-api/domain/upstream/provider"
	relayservice "micro-one-api/internal/service"
	"micro-one-api/internal/server"
	appaudit "micro-one-api/platform/audit"
	appauth "micro-one-api/platform/security/auth"
	appcache "micro-one-api/platform/cache"
	"micro-one-api/platform/events"
	appgrpc "micro-one-api/platform/grpc"
	applogger "micro-one-api/platform/logging"
	appmiddleware "micro-one-api/platform/middleware"
	appregistry "micro-one-api/platform/registry"
	apptimeout "micro-one-api/pkg/timeout"
	apptls "micro-one-api/platform/tls"
	"micro-one-api/platform/database/xdb"
	"micro-one-api/platform/metrics"

	relayadaptor "micro-one-api/internal/adaptor"
	relaycredential "micro-one-api/domain/upstream/credential"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	subscriptiondata "micro-one-api/domain/subscription/data"
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

func newApp(cfg *relaycfg.Config) (*kratos.App, func()) {
	tlsConfig := apptls.LoadTLSConfig()
	enableAuth := os.Getenv("ENABLE_AUTH") != "false"
	var serviceAuth *appauth.ServiceAuthConfig
	if enableAuth {
		serviceAuth, _ = appauth.LoadServiceAuthConfig()
	}

	providerTimeout := apptimeout.GetUpstreamTimeout()
	if timeoutStr := os.Getenv("RELAY_PROVIDER_TIMEOUT"); timeoutStr != "" {
		if duration, err := time.ParseDuration(timeoutStr); err == nil {
			providerTimeout = duration
		}
	}

	discovery, _ := appregistry.NewDiscovery(cfg.Registry)
	registrar, _ := appregistry.NewRegistrar(cfg.Registry)

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
		identityConn, _ = createAuthenticatedClient(identityEndpoint, tlsConfig, serviceAuth)
		channelConn, _ = createAuthenticatedClient(channelEndpoint, tlsConfig, serviceAuth)
		billingConn, _ = createAuthenticatedClient(billingEndpoint, tlsConfig, serviceAuth)
		logConn, _ = createAuthenticatedClient(logEndpoint, tlsConfig, serviceAuth)
	} else {
		identityConn, _ = grpc.NewClient(identityEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		channelConn, _ = grpc.NewClient(channelEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		billingConn, _ = grpc.NewClient(billingEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
		logConn, _ = grpc.NewClient(logEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
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
	relayadaptor.SetProviderFactory(providerFactory)

	identityTTL := parseDurationOrDefault(cfg.HybridAdaptor.GetIdentityTTL(), 24*time.Hour)
	identityService := relayidentity.NewIdentityService(identityTTL)
	relayadaptor.SetIdentityService(identityService)

	accountLookup := relaydata.NewChannelSubscriptionAccountStore(channelClient)
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
	oauthHTTPClient := &http.Client{Timeout: providerTimeout}

	var refreshTask *relaycredential.RefreshTask
	if cfg.HybridAdaptor.GetTokenRefreshEnabled() {
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
				Interval:                  parseDurationOrDefault(cfg.HybridAdaptor.GetRefreshInterval(), 10*time.Minute),
				Lookahead:                 parseDurationOrDefault(cfg.HybridAdaptor.GetRefreshLookahead(), 24*time.Hour),
				MaxRetries:                cfg.HybridAdaptor.TokenRefresh.MaxRetries,
				RetryBackoff:              time.Duration(cfg.HybridAdaptor.TokenRefresh.RetryBackoffSeconds) * time.Second,
				TempUnschedulableDuration: parseDurationOrDefault(cfg.HybridAdaptor.TokenRefresh.TempUnschedDuration, 10*time.Minute),
				Hook:                      accountLookup,
			},
		)
		refreshTask.Start()
	}

	redisAddr := cfg.Redis.Addr
	redisPassword := cfg.Redis.Password
	if redisAddr == "" {
		redisAddr = cfg.OpenAIWS.RedisAddr
		redisPassword = cfg.OpenAIWS.RedisPassword
	}
	redisClient := xdb.NewRedisClient(redisAddr, redisPassword)
	eventBus := events.NewConfiguredEventBus(redisClient, "relay-gateway")
	authLoader := appcache.NewAuthCacheLoader(identityClient, nil, resilienceTimeout)
	authCache, _ := appcache.NewAuthCache(redisClient, nil, authLoader.Load)
	identityClient = relaydata.NewCachedIdentityClient(identityClient, authCache)

	if cfg.ChannelCache.GetChannelCacheEnabled() {
		channelLoader := appcache.NewChannelCacheLoader(channelClient, nil, resilienceTimeout)
		channelCache, _ := appcache.NewChannelCache(redisClient, nil, channelLoader.Load)
		channelClient = relaydata.NewCachedChannelClient(channelClient, channelCache)
	}

	modelMapper := newModelMapper(cfg)
	retryPolicy := newRetryPolicy(cfg)

	identityAdapter := relaydata.NewIdentityAdapter(identityClient)
	channelAdapter := relaydata.NewChannelAdapter(channelClient)
	relayUsecase := relaybiz.NewRelayUsecase(identityAdapter, channelAdapter, modelMapper, retryPolicy)
	relayUsecase.SetRuntimeBlocker(relaybiz.NewMemoryRuntimeBlocker())

	httpServer := server.NewHTTPServer(identityClient, channelClient, billingClient, providerFactory, relayUsecase, logClient)
	httpServer.SetHybridAdaptorEnabled(cfg.HybridAdaptor.GetHybridAdaptorEnabled())
	httpServer.SetSubscriptionSessionStickyEnabled(cfg.SessionSticky.GetSessionStickyEnabled())
	httpServer.SetRelayOrchestratorEnabled(cfg.RelayOrchestrator.GetRelayOrchestratorEnabled())
	httpServer.SetSubscriptionAccountResolver(accountResolver)
	httpServer.SetOAuthHTTPClient(oauthHTTPClient)
	httpServer.SetSubscriptionAccountQuotaRecorder(accountLookup)
	httpServer.SetUserRPMLimit(cfg.Subscription.GetUserRPMLimit())
	httpServer.SetRuntimeBlockDurations(
		parseDurationOrDefault(cfg.HybridAdaptor.RuntimeBlock.GetRateLimitedDuration(), 5*time.Second),
		parseDurationOrDefault(cfg.HybridAdaptor.RuntimeBlock.GetUnauthorizedDuration(), 2*time.Minute),
		parseDurationOrDefault(cfg.HybridAdaptor.RuntimeBlock.GetServerErrorDuration(), 2*time.Minute),
		parseDurationOrDefault(cfg.HybridAdaptor.RuntimeBlock.GetOverloadedDuration(), 30*time.Second),
	)
	stopBlockerReporter := func() {}
	if redisClient != nil {
		redisBlocker := relaybiz.NewRedisRuntimeBlocker(redisClient)
		httpServer.SetRuntimeBlocker(redisBlocker)
		stopBlockerReporter = redisBlocker.StartActiveGaugeReporter(
			parseDurationOrDefault(cfg.HybridAdaptor.RuntimeBlock.GetActiveGaugeInterval(), 30*time.Second),
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
	if cfg.Subscription.GetSubscriptionEnabled() {
		subscriptionRepo, _ := subscriptiondata.NewRepositoryFromEnv(os.Getenv("SQL_DRIVER"))
		subscriptionUc := subscriptionbiz.NewSubscriptionUsecase(subscriptionRepo, subscriptionRepo)
		httpServer.SetSubscriptionUsecase(subscriptionUc)
		routeMiddleware = append(routeMiddleware, httpServer.SubscriptionQuotaMiddleware)
	}
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

	srv := newKratosHTTPServer(cfg, httpServer, providerTimeout)

	grpcSvc := relayservice.NewRelayGrpcService(identityClient, channelClient, billingClient, providerFactory, relayUsecase)
	var relayGRPCOpts []grpc.ServerOption
	if cfg.MTLS.Enabled {
		mtlsOpts, err := appgrpc.MTLSServerOptions(cfg.MTLS.CertFile, cfg.MTLS.KeyFile, cfg.MTLS.CAFile)
		if err != nil {
			applogger.Log.Error("create relay mTLS server options", zap.Error(err))
		} else {
			relayGRPCOpts = append(relayGRPCOpts, mtlsOpts...)
		}
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

	_ = fmt.Sprintf
	return app, cleanup
}
