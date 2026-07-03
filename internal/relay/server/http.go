package server

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bytedance/sonic"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/internal/pkg/errors"
	applogger "micro-one-api/internal/pkg/logger"
	"micro-one-api/internal/pkg/metrics"
	relaybiz "micro-one-api/internal/relay/biz"
	relaycredential "micro-one-api/internal/relay/credential"
	relayprovider "micro-one-api/internal/relay/provider"
	subscriptionbiz "micro-one-api/internal/subscription/biz"
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
	runtimeBlockCfg    runtimeBlockConfig

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

	// accountConcurrency enforces SubscriptionAccount.Concurrency per account
	// within this process. Never nil after NewHTTPServer.
	accountConcurrency *relaybiz.AccountConcurrencyLimiter
}

func (s *HTTPServer) Plan(ctx context.Context, req relaybiz.RelayRequest) (*relaybiz.RelayPlan, error) {
	if s == nil || s.relayUsecase == nil {
		return nil, fmt.Errorf("relay usecase unavailable")
	}
	return s.relayUsecase.Plan(ctx, req)
}

// openAIWSTimeouts holds parsed durations for the Responses WebSocket relay.
// Zero values fall back to defaults in the relay server.
type openAIWSTimeouts struct {
	writeTimeout        time.Duration
	idleTimeout         time.Duration
	dialTimeout         time.Duration
	firstMessageTimeout time.Duration
}

// openAIWSPoolConfig holds connection-pool + failover tunables. Zero values
// fall back to defaults.
type openAIWSPoolConfig struct {
	maxConnsPerChannel  int
	failoverMaxSwitches int
	stickyTTL           time.Duration
}

// runtimeBlockConfig holds per-status runtime cool-down durations. Zero values
// fall back to the built-in defaults in runtimeBlockDuration.
type runtimeBlockConfig struct {
	rateLimited  time.Duration // 429
	unauthorized time.Duration // 401
	serverError  time.Duration // 5xx
	overloaded   time.Duration // 529
}

type responseRoute struct {
	Model                 string
	ResolvedModel         string
	Channel               relaybiz.Channel
	UserID                int64
	SubscriptionAccountID int64
}

// responseRouteEntry wraps a responseRoute with an expiry so the in-process
// route map can be swept. Without a TTL the map grows once per unique upstream
// response ID for the lifetime of the process (memory leak).
type responseRouteEntry struct {
	route     responseRoute
	expiresAt time.Time
}

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

// isSubscriptionChannel reports whether the channel type is a subscription
// account handled by the OAuth adaptor layer. These types are only routed
// through the adaptor when the hybrid feature flag is enabled.
func isSubscriptionChannel(t int32) bool {
	switch t {
	case relayprovider.ChannelTypeCodexOAuth, relayprovider.ChannelTypeClaudeOAuth:
		return true
	default:
		return false
	}
}

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
	s.wsScheduler = NewOpenAIWSRoutingScheduler(s)
	// The same sticky store also backs session -> subscription-account
	// stickiness in the biz planner (docs #7).
	s.syncSessionStickyToUsecase()
}

// SetOpenAIWSConnPool configures the upstream connection pool. Must be called
// after SetOpenAIWSTimeouts since it reads the dial timeout.
func (s *HTTPServer) SetOpenAIWSConnPool() {
	if s == nil {
		return
	}
	s.wsPool = newOpenAIWSConnPool(s.openAIWSDialTimeout())
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

func (s *HTTPServer) handleRawRelay(upstreamPath string, requireModel bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			s.writeError(w, http.StatusUnauthorized, "missing authorization header")
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") {
			s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
			return
		}
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			s.writeError(w, http.StatusUnauthorized, "missing token")
			return
		}

		body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
		if err != nil {
			s.writeError(w, http.StatusBadRequest, "failed to read request body")
			return
		}

		clientModel := extractRawModel(body)
		if clientModel == "" {
			clientModel = defaultRawModel(upstreamPath)
		}
		if requireModel && clientModel == "" {
			s.writeError(w, http.StatusBadRequest, "model is required")
			return
		}

		plan, err := s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
			Token: token,
			Model: clientModel,
		})
		if err != nil {
			s.handleRelayPlanError(w, err)
			return
		}
		upstreamBody := rewriteRawModel(body, plan.ResolvedModel)

		var upstreamResp *relayprovider.RawResponse
		retryExecutor := s.relayUsecase.NewRetryExecutor()
		result := retryExecutor.ExecuteWithInitialChannel(r.Context(), plan.Auth.Group, plan.ResolvedModel, plan.Channel, func(ctx context.Context, ch *relaybiz.Channel) error {
			startedAt := time.Now()
			requestID := generateRequestID()
			reservation, reserveErr := s.reserveQuota(
				ctx,
				fmt.Sprintf("%d", plan.Auth.UserID),
				requestID,
				estimateRawTokens(body),
				plan.ResolvedModel,
				fmt.Sprintf("%d", ch.ID),
				subscriptionAccountIDFromPlan(plan),
			)
			if reserveErr != nil {
				return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
			}

			provider, provErr := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
				APIVersion: ch.Config.APIVersion,
			})
			if provErr != nil {
				_ = s.releaseQuota(ctx, reservation.ReservationId, "failed to create provider")
				return fmt.Errorf("failed to create provider: %w", provErr)
			}

			resp, forwardErr := provider.Forward(ctx, &relayprovider.RawRequest{
				Method: r.Method,
				Path:   upstreamPath,
				Query:  r.URL.RawQuery,
				Header: r.Header.Clone(),
				Body:   upstreamBody,
			})
			if forwardErr != nil {
				_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
				return forwardErr
			}

			usage := extractRawUsage(resp.Body, estimateRawTokens(body))
			logInput := usageLogInput{
				UserID:           plan.Auth.UserID,
				TokenID:          plan.Auth.TokenID,
				TokenName:        plan.Auth.TokenName,
				RequestID:        requestID,
				Endpoint:         upstreamPath,
				ModelName:        clientModel,
				Quota:            usage.TotalTokens,
				PromptTokens:     usage.PromptTokens,
				CompletionTokens: usage.CompletionTokens,
				CacheReadTokens:  usage.CacheReadTokens,
				ChannelID:        ch.ID,
				ElapsedTime:      time.Since(startedAt).Milliseconds(),
				IsStream:         false,
			}
			logUpstreamUsage(logInput)
			if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
				return err
			}
			s.ingestUsageLog(ctx, logInput)
			upstreamResp = resp
			return nil
		})

		if result.Err != nil {
			s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
			return
		}
		if upstreamResp == nil {
			s.writeError(w, http.StatusBadGateway, "upstream service error")
			return
		}

		writeRawResponse(w, upstreamResp)
	}
}

func (s *HTTPServer) handleResponsesRelay(w http.ResponseWriter, r *http.Request) {
	// Codex Responses WebSocket: when the client sends an Upgrade: websocket
	// request against /v1/responses, hand off to the WS forwarder instead of
	// the HTTP/SSE path. This is the ingress point for the new Responses WS
	// protocol used by the Codex CLI.
	if isOpenAIWSUpgradeRequest(r) {
		s.handleResponsesWebSocket(r.Context(), w, r)
		return
	}

	upstreamPath := r.URL.Path
	if strings.HasPrefix(upstreamPath, "/v1/") {
		upstreamPath = strings.TrimPrefix(upstreamPath, "/v1")
	}

	if r.URL.Path == "/v1/responses" || r.URL.Path == "/v1/responses/input_tokens" || r.URL.Path == "/v1/responses/compact" {
		if r.Method != http.MethodPost {
			s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.handleResponsesCreateLike(w, r, upstreamPath)
		return
	}

	responseID, ok := parseResponsesResourcePath(r.Method, r.URL.Path)
	if !ok {
		s.writeError(w, http.StatusNotFound, "response not found")
		return
	}
	s.handleResponsesResource(w, r, upstreamPath, responseID)
}

func (s *HTTPServer) handleResponsesCreateLike(w http.ResponseWriter, r *http.Request, upstreamPath string) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	clientModel := extractRawModel(body)
	previousResponseID := extractPreviousResponseID(body)
	sessionHash := extractSessionHashFromRequest(r, body)
	if clientModel == "" {
		if previousRoute, ok := s.lookupResponseRouteWithSticky(r.Context(), token, previousResponseID); ok {
			s.forwardResponsesToStoredRoute(w, r, upstreamPath, body, token, previousRoute, isRawStreamRequest(body))
			return
		}
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	var plan *relaybiz.RelayPlan
	if s.wsScheduler != nil {
		plan, err = s.wsScheduler.ResolvePlan(r.Context(), token, clientModel, previousResponseID, sessionHash)
	} else {
		plan, err = s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
			Token: token,
			Model: clientModel,
		})
	}
	if err != nil {
		s.handleRelayPlanError(w, err)
		return
	}
	if s.hybridAdaptorEnabled && plan.Channel != nil && isSubscriptionChannel(plan.Channel.Type) {
		s.handleResponsesCreateLikeViaAdaptor(w, r, plan, clientModel, body)
		return
	}
	upstreamBody := rewriteRawModel(body, plan.ResolvedModel)

	var upstreamResp *relayprovider.RawResponse
	var responseChannel *relaybiz.Channel
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.ExecuteWithInitialChannel(r.Context(), plan.Auth.Group, plan.ResolvedModel, plan.Channel, func(ctx context.Context, ch *relaybiz.Channel) error {
		startedAt := time.Now()
		requestID := generateRequestID()
		reservation, reserveErr := s.reserveQuota(
			ctx,
			fmt.Sprintf("%d", plan.Auth.UserID),
			requestID,
			estimateRawTokens(upstreamBody),
			plan.ResolvedModel,
			fmt.Sprintf("%d", ch.ID),
			subscriptionAccountIDFromPlan(plan),
		)
		if reserveErr != nil {
			return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
		}

		if isRawStreamRequest(body) {
			streamResp, streamErr := s.forwardResponsesRawStream(ctx, ch, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), upstreamBody)
			if streamErr != nil {
				if shouldFallbackResponsesToChat(upstreamPath, streamErr) {
					fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(ctx, ch, r.Header.Clone(), upstreamBody)
					if fallbackErr == nil && fallbackResp.Stream != nil {
						usage := newRawStreamUsageTracker(estimateRawUsage(upstreamBody))
						writeRawStreamResponse(w, fallbackResp.Stream, usage)
						actualUsage := usage.Usage()
						logInput := usageLogInput{
							UserID:           plan.Auth.UserID,
							TokenID:          plan.Auth.TokenID,
							TokenName:        plan.Auth.TokenName,
							RequestID:        requestID,
							Endpoint:         "/chat/completions",
							ModelName:        clientModel,
							Quota:            actualUsage.TotalTokens,
							PromptTokens:     actualUsage.PromptTokens,
							CompletionTokens: actualUsage.CompletionTokens,
							CacheReadTokens:  actualUsage.CacheReadTokens,
							ChannelID:        ch.ID,
							ElapsedTime:      time.Since(startedAt).Milliseconds(),
							IsStream:         true,
						}
						if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
							s.logPostResponseCommitError(err)
						} else {
							logUpstreamUsage(logInput)
							s.ingestUsageLogAfterResponse(logInput)
						}
						upstreamResp = &relayprovider.RawResponse{StatusCode: fallbackResp.Stream.StatusCode}
						responseChannel = ch
						if responseID := usage.ResponseID(); responseID != "" {
							s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *ch, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
						}
						return nil
					}
				}
				_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream stream error")
				return streamErr
			}
			usage := newRawStreamUsageTracker(estimateRawUsage(upstreamBody))
			writeRawStreamResponse(w, streamResp, usage)
			actualUsage := usage.Usage()
			logInput := usageLogInput{
				UserID:           plan.Auth.UserID,
				TokenID:          plan.Auth.TokenID,
				TokenName:        plan.Auth.TokenName,
				RequestID:        requestID,
				Endpoint:         upstreamPath,
				ModelName:        clientModel,
				Quota:            actualUsage.TotalTokens,
				PromptTokens:     actualUsage.PromptTokens,
				CompletionTokens: actualUsage.CompletionTokens,
				CacheReadTokens:  actualUsage.CacheReadTokens,
				ChannelID:        ch.ID,
				ElapsedTime:      time.Since(startedAt).Milliseconds(),
				IsStream:         true,
			}
			if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
				s.logPostResponseCommitError(err)
			} else {
				logUpstreamUsage(logInput)
				s.ingestUsageLogAfterResponse(logInput)
			}
			upstreamResp = &relayprovider.RawResponse{StatusCode: streamResp.StatusCode}
			responseChannel = ch
			if responseID := usage.ResponseID(); responseID != "" {
				s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *ch, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
			}
			return nil
		}

		resp, forwardErr := s.forwardResponsesRaw(ctx, ch, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), upstreamBody)
		if forwardErr != nil {
			if shouldFallbackResponsesToChat(upstreamPath, forwardErr) {
				fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(ctx, ch, r.Header.Clone(), upstreamBody)
				if fallbackErr == nil && fallbackResp.Response != nil {
					usage := fallbackResp.Usage
					if usage.TotalTokens <= 0 {
						usage = extractRawUsage(fallbackResp.Response.Body, estimateRawTokens(upstreamBody))
					}
					logInput := usageLogInput{
						UserID:           plan.Auth.UserID,
						TokenID:          plan.Auth.TokenID,
						TokenName:        plan.Auth.TokenName,
						RequestID:        requestID,
						Endpoint:         "/chat/completions",
						ModelName:        clientModel,
						Quota:            usage.TotalTokens,
						PromptTokens:     usage.PromptTokens,
						CompletionTokens: usage.CompletionTokens,
						CacheReadTokens:  usage.CacheReadTokens,
						ChannelID:        ch.ID,
						ElapsedTime:      time.Since(startedAt).Milliseconds(),
						IsStream:         false,
					}
					if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
						return err
					}
					logUpstreamUsage(logInput)
					s.ingestUsageLog(ctx, logInput)
					upstreamResp = fallbackResp.Response
					responseChannel = ch
					return nil
				}
			}
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
			return forwardErr
		}

		usage := extractRawUsage(resp.Body, estimateRawTokens(upstreamBody))
		logInput := usageLogInput{
			UserID:           plan.Auth.UserID,
			TokenID:          plan.Auth.TokenID,
			TokenName:        plan.Auth.TokenName,
			RequestID:        requestID,
			Endpoint:         upstreamPath,
			ModelName:        clientModel,
			Quota:            usage.TotalTokens,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			CacheReadTokens:  usage.CacheReadTokens,
			ChannelID:        ch.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         false,
		}
		if err := s.commitQuota(ctx, reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
			return err
		}
		logUpstreamUsage(logInput)
		s.ingestUsageLog(ctx, logInput)
		upstreamResp = resp
		responseChannel = ch
		return nil
	})

	if result.Err != nil {
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
		return
	}
	if upstreamResp == nil || responseChannel == nil {
		s.writeError(w, http.StatusBadGateway, "upstream service error")
		return
	}

	if s.wsScheduler != nil {
		s.wsScheduler.BindSession(r.Context(), &relaybiz.RelayPlan{
			Auth:          plan.Auth,
			Channel:       responseChannel,
			ResolvedModel: plan.ResolvedModel,
			Account:       plan.Account,
		}, sessionHash)
	}
	if upstreamResp.Body == nil {
		return
	}
	if responseID := extractResponseID(upstreamResp.Body); responseID != "" {
		s.storeResponseRoute(responseID, responseRoute{Model: clientModel, ResolvedModel: plan.ResolvedModel, Channel: *responseChannel, UserID: plan.Auth.UserID, SubscriptionAccountID: subscriptionAccountIDFromPlan(plan)})
	}
	writeRawResponse(w, upstreamResp)
}

func (s *HTTPServer) handleResponsesResource(w http.ResponseWriter, r *http.Request, upstreamPath, responseID string) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	route, ok := s.lookupResponseRoute(responseID)
	if !ok {
		s.writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": map[string]interface{}{
				"message": "response route not found",
				"type":    "invalid_request_error",
				"param":   "response_id",
				"code":    "response_not_found",
			},
		})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	s.forwardResponsesToStoredRoute(w, r, upstreamPath, body, token, route, false)
}

func (s *HTTPServer) forwardResponsesToStoredRoute(w http.ResponseWriter, r *http.Request, upstreamPath string, body []byte, token string, route responseRoute, stream bool) {
	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if route.UserID != 0 && route.UserID != authSnapshot.UserId {
		s.writeJSON(w, http.StatusNotFound, map[string]interface{}{
			"error": map[string]interface{}{
				"message": "response route not found",
				"type":    "invalid_request_error",
				"param":   "response_id",
				"code":    "response_not_found",
			},
		})
		return
	}

	requestID := generateRequestID()
	resolvedModel := routeResolvedModel(route)
	fallbackBody := ensureRawModel(body, resolvedModel)
	reservation, err := s.reserveQuota(
		r.Context(),
		fmt.Sprintf("%d", authSnapshot.UserId),
		requestID,
		estimateRawTokens(body),
		route.Model,
		fmt.Sprintf("%d", route.Channel.ID),
		route.SubscriptionAccountID,
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	startedAt := time.Now()
	if stream {
		streamResp, err := s.forwardResponsesRawStream(r.Context(), &route.Channel, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
		if err != nil {
			if shouldFallbackResponsesToChat(upstreamPath, err) {
				fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(r.Context(), &route.Channel, r.Header.Clone(), fallbackBody)
				if fallbackErr == nil && fallbackResp.Stream != nil {
					usage := newRawStreamUsageTracker(estimateRawUsage(fallbackBody))
					writeRawStreamResponse(w, fallbackResp.Stream, usage)
					actualUsage := usage.Usage()
					logInput := usageLogInput{
						UserID:           authSnapshot.UserId,
						TokenID:          authSnapshot.TokenId,
						TokenName:        authSnapshot.TokenName,
						RequestID:        requestID,
						Endpoint:         "/chat/completions",
						ModelName:        route.Model,
						Quota:            actualUsage.TotalTokens,
						PromptTokens:     actualUsage.PromptTokens,
						CompletionTokens: actualUsage.CompletionTokens,
						CacheReadTokens:  actualUsage.CacheReadTokens,
						ChannelID:        route.Channel.ID,
						ElapsedTime:      time.Since(startedAt).Milliseconds(),
						IsStream:         true,
					}
					if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
						s.logPostResponseCommitError(err)
					} else {
						s.ingestUsageLogAfterResponse(logInput)
					}
					if responseID := usage.ResponseID(); responseID != "" {
						route.UserID = authSnapshot.UserId
						route.ResolvedModel = resolvedModel
						s.storeResponseRoute(responseID, route)
					}
					return
				}
			}
			_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
			s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
			return
		}
		usage := newRawStreamUsageTracker(estimateRawUsage(body))
		writeRawStreamResponse(w, streamResp, usage)
		actualUsage := usage.Usage()
		logInput := usageLogInput{
			UserID:           authSnapshot.UserId,
			TokenID:          authSnapshot.TokenId,
			TokenName:        authSnapshot.TokenName,
			RequestID:        requestID,
			Endpoint:         upstreamPath,
			ModelName:        route.Model,
			Quota:            actualUsage.TotalTokens,
			PromptTokens:     actualUsage.PromptTokens,
			CompletionTokens: actualUsage.CompletionTokens,
			CacheReadTokens:  actualUsage.CacheReadTokens,
			ChannelID:        route.Channel.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         true,
		}
		if err := s.commitQuotaAfterResponse(reservation.ReservationId, actualUsage.TotalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			s.ingestUsageLogAfterResponse(logInput)
		}
		if responseID := usage.ResponseID(); responseID != "" {
			route.UserID = authSnapshot.UserId
			s.storeResponseRoute(responseID, route)
		}
		return
	}

	resp, err := s.forwardResponsesRaw(r.Context(), &route.Channel, r.Method, upstreamPath, r.URL.RawQuery, r.Header.Clone(), body)
	if err != nil {
		if shouldFallbackResponsesToChat(upstreamPath, err) {
			fallbackResp, fallbackErr := s.forwardResponsesViaChatFallback(r.Context(), &route.Channel, r.Header.Clone(), fallbackBody)
			if fallbackErr == nil && fallbackResp.Response != nil {
				usage := fallbackResp.Usage
				if usage.TotalTokens <= 0 {
					usage = extractRawUsage(fallbackResp.Response.Body, estimateRawTokens(fallbackBody))
				}
				logInput := usageLogInput{
					UserID:           authSnapshot.UserId,
					TokenID:          authSnapshot.TokenId,
					TokenName:        authSnapshot.TokenName,
					RequestID:        requestID,
					Endpoint:         "/chat/completions",
					ModelName:        route.Model,
					Quota:            usage.TotalTokens,
					PromptTokens:     usage.PromptTokens,
					CompletionTokens: usage.CompletionTokens,
					CacheReadTokens:  usage.CacheReadTokens,
					ChannelID:        route.Channel.ID,
					ElapsedTime:      time.Since(startedAt).Milliseconds(),
					IsStream:         false,
				}
				if err := s.commitQuota(r.Context(), reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
					s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
					return
				}
				s.ingestUsageLog(r.Context(), logInput)
				if responseID := extractResponseID(fallbackResp.Response.Body); responseID != "" {
					route.UserID = authSnapshot.UserId
					route.ResolvedModel = resolvedModel
					s.storeResponseRoute(responseID, route)
				}
				writeRawResponse(w, fallbackResp.Response)
				return
			}
		}
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
		return
	}

	usage := extractRawUsage(resp.Body, estimateRawTokens(body))
	logInput := usageLogInput{
		UserID:           authSnapshot.UserId,
		TokenID:          authSnapshot.TokenId,
		TokenName:        authSnapshot.TokenName,
		RequestID:        requestID,
		Endpoint:         upstreamPath,
		ModelName:        route.Model,
		Quota:            usage.TotalTokens,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		CacheReadTokens:  usage.CacheReadTokens,
		ChannelID:        route.Channel.ID,
		ElapsedTime:      time.Since(startedAt).Milliseconds(),
		IsStream:         false,
	}
	if err := s.commitQuota(r.Context(), reservation.ReservationId, usage.TotalTokens, true, logInput); err != nil {
		s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
		return
	}
	s.ingestUsageLog(r.Context(), logInput)
	if responseID := extractResponseID(resp.Body); responseID != "" {
		route.UserID = authSnapshot.UserId
		s.storeResponseRoute(responseID, route)
	}
	writeRawResponse(w, resp)
}

func (s *HTTPServer) forwardResponsesRaw(ctx context.Context, ch *relaybiz.Channel, method, path, query string, header http.Header, body []byte) (*relayprovider.RawResponse, error) {
	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
		APIVersion: ch.Config.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}
	return provider.Forward(ctx, &relayprovider.RawRequest{
		Method: method,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   body,
	})
}

func (s *HTTPServer) forwardResponsesRawStream(ctx context.Context, ch *relaybiz.Channel, method, path, query string, header http.Header, body []byte) (*relayprovider.RawStreamResponse, error) {
	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
		APIVersion: ch.Config.APIVersion,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create provider: %w", err)
	}
	return provider.ForwardStream(ctx, &relayprovider.RawRequest{
		Method: method,
		Path:   path,
		Query:  query,
		Header: header,
		Body:   body,
	})
}

func (s *HTTPServer) storeResponseRoute(responseID string, route responseRoute) {
	if responseID == "" {
		return
	}
	now := time.Now()
	s.responsesMu.Lock()
	defer s.responsesMu.Unlock()
	s.responseRoutes[responseID] = responseRouteEntry{route: route, expiresAt: now.Add(responseRouteTTL)}
	// Opportunistically evict expired entries so the map is bounded by live TTL
	// traffic rather than growing for the process lifetime.
	if now.Sub(s.responsesLastSweep) >= responseRouteSweepInterval {
		s.responsesLastSweep = now
		for id, entry := range s.responseRoutes {
			if now.After(entry.expiresAt) {
				delete(s.responseRoutes, id)
			}
		}
	}
}

func (s *HTTPServer) lookupResponseRoute(responseID string) (responseRoute, bool) {
	if responseID == "" {
		return responseRoute{}, false
	}
	s.responsesMu.RLock()
	entry, ok := s.responseRoutes[responseID]
	s.responsesMu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return responseRoute{}, false
	}
	return entry.route, true
}

func providerConfigFromChannelInfo(channel *commonv1.ChannelInfo) relayprovider.ProviderConfig {
	if channel == nil || channel.Config == nil {
		return relayprovider.ProviderConfig{}
	}
	return relayprovider.ProviderConfig{APIVersion: channel.Config.ApiVersion}
}

func (s *HTTPServer) handleUnsupportedOpenAIRoute(feature string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.writeJSON(w, http.StatusNotImplemented, map[string]interface{}{
			"error": map[string]interface{}{
				"message": fmt.Sprintf("%s is not implemented", feature),
				"type":    "one_api_not_implemented",
				"param":   nil,
				"code":    "not_implemented",
			},
		})
	}
}

func (s *HTTPServer) handleOneAPIProxy(w http.ResponseWriter, r *http.Request) {
	const prefix = "/v1/oneapi/proxy/"

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, prefix)
	channelPart, targetPart, ok := strings.Cut(rest, "/")
	if !ok || channelPart == "" || targetPart == "" {
		s.writeError(w, http.StatusBadRequest, "invalid proxy path")
		return
	}
	channelID, err := parsePositiveInt64(channelPart)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}

	channelReply, err := s.channelClient.GetChannel(r.Context(), &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil {
		s.handleChannelError(w, err)
		return
	}
	if channelReply.Channel == nil {
		s.writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 64*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	model := extractRawModel(body)
	if model == "" {
		model = "proxy"
	}

	requestID := generateRequestID()
	startedAt := time.Now()
	reservation, err := s.reserveQuota(
		r.Context(),
		fmt.Sprintf("%d", authSnapshot.UserId),
		requestID,
		estimateRawTokens(body),
		model,
		fmt.Sprintf("%d", channelReply.Channel.Id),
		0,
	)
	if err != nil {
		s.writeError(w, http.StatusPaymentRequired, "quota reservation failed")
		return
	}

	provider, err := s.providerFactory.CreateProviderWithConfig(channelReply.Channel.Type, channelReply.Channel.BaseUrl, channelReply.Channel.Key, providerConfigFromChannelInfo(channelReply.Channel))
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "failed to create provider")
		s.writeError(w, http.StatusInternalServerError, "failed to create provider")
		return
	}

	resp, err := provider.Forward(r.Context(), &relayprovider.RawRequest{
		Method: r.Method,
		Path:   "/" + targetPart,
		Query:  r.URL.RawQuery,
		Header: r.Header.Clone(),
		Body:   body,
	})
	if err != nil {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream error")
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(err)), "upstream service error")
		return
	}

	totalTokens := extractTotalTokens(resp.Body, estimateRawTokens(body))
	usage := extractRawUsage(resp.Body, totalTokens)
	if err := s.commitQuota(r.Context(), reservation.ReservationId, totalTokens, true, usageLogInput{
		UserID:                authSnapshot.UserId,
		TokenID:               authSnapshot.TokenId,
		TokenName:             authSnapshot.TokenName,
		RequestID:             requestID,
		Endpoint:              "/" + targetPart,
		ModelName:             model,
		Quota:                 totalTokens,
		PromptTokens:          usage.PromptTokens,
		CompletionTokens:      usage.CompletionTokens,
		CacheReadTokens:       usage.CacheReadTokens,
		ChannelID:             channelReply.Channel.Id,
		SubscriptionAccountID: 0,
		ElapsedTime:           time.Since(startedAt).Milliseconds(),
		IsStream:              false,
	}); err != nil {
		s.writeError(w, http.StatusPaymentRequired, "billing commit failed")
		return
	}
	writeRawResponse(w, resp)
}

func (s *HTTPServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	// Read the original body so session_hash (which the typed struct does not
	// carry) survives for session stickiness; then decode from those bytes.
	originalBody, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024))
	if err != nil {
		s.writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	var req relayprovider.ChatCompletionsRequest
	if err := sonic.Unmarshal(originalBody, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Model == "" {
		s.writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	sessionHash := ""
	if s.subscriptionSessionStickyEnabled {
		sessionHash = extractSessionHashFromRequest(r, originalBody)
	}

	// Delegate auth, model validation, model mapping, and channel selection to biz layer
	plan, err := s.relayUsecase.Plan(r.Context(), relaybiz.RelayRequest{
		Token:       token,
		Model:       req.Model,
		SessionHash: sessionHash,
	})
	if err != nil {
		s.handleRelayPlanError(w, err)
		return
	}

	// Subscription-account channels (Codex/Claude OAuth) are routed through the
	// hybrid adaptor layer when the feature flag is on. The adaptor owns the
	// full upstream interaction (protocol conversion, identity mimicry, OAuth
	// token, stream bridging). API-key channels fall through to the existing
	// provider-factory path below.
	if s.hybridAdaptorEnabled && plan.Channel != nil && isSubscriptionChannel(plan.Channel.Type) {
		// req.Model still holds the client-facing model name at this point (it is
		// reassigned to the resolved model only further below). Reconstruct the raw
		// body from the decoded request since the original body was consumed.
		rawBody, _ := sonic.Marshal(req)
		s.handleChatCompletionsViaAdaptor(w, r, plan, req.Model, rawBody, sessionHash)
		return
	}

	clientModel := req.Model

	// Use resolved model name for upstream calls
	req.Model = plan.ResolvedModel

	// Use RetryExecutor for upstream calls with channel fallback
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	result := retryExecutor.ExecuteWithInitialChannel(r.Context(), plan.Auth.Group, plan.ResolvedModel, plan.Channel, func(ctx context.Context, ch *relaybiz.Channel) error {
		startedAt := time.Now()
		// Reserve quota
		requestID := generateRequestID()
		estimatedTokens := s.estimateTokens(&req)
		reservation, reserveErr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimatedTokens, plan.ResolvedModel, fmt.Sprintf("%d", ch.ID), subscriptionAccountIDFromPlan(plan))
		if reserveErr != nil {
			return &relaybiz.RetryableError{Status: http.StatusPaymentRequired, Err: reserveErr}
		}

		provider, provErr := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{
			APIVersion: ch.Config.APIVersion,
		})
		if provErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "failed to create provider")
			return fmt.Errorf("failed to create provider: %w", provErr)
		}

		if req.Stream {
			return s.handleStreamingResponse(w, r, provider, &req, reservation, usageLogInput{
				UserID:                plan.Auth.UserID,
				TokenID:               plan.Auth.TokenID,
				TokenName:             plan.Auth.TokenName,
				RequestID:             requestID,
				Endpoint:              "/v1/chat/completions",
				ModelName:             clientModel,
				ChannelID:             ch.ID,
				SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
				IsStream:              true,
			})
		}

		// Non-streaming call
		resp, callErr := provider.ChatCompletions(ctx, &req)
		if callErr != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream error")
			return callErr
		}

		// Success — commit quota and return
		actualTokens := s.calculateActualTokens(resp)
		logInput := usageLogInput{
			UserID:           plan.Auth.UserID,
			TokenID:          plan.Auth.TokenID,
			TokenName:        plan.Auth.TokenName,
			RequestID:        requestID,
			Endpoint:         "/v1/chat/completions",
			ModelName:        clientModel,
			Quota:            actualTokens,
			PromptTokens:     int64(resp.Usage.PromptTokens),
			CompletionTokens: int64(resp.Usage.CompletionTokens),
			CacheReadTokens:  cacheReadTokensFromProviderUsage(resp.Usage),
			ChannelID:        ch.ID,
			ElapsedTime:      time.Since(startedAt).Milliseconds(),
			IsStream:         false,
		}
		if err := s.commitQuota(ctx, reservation.ReservationId, actualTokens, true, logInput); err != nil {
			return err
		}
		logUpstreamUsage(logInput)
		s.ingestUsageLog(ctx, logInput)
		s.writeJSON(w, http.StatusOK, resp)
		return nil
	})

	if result.Err != nil {
		s.writeError(w, mapUpstreamError(relaybiz.UpstreamStatus(result.Err)), "upstream service error")
	}
}

func (s *HTTPServer) handleStreamingResponse(w http.ResponseWriter, r *http.Request, provider relayprovider.Provider, req *relayprovider.ChatCompletionsRequest, reservation *billingv1.ReserveQuotaResponse, logInput usageLogInput) error {
	startedAt := time.Now()
	chunkChan, err := provider.ChatCompletionsStream(r.Context(), req)
	if err != nil {
		// 流式请求失败，释放预扣配额
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "upstream stream error")
		return err
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		// 流式不支持，释放预扣配额
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "streaming not supported")
		return stderrors.New("streaming not supported")
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Transfer-Encoding", "chunked")

	totalTokens := int64(0)
	promptTokens := int64(0)
	completionTokens := int64(0)
	cacheReadTokens := int64(0)
	estimatedTokens := int64(0)
	streamError := false

	for chunk := range chunkChan {
		if chunk.Usage.TotalTokens > 0 {
			totalTokens = int64(chunk.Usage.TotalTokens)
			promptTokens = int64(chunk.Usage.PromptTokens)
			completionTokens = int64(chunk.Usage.CompletionTokens)
			cacheReadTokens = cacheReadTokensFromProviderUsage(chunk.Usage)
		}
		for _, choice := range chunk.Choices {
			estimatedTokens += int64(len(choice.Delta.Content) / 4)
		}

		jsonData, err := sonic.Marshal(chunk)
		if err != nil {
			if applogger.Log != nil {
				applogger.Log.Warn("failed to marshal chunk", zap.Error(err))
			}
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", string(jsonData))
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	// 流式请求完成，提交配额
	if !streamError {
		if totalTokens == 0 {
			totalTokens = estimatedTokens
			completionTokens = estimatedTokens
		}
		logInput.Quota = totalTokens
		logInput.PromptTokens = promptTokens
		logInput.CompletionTokens = completionTokens
		logInput.CacheReadTokens = cacheReadTokens
		logInput.ElapsedTime = time.Since(startedAt).Milliseconds()
		if logInput.Endpoint == "" {
			logInput.Endpoint = "/v1/chat/completions"
		}
		if err := s.commitQuotaAfterResponse(reservation.ReservationId, totalTokens, true, logInput); err != nil {
			s.logPostResponseCommitError(err)
		} else {
			logUpstreamUsage(logInput)
			s.ingestUsageLogAfterResponse(logInput)
		}
	} else {
		_ = s.releaseQuota(r.Context(), reservation.ReservationId, "stream error")
	}
	return nil
}

type usageLogInput struct {
	UserID                int64
	TokenID               int64
	TokenName             string
	RequestID             string
	Endpoint              string
	ModelName             string
	Quota                 int64
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	ChannelID             int64
	SubscriptionAccountID int64
	ElapsedTime           int64
	IsStream              bool
}

func (s *HTTPServer) ingestUsageLog(ctx context.Context, in usageLogInput) {
	if s.logClient == nil {
		metrics.UsageLogIngestTotal.WithLabelValues("skipped").Inc()
		return
	}
	message := applogger.Sanitize(fmt.Sprintf("model=%s quota=%d prompt_tokens=%d completion_tokens=%d cache_read_tokens=%d channel=%d", in.ModelName, in.Quota, in.PromptTokens, in.CompletionTokens, in.CacheReadTokens, in.ChannelID))
	_, err := s.logClient.IngestLog(ctx, &logv1.IngestLogRequest{
		Level:                 "consume",
		Message:               message,
		Source:                "relay-gateway",
		RequestId:             in.RequestID,
		UserId:                in.UserID,
		TokenName:             usageTokenName(in),
		ModelName:             in.ModelName,
		Quota:                 in.Quota,
		PromptTokens:          in.PromptTokens,
		CompletionTokens:      in.CompletionTokens,
		CacheReadTokens:       in.CacheReadTokens,
		ChannelId:             in.ChannelID,
		SubscriptionAccountId: in.SubscriptionAccountID,
		ElapsedTime:           in.ElapsedTime,
		IsStream:              in.IsStream,
	})
	if err != nil && applogger.Log != nil {
		metrics.UsageLogIngestTotal.WithLabelValues("error").Inc()
		applogger.Log.Warn("failed to ingest usage log", zap.Error(err))
		return
	}
	metrics.UsageLogIngestTotal.WithLabelValues("success").Inc()
}

func logUpstreamUsage(in usageLogInput) {
	cacheRatio := float64(0)
	if in.PromptTokens > 0 {
		cacheRatio = float64(in.CacheReadTokens) / float64(in.PromptTokens)
	}
	nonCachedInputTokens := in.PromptTokens
	if in.CacheReadTokens > 0 {
		nonCachedInputTokens = in.PromptTokens - in.CacheReadTokens
		if nonCachedInputTokens < 0 {
			nonCachedInputTokens = 0
		}
	}
	applogger.Log.Info("upstream usage reported",
		zap.String("request_id", in.RequestID),
		zap.String("endpoint", in.Endpoint),
		zap.String("model", in.ModelName),
		zap.Int64("user_id", in.UserID),
		zap.Int64("channel_id", in.ChannelID),
		zap.Bool("is_stream", in.IsStream),
		zap.Int64("total_tokens", in.Quota),
		zap.Int64("upstream_input_tokens", in.PromptTokens),
		zap.Int64("input_tokens", nonCachedInputTokens),
		zap.Int64("output_tokens", in.CompletionTokens),
		zap.Int64("cache_read_tokens", in.CacheReadTokens),
		zap.Float64("cache_read_input_ratio", cacheRatio),
	)
}

func postResponseContext() (context.Context, context.CancelFunc) {
	return detachedBillingContext(context.Background())
}

func detachedBillingContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), postResponseWriteTimeout)
}

func (s *HTTPServer) commitQuotaAfterResponse(reservationID string, actualTokens int64, success bool, details ...usageLogInput) error {
	ctx, cancel := postResponseContext()
	defer cancel()
	return s.commitQuota(ctx, reservationID, actualTokens, success, details...)
}

func (s *HTTPServer) ingestUsageLogAfterResponse(in usageLogInput) {
	ctx, cancel := postResponseContext()
	defer cancel()
	s.ingestUsageLog(ctx, in)
}

func (s *HTTPServer) logPostResponseCommitError(err error) {
	if err != nil && applogger.Log != nil {
		applogger.Log.Warn("failed to commit quota after response was written", zap.Error(err))
	}
}

func (s *HTTPServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}

	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}

	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}

	modelsReply, err := s.listAvailableModels(r.Context(), authSnapshot.Group)
	if err != nil {
		s.handleChannelError(w, err)
		return
	}

	models := s.applyModelWhitelist(modelsReply.Models, authSnapshot.AllowedModels)

	response := struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}{
		Object: "list",
	}

	for _, model := range models {
		response.Data = append(response.Data, struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		}{
			ID:      model,
			Object:  "model",
			Created: 0,
			OwnedBy: "organization",
		})
	}

	s.writeJSON(w, http.StatusOK, response)
}

func (s *HTTPServer) handleUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		s.writeError(w, http.StatusUnauthorized, "missing authorization header")
		return
	}
	if !strings.HasPrefix(authHeader, "Bearer ") {
		s.writeError(w, http.StatusUnauthorized, "invalid authorization header format")
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
	if token == "" {
		s.writeError(w, http.StatusUnauthorized, "missing token")
		return
	}

	authSnapshot, err := s.getAuthSnapshot(r.Context(), token)
	if err != nil {
		s.handleIdentityError(w, err)
		return
	}
	if s.billingClient == nil {
		s.writeError(w, http.StatusServiceUnavailable, "billing service unavailable")
		return
	}
	resp, err := s.billingClient.GetAccountSnapshot(r.Context(), &billingv1.GetAccountSnapshotRequest{
		UserId: strconv.FormatInt(authSnapshot.UserId, 10),
	})
	if err != nil {
		s.writeError(w, http.StatusBadGateway, "billing service error")
		return
	}
	account := resp.GetSnapshot()
	if account == nil {
		s.writeError(w, http.StatusBadGateway, "billing account not found")
		return
	}

	remaining := account.GetBalance()
	used := account.GetUsedAmount()
	frozen := account.GetFrozenAmount()
	remainingUSD := amountUnitsToUSD(remaining)
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"mode":      "unrestricted",
		"isValid":   true,
		"is_active": true,
		"status":    "active",
		"user_id":   account.GetUserId(),
		"planName":  "钱包余额",
		"remaining": remainingUSD,
		"balance":   remainingUSD,
		"unit":      "USD",
		"quota": map[string]interface{}{
			"remaining": remaining,
			"used":      used,
			"frozen":    frozen,
			"unit":      "quota",
			"per_usd":   amountUnitsPerUSD,
		},
		"usage": map[string]interface{}{
			"total": map[string]interface{}{
				"cost":     used,
				"requests": account.GetRequestCount(),
			},
		},
	})
}

func amountUnitsToUSD(amount int64) float64 {
	return float64(amount) / float64(amountUnitsPerUSD)
}

func quotaPerUSDFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv("PAYMENT_QUOTA_PER_UNIT"))
	if raw == "" {
		return defaultQuotaPerUSD
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return defaultQuotaPerUSD
	}
	return value
}

func (s *HTTPServer) handleRetrieveModel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	const prefix = "/v1/models/"
	modelID := strings.TrimPrefix(r.URL.Path, prefix)
	if modelID == "" || strings.Contains(modelID, "/") {
		s.writeError(w, http.StatusNotFound, "model not found")
		return
	}

	s.writeJSON(w, http.StatusOK, openAIModelResponse(modelID))
}

func (s *HTTPServer) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "",
		"data": map[string]interface{}{
			"version":              "micro-one-api",
			"system_name":          "micro-one-api",
			"email_verification":   false,
			"github_oauth":         false,
			"wechat_login":         false,
			"turnstile_check":      false,
			"display_in_currency":  false,
			"registration_enabled": true,
		},
	})
}

func (s *HTTPServer) handleDashboardModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success":  true,
		"message":  "",
		"data":     oneAPIChannelModelsByType(),
		"metadata": oneAPIProviderCatalogMetadata(),
	})
}

func (s *HTTPServer) handleGroups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	group := r.URL.Query().Get("group")
	if group == "" {
		group = "default"
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"message": "",
		"data":    []string{group},
	})
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func openAIModelResponse(modelID string) map[string]interface{} {
	permissionID := "modelperm-micro-one-api"
	return map[string]interface{}{
		"id":       modelID,
		"object":   "model",
		"created":  1626777600,
		"owned_by": "organization",
		"permission": []map[string]interface{}{
			{
				"id":                   permissionID,
				"object":               "model_permission",
				"created":              1626777600,
				"allow_create_engine":  true,
				"allow_sampling":       true,
				"allow_logprobs":       true,
				"allow_search_indices": false,
				"allow_view":           true,
				"allow_fine_tuning":    false,
				"organization":         "*",
				"group":                nil,
				"is_blocking":          false,
			},
		},
		"root":   modelID,
		"parent": nil,
	}
}

func (s *HTTPServer) getAuthSnapshot(ctx context.Context, token string) (*identityv1.GetAuthSnapshotReply, error) {
	req := &identityv1.GetAuthSnapshotRequest{
		Token: token,
	}
	return s.identityClient.GetAuthSnapshot(ctx, req)
}

func (s *HTTPServer) listAvailableModels(ctx context.Context, group string) (*channelv1.ListAvailableModelsReply, error) {
	req := &channelv1.ListAvailableModelsRequest{
		Group: group,
	}
	return s.channelClient.ListAvailableModels(ctx, req)
}

func (s *HTTPServer) applyModelWhitelist(availableModels []string, allowedModels []string) []string {
	if len(allowedModels) == 0 {
		return availableModels
	}

	allowedSet := make(map[string]bool)
	for _, model := range allowedModels {
		allowedSet[model] = true
	}

	filtered := make([]string, 0, len(availableModels))
	for _, model := range availableModels {
		if allowedSet[model] {
			filtered = append(filtered, model)
		}
	}

	return filtered
}

// handleRelayPlanError maps biz-layer Plan() errors to HTTP responses.
func (s *HTTPServer) handleRelayPlanError(w http.ResponseWriter, err error) {
	// Check for structured errors
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, "no available channel")
		return
	}

	// Handle gRPC errors from downstream services
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, "unauthorized")
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, "forbidden")
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		case codes.Unavailable:
			s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		default:
			if strings.Contains(st.Message(), "no available channel") || strings.Contains(st.Message(), "channel not found") {
				s.writeError(w, http.StatusServiceUnavailable, "no available channel")
				return
			}
			s.writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	if strings.Contains(err.Error(), "no available channel") || strings.Contains(err.Error(), "channel not found") {
		s.writeError(w, http.StatusServiceUnavailable, "no available channel")
		return
	}

	// Model not allowed (string match from biz layer)
	if strings.Contains(err.Error(), "not allowed") {
		s.writeError(w, http.StatusForbidden, "model not allowed")
		return
	}

	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) handleIdentityError(w http.ResponseWriter, err error) {
	// Check for structured errors first
	if errors.IsUnauthorized(err) {
		s.writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if errors.IsForbidden(err) {
		s.writeError(w, http.StatusForbidden, "forbidden")
		return
	}

	// Handle gRPC errors
	st, ok := status.FromError(err)
	if ok {
		switch st.Code() {
		case codes.NotFound:
			s.writeError(w, http.StatusUnauthorized, "unauthorized")
		case codes.PermissionDenied:
			s.writeError(w, http.StatusForbidden, "forbidden")
		case codes.ResourceExhausted:
			s.writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		default:
			s.writeError(w, http.StatusInternalServerError, "internal server error")
		}
		return
	}

	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) handleChannelError(w http.ResponseWriter, err error) {
	if errors.IsServiceUnavailable(err) {
		s.writeError(w, http.StatusServiceUnavailable, "service unavailable")
		return
	}
	s.writeError(w, http.StatusInternalServerError, "internal server error")
}

func (s *HTTPServer) writeError(w http.ResponseWriter, statusCode int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	encodeJSON(w, map[string]interface{}{
		"error": map[string]interface{}{
			"message": message,
		},
	})
}

func (s *HTTPServer) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	encodeJSON(w, data)
}

// 配额管理方法

func (s *HTTPServer) reserveQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string, subscriptionAccountID int64) (*billingv1.ReserveQuotaResponse, error) {
	req := &billingv1.ReserveQuotaRequest{
		UserId:                userID,
		RequestId:             requestID,
		EstimatedTokens:       estimatedTokens,
		Model:                 model,
		ChannelId:             channelID,
		SubscriptionAccountId: subscriptionAccountID,
	}
	resp, err := s.billingClient.ReserveQuota(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp == nil || !resp.GetSuccess() {
		return resp, stderrors.New(billingErrorMessage(resp, "reserve quota failed"))
	}
	return resp, nil
}

func (s *HTTPServer) commitQuota(ctx context.Context, reservationID string, actualTokens int64, success bool, details ...usageLogInput) error {
	req := &billingv1.CommitQuotaRequest{
		ReservationId: reservationID,
		ActualTokens:  actualTokens,
		Success:       success,
	}
	if len(details) > 0 {
		detail := details[0]
		req.TokenName = usageTokenName(detail)
		req.Endpoint = detail.Endpoint
		req.PromptTokens = detail.PromptTokens
		req.CompletionTokens = detail.CompletionTokens
		req.CacheReadTokens = detail.CacheReadTokens
		req.ElapsedTime = detail.ElapsedTime
		req.IsStream = detail.IsStream
		req.SubscriptionAccountId = detail.SubscriptionAccountID
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.billingClient.CommitQuota(billingCtx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.GetSuccess() {
		return stderrors.New(billingErrorMessage(resp, "commit quota failed"))
	}
	if len(details) > 0 {
		detail := details[0]
		s.recordChannelUsage(ctx, detail.ChannelID, actualTokens)
		s.recordSubscriptionUsage(ctx, detail.UserID, actualTokens)
	}
	return nil
}

func (s *HTTPServer) recordSubscriptionUsage(ctx context.Context, userID int64, quota int64) {
	if s.subscriptionUsecase == nil || userID <= 0 || quota <= 0 {
		metrics.SubscriptionUsageRecordsTotal.WithLabelValues("skipped").Inc()
		return
	}
	costUSD := quotaToUSD(quota)
	if costUSD <= 0 {
		metrics.SubscriptionUsageRecordsTotal.WithLabelValues("skipped").Inc()
		return
	}
	subscriptionCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	if err := s.subscriptionUsecase.RecordUsage(subscriptionCtx, userID, costUSD); err != nil {
		metrics.SubscriptionUsageRecordsTotal.WithLabelValues("error").Inc()
		if applogger.Log != nil {
			applogger.Log.Warn("failed to record subscription usage", zap.Int64("user_id", userID), zap.Float64("cost_usd", costUSD), zap.Error(err))
		}
		return
	}
	metrics.SubscriptionUsageRecordsTotal.WithLabelValues("success").Inc()
}

func quotaToUSD(quota int64) float64 {
	if quota <= 0 {
		return 0
	}
	perUSD := quotaPerUSDFromEnv()
	if perUSD <= 0 {
		perUSD = defaultQuotaPerUSD
	}
	return float64(quota) / float64(perUSD)
}

func (s *HTTPServer) recordChannelUsage(ctx context.Context, channelID int64, quota int64) {
	if s.channelClient == nil || channelID <= 0 || quota <= 0 {
		return
	}
	channelCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.channelClient.RecordChannelUsage(channelCtx, &channelv1.RecordChannelUsageRequest{
		ChannelId: channelID,
		Quota:     quota,
	})
	if err != nil && applogger.Log != nil {
		applogger.Log.Warn("failed to record channel usage", zap.Int64("channel_id", channelID), zap.Int64("quota", quota), zap.Error(err))
		return
	}
	if resp != nil && !resp.GetSuccess() && applogger.Log != nil {
		applogger.Log.Warn("failed to record channel usage", zap.Int64("channel_id", channelID), zap.Int64("quota", quota), zap.String("message", resp.GetMessage()))
	}
}

func usageTokenName(in usageLogInput) string {
	if strings.TrimSpace(in.TokenName) != "" {
		return strings.TrimSpace(in.TokenName)
	}
	return fmt.Sprintf("token-%d", in.TokenID)
}

func (s *HTTPServer) releaseQuota(ctx context.Context, reservationID, reason string) error {
	req := &billingv1.ReleaseQuotaRequest{
		ReservationId: reservationID,
		Reason:        reason,
	}
	billingCtx, cancel := detachedBillingContext(ctx)
	defer cancel()
	resp, err := s.billingClient.ReleaseQuota(billingCtx, req)
	if err != nil {
		return err
	}
	if resp == nil || !resp.GetSuccess() {
		return stderrors.New(billingErrorMessage(resp, "release quota failed"))
	}
	return nil
}

type billingFailure interface {
	GetErrorMessage() string
}

func billingErrorMessage(resp billingFailure, fallback string) string {
	if resp == nil {
		return fallback
	}
	if msg := strings.TrimSpace(resp.GetErrorMessage()); msg != "" {
		return msg
	}
	return fallback
}

func (s *HTTPServer) estimateTokens(req *relayprovider.ChatCompletionsRequest) int64 {
	// 简单的 token 估算逻辑
	// 实际应用中可以使用更精确的 tokenizer
	tokens := int64(0)

	// 估算输入 tokens
	for _, msg := range req.Messages {
		tokens += int64(len(msg.Content) / 4) // 假设平均每个 token 4 个字符
	}

	// 估算输出 tokens (基于 max_tokens 或默认值)
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		tokens += int64(*req.MaxTokens)
	} else {
		tokens += 1000 // 默认输出 tokens
	}

	return tokens
}

func (s *HTTPServer) calculateActualTokens(resp *relayprovider.ChatCompletionsResponse) int64 {
	// resp.Usage 不是指针，是值类型
	return int64(resp.Usage.TotalTokens)
}

func cacheReadTokensFromProviderUsage(usage relayprovider.Usage) int64 {
	for _, value := range []int{
		usage.PromptTokensDetails.CacheReadTokens,
		usage.PromptTokensDetails.CachedTokens,
		usage.InputTokensDetails.CacheReadTokens,
		usage.InputTokensDetails.CachedTokens,
	} {
		if value > 0 {
			return int64(value)
		}
	}
	return 0
}
