package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	relaycredential "micro-one-api/domain/upstream/credential"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	applogger "micro-one-api/platform/logging"
	appws "micro-one-api/platform/websocket"
)

const postResponseWriteTimeout = 10 * time.Second

const defaultQuotaPerUSD = 500000

const amountUnitsPerUSD = 10000

// HTTPServer handles HTTP requests for relay-gateway.

type HTTPServer struct {
	identityClient     identityv1.IdentityServiceClient
	channelClient      channelv1.ChannelServiceClient
	billingClient      billingv1.BillingServiceClient
	logClient          logv1.LogServiceClient
	providerFactory    *relayprovider.ProviderFactory
	relayUsecase       *relaybiz.RelayUsecase
	responsesMu        sync.RWMutex
	responseRoutes     map[string]responseRouteEntry
	responsesLastSweep time.Time
	wsTimeouts         openAIWSTimeouts
	wsPool             *openAIWSConnPool
	wsSticky           *openAIWSStickyStore
	wsPoolCfg          openAIWSPoolConfig
	wsScheduler        *OpenAIWSRoutingScheduler
	// wsConnTracker tracks active client WebSocket connections so the gateway
	// can drain them gracefully on shutdown (Phase 3.3). nil until
	// SetOpenAIWSConnPool wires it; all accessors are nil-safe so tests and
	// the default-disabled path behave exactly as before.
	wsConnTracker   *appws.ConnectionTracker
	wsDrainCfg      appws.DrainConfig
	runtimeBlockCfg runtimeBlockConfig

	// hybridAdaptorEnabled gates the new adaptor-based request path (plan §十).
	// When false the gateway uses the legacy provider-factory path unchanged.
	hybridAdaptorEnabled bool

	// subscriptionSessionStickyEnabled gates cross-session subscription-account
	// stickiness (docs #7) for the chat-completions and anthropic-messages
	// entry points. When true, the adaptor loop binds session_hash to the
	// account that served the request so subsequent turns reuse it. It is a
	// no-op unless hybridAdaptorEnabled is also true.
	subscriptionSessionStickyEnabled bool

	// relayOrchestratorEnabled gates the handler -> orchestrator -> forwarder
	// request path for /v1/chat/completions. It remains disabled by default so
	// the legacy billing path is preserved unless explicitly enabled.
	relayOrchestratorEnabled bool
	routeMiddleware          []func(http.Handler) http.Handler

	// accountResolver resolves subscription-account metadata (real account id,
	// upstream account id, fingerprint) for subscription-typed channels. nil
	// when the hybrid path is disabled.
	accountResolver relaycredential.SubscriptionAccountResolver

	// oauthHTTPClient is the HTTP client used for subscription-account
	// upstream calls. It mirrors the provider-factory timeout so OAuth calls
	// don't outlive the configured upstream timeout.
	oauthHTTPClient *http.Client

	// subscriptionUsecase is an optional business-layer hook used to enforce
	// user subscription quota and record usage after successful commits.
	subscriptionUsecase *subscriptionbiz.SubscriptionUsecase

	accountQuotaRecorder subscriptionAccountQuotaRecorder
	runtimeBlocker       relaybiz.RuntimeBlocker

	// accountConcurrency enforces SubscriptionAccount.Concurrency per account.
	// Never nil after NewHTTPServer.
	accountConcurrency relaybiz.AccountConcurrencyLimiter
	// accountRPM enforces SubscriptionAccount.RPMLimit per account.
	accountRPM relaybiz.AccountRPMLimiter
	// userRPM enforces a global per-user request-per-minute limit.
	userRPM      relaybiz.AccountRPMLimiter
	userRPMLimit int32
	// sessionWindow tracks per-session cost windows for subscription accounts.
	sessionWindow *subscriptionSessionWindowStore
}

func (s *HTTPServer) Plan(ctx context.Context, req relaybiz.RelayRequest) (*relaybiz.RelayPlan, error) {
	if s == nil || s.relayUsecase == nil {
		return nil, fmt.Errorf("relay usecase unavailable")
	}
	return s.relayUsecase.Plan(ctx, req)
}

// openAIWSTimeouts holds parsed durations for the Responses WebSocket relay.

// Zero values fall back to defaults in the relay server.

// openAIWSPoolConfig holds connection-pool + failover tunables. Zero values

// fall back to defaults.

// runtimeBlockConfig holds per-status runtime cool-down durations. Zero values

// fall back to the built-in defaults in runtimeBlockDuration.

// responseRouteEntry wraps a responseRoute with an expiry so the in-process

// route map can be swept. Without a TTL the map grows once per unique upstream

// response ID for the lifetime of the process (memory leak).

const (
	// responseRouteTTL bounds how long a stored response route is retained.
	// Continuations reference a prior response within a short window, so a
	// generous but finite TTL is safe.
	responseRouteTTL = 30 * time.Minute
	// responseRouteSweepInterval throttles how often storeResponseRoute performs
	// a full expired-entry sweep, so writes stay O(1) amortized.
	responseRouteSweepInterval = time.Minute
)

// NewHTTPServer creates a new HTTP server for Kratos.

func NewHTTPServer(
	identityClient identityv1.IdentityServiceClient,
	channelClient channelv1.ChannelServiceClient,
	billingClient billingv1.BillingServiceClient,
	providerFactory *relayprovider.ProviderFactory,
	relayUsecase *relaybiz.RelayUsecase,
	logClients ...logv1.LogServiceClient,
) *HTTPServer {
	var logClient logv1.LogServiceClient
	if len(logClients) > 0 {
		logClient = logClients[0]
	}
	runtimeBlocker := relaybiz.NewMemoryRuntimeBlocker()
	if relayUsecase != nil {
		relayUsecase.SetRuntimeBlocker(runtimeBlocker)
	}
	return &HTTPServer{
		identityClient:     identityClient,
		channelClient:      channelClient,
		billingClient:      billingClient,
		logClient:          logClient,
		providerFactory:    providerFactory,
		relayUsecase:       relayUsecase,
		responseRoutes:     make(map[string]responseRouteEntry),
		runtimeBlocker:     runtimeBlocker,
		accountConcurrency: relaybiz.NewAccountConcurrencyLimiter(),
		accountRPM:         relaybiz.NewAccountRPMLimiter(),
		userRPM:            relaybiz.NewAccountRPMLimiter(),
		sessionWindow:      newSubscriptionSessionWindowStore(nil),
	}
}

// SetHybridAdaptorEnabled turns on the hybrid adaptor request path. When true,

// subscription-account channel types (Codex/Claude OAuth) are routed through

// the relay/adaptor layer instead of the provider factory. API-key channels are

// unaffected and continue to use the existing path either way.

func (s *HTTPServer) SetHybridAdaptorEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.hybridAdaptorEnabled = enabled
}

// SetSubscriptionSessionStickyEnabled turns on session -> subscription-account

// stickiness for the chat-completions and anthropic-messages entry points. It

// only takes effect when the hybrid adaptor path is enabled (bind happens in

// the adaptor failover loop).

func (s *HTTPServer) SetSubscriptionSessionStickyEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.subscriptionSessionStickyEnabled = enabled
	s.syncSessionStickyToUsecase()
}

// syncSessionStickyToUsecase pushes the current sticky store, TTL and enable

// flag into the biz RelayUsecase so its Plan can reuse the session-bound

// account. It is called from both the store and the flag setters so the result

// is consistent regardless of call order. A nil store is passed as a true nil

// interface (not a typed-nil) so the usecase correctly treats stickiness as off.

func (s *HTTPServer) syncSessionStickyToUsecase() {
	if s == nil || s.relayUsecase == nil {
		return
	}
	var store relaybiz.SessionAccountStore
	if s.wsSticky != nil {
		store = s.wsSticky
	}
	s.relayUsecase.SetSessionAccountStore(store, s.openAIWSStickyTTL(), s.subscriptionSessionStickyEnabled)
}

// SetRelayOrchestratorEnabled turns on the orchestrator-based chat route.

func (s *HTTPServer) SetRelayOrchestratorEnabled(enabled bool) {
	if s == nil {
		return
	}
	s.relayOrchestratorEnabled = enabled
}

// UseRouteMiddleware applies middleware to routes registered by RegisterRoutes.

func (s *HTTPServer) UseRouteMiddleware(middleware ...func(http.Handler) http.Handler) {
	if s == nil {
		return
	}
	s.routeMiddleware = append(s.routeMiddleware, middleware...)
}

// SetSubscriptionAccountResolver wires the resolver that maps a

// subscription-typed channel to the underlying subscription-account metadata.

// Required when the hybrid adaptor path is enabled.

func (s *HTTPServer) SetSubscriptionAccountResolver(r relaycredential.SubscriptionAccountResolver) {
	if s == nil {
		return
	}
	s.accountResolver = r
}

// SetOAuthHTTPClient sets the HTTP client used for subscription-account

// upstream calls. It should carry the gateway's upstream timeout so OAuth

// calls do not hang indefinitely.

func (s *HTTPServer) SetOAuthHTTPClient(c *http.Client) {
	if s == nil {
		return
	}
	s.oauthHTTPClient = c
}

// SetSubscriptionUsecase wires the optional subscription business hook.

// When unset the relay gateway behaves exactly as before.

func (s *HTTPServer) SetSubscriptionUsecase(uc *subscriptionbiz.SubscriptionUsecase) {
	if s == nil {
		return
	}
	s.subscriptionUsecase = uc
}

func (s *HTTPServer) SetSubscriptionAccountQuotaRecorder(recorder subscriptionAccountQuotaRecorder) {
	if s == nil {
		return
	}
	s.accountQuotaRecorder = recorder
}

func (s *HTTPServer) SetRuntimeBlocker(blocker relaybiz.RuntimeBlocker) {
	if s == nil {
		return
	}
	if blocker == nil {
		blocker = relaybiz.NoopRuntimeBlocker{}
	}
	s.runtimeBlocker = blocker
	if s.relayUsecase != nil {
		s.relayUsecase.SetRuntimeBlocker(blocker)
	}
}

func (s *HTTPServer) SetAccountConcurrencyLimiter(limiter relaybiz.AccountConcurrencyLimiter) {
	if s == nil {
		return
	}
	if limiter == nil {
		limiter = relaybiz.NewAccountConcurrencyLimiter()
	}
	s.accountConcurrency = limiter
}

func (s *HTTPServer) SetAccountRPMLimiter(limiter relaybiz.AccountRPMLimiter) {
	if s == nil {
		return
	}
	if limiter == nil {
		limiter = relaybiz.NewAccountRPMLimiter()
	}
	s.accountRPM = limiter
}

func (s *HTTPServer) SetUserRPMLimiter(limiter relaybiz.AccountRPMLimiter) {
	if s == nil {
		return
	}
	if limiter == nil {
		limiter = relaybiz.NewAccountRPMLimiter()
	}
	s.userRPM = limiter
}

func (s *HTTPServer) SetUserRPMLimit(limit int32) {
	if s == nil {
		return
	}
	s.userRPMLimit = limit
}

// isSubscriptionChannel reports whether the channel type is a subscription

// account handled by the OAuth adaptor layer. These types are only routed

// through the adaptor when the hybrid feature flag is enabled.

// SetRuntimeBlockDurations configures the per-status runtime cool-down applied

// to a subscription account after a retryable upstream failure. Non-positive

// values keep the built-in defaults (429=5s, 401=2m, 5xx=2m, 529=30s).

func (s *HTTPServer) SetRuntimeBlockDurations(rateLimited, unauthorized, serverError, overloaded time.Duration) {
	if s == nil {
		return
	}
	s.runtimeBlockCfg = runtimeBlockConfig{
		rateLimited:  rateLimited,
		unauthorized: unauthorized,
		serverError:  serverError,
		overloaded:   overloaded,
	}
}

// SetOpenAIWSTimeouts configures the Responses WebSocket relay timeouts. It is

// optional; when not called the forwarder uses built-in defaults. Durations are

// parsed from the relay config string fields (see wire_gen.go).

func (s *HTTPServer) SetOpenAIWSTimeouts(writeTimeout, idleTimeout, dialTimeout, firstMessageTimeout time.Duration) {
	if s == nil {
		return
	}
	s.wsTimeouts = openAIWSTimeouts{
		writeTimeout:        writeTimeout,
		idleTimeout:         idleTimeout,
		dialTimeout:         dialTimeout,
		firstMessageTimeout: firstMessageTimeout,
	}
}

// SetOpenAIWSStickyStore configures the cross-process response->channel sticky

// store backed by Redis. Pass a nil client to use in-memory only.

func (s *HTTPServer) SetOpenAIWSStickyStore(rdb *redis.Client) {
	if s == nil {
		return
	}
	s.wsSticky = newOpenAIWSStickyStore(rdb)
	s.sessionWindow = newSubscriptionSessionWindowStore(rdb)
	s.wsScheduler = NewOpenAIWSRoutingScheduler(s)
	// The same sticky store also backs session -> subscription-account
	// stickiness in the biz planner (docs #7).
	s.syncSessionStickyToUsecase()
}

// SetOpenAIWSConnPool configures the upstream connection pool. Must be called

// after SetOpenAIWSTimeouts since it reads the dial timeout. It also activates
// the platform/websocket.ConnectionTracker used for Phase 3.3 graceful drain:
// every accepted client Responses-WS connection is registered here so that, on
// shutdown, DrainWSConnections can wait for in-flight turns to finish (or
// force-close after DrainTimeout) before the HTTP server stops accepting.

func (s *HTTPServer) SetOpenAIWSConnPool() {
	if s == nil {
		return
	}
	s.wsPool = newOpenAIWSConnPool(s.openAIWSDialTimeout())
	if s.wsConnTracker == nil {
		s.wsConnTracker = appws.NewConnectionTracker(s.drainConfig())
	}
}

// SetOpenAIWSDrainConfig overrides the graceful-drain configuration. Call
// before SetOpenAIWSConnPool so the tracker is built with the custom config;
// a later call rebuilds the tracker, dropping any previously registered
// connections (only intended during startup wiring, not at runtime).

func (s *HTTPServer) SetOpenAIWSDrainConfig(cfg *appws.DrainConfig) {
	if s == nil {
		return
	}
	if cfg == nil {
		cfg = appws.DefaultDrainConfig()
	}
	s.wsDrainCfg = *cfg
	s.wsConnTracker = appws.NewConnectionTracker(cfg)
}

// drainConfig returns the effective drain configuration, falling back to the
// platform default when unset.

func (s *HTTPServer) drainConfig() *appws.DrainConfig {
	if s == nil {
		return appws.DefaultDrainConfig()
	}
	if s.wsDrainCfg.DrainTimeout > 0 {
		return &s.wsDrainCfg
	}
	return appws.DefaultDrainConfig()
}

// defaultDrainTimeout caches the platform-default drain timeout once so the
// hot healthz path does not allocate a new DrainConfig on every probe.
var defaultDrainTimeout = sync.OnceValue(func() time.Duration {
	return appws.DefaultDrainConfig().DrainTimeout
})

// drainTimeout returns the effective drain timeout as a duration. It mirrors
// drainConfig() but avoids allocating a config object on the hot healthz path.
func (s *HTTPServer) drainTimeout() time.Duration {
	if s == nil {
		return defaultDrainTimeout()
	}
	if s.wsDrainCfg.DrainTimeout > 0 {
		return s.wsDrainCfg.DrainTimeout
	}
	return defaultDrainTimeout()
}

// IsWSDraining reports whether the gateway is draining WebSocket connections
// prior to shutdown. Load balancers probe /healthz to pull the instance.

func (s *HTTPServer) IsWSDraining() bool {
	if s == nil || s.wsConnTracker == nil {
		return false
	}
	return s.wsConnTracker.IsDraining()
}

// DrainWSConnections initiates graceful drain of all tracked client WebSocket
// connections. It is idempotent and safe to call from a kratos.BeforeStop hook:
// the tracker's Drain sets a CAS flag so new upgrades are rejected (see
// handleResponsesWebSocket), existing relays are given DrainTimeout to
// complete, and any that remain are force-closed. Returns the drain error
// (context.DeadlineExceeded on force-close) for the caller to log.

func (s *HTTPServer) DrainWSConnections(ctx context.Context) error {
	if s == nil || s.wsConnTracker == nil {
		return nil
	}
	if applogger.Log != nil {
		applogger.Log.Info("draining openai responses websocket connections",
			zap.Int("active", s.wsConnTracker.ActiveCount()),
		)
	}
	err := s.wsConnTracker.Drain(ctx)
	if err != nil && applogger.Log != nil {
		applogger.Log.Warn("openai ws drain finished with error",
			zap.Int("active_remaining", s.wsConnTracker.ActiveCount()),
			zap.Error(err),
		)
	} else if applogger.Log != nil {
		m := s.wsConnTracker.Metrics()
		applogger.Log.Info("openai ws drain complete",
			zap.Int64("total", m.TotalConnections),
			zap.Int64("graceful", m.ClosedGracefully),
			zap.Int64("forced", m.ClosedByForce),
			zap.Int("active_remaining", s.wsConnTracker.ActiveCount()),
		)
	}
	return err
}

// SetOpenAIWSPoolConfig configures pool + failover tunables.

func (s *HTTPServer) SetOpenAIWSPoolConfig(maxConnsPerChannel, failoverMaxSwitches int, stickyTTL time.Duration) {
	if s == nil {
		return
	}
	s.wsPoolCfg = openAIWSPoolConfig{
		maxConnsPerChannel:  maxConnsPerChannel,
		failoverMaxSwitches: failoverMaxSwitches,
		stickyTTL:           stickyTTL,
	}
}

// isAnthropicAPIKeyChannel reports whether the channel is an Anthropic

// API-key channel (type=2) whose upstream speaks the Anthropic Messages API

// (/v1/messages) rather than OpenAI Responses. Such channels must convert

// inbound Responses requests to Anthropic Messages before forwarding, and

// convert the upstream response back to Responses.

// handleSubscriptionUsage returns the authenticated user's active subscription

// usage (daily/weekly/monthly used/limit/remaining + next refresh timestamps).

// It is the API-key-authenticated counterpart to the admin

// /api/v1/subscriptions/progress endpoint, exposed on the relay gateway so

// external tools (e.g. cc-switch) can query a subscription plan's usage with

// the same API key they already use for /v1/chat/completions.

//

// Response shape (non-subscription / no active subscription returns success:false

// with an explanatory message so callers can distinguish "no subscription" from

// a real error):

//

//	{

//	  "success": true,

//	  "isValid": true,

//	  "is_active": true,

//	  "status": "active",

//	  "mode": "subscription",

//	  "planName": "<group display name>",

//	  "unit": "USD",

//	  "data": {

//	    "id": 7, "status": "active", "starts_at": ..., "expires_at": ...,

//	    "group_id": ..., "subscription_name": "...", "remaining_seconds": ...,

//	    "daily_used":   {"used":..,"limit":..,"remaining":..,"next_refresh":..},

//	    "weekly_used":  {"used":..,"limit":..,"remaining":..,"next_refresh":..},

//	    "monthly_used": {"used":..,"limit":..,"remaining":..,"next_refresh":..}

//	  }

//	}

// handleRelayPlanError maps biz-layer Plan() errors to HTTP responses.
