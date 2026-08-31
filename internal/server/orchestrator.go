package server

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"micro-one-api/pkg/jsonx"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/internal/server/forwarder"
	apperrors "micro-one-api/pkg/errors"
)

// APIEndpoint represents a specific API endpoint type.
type APIEndpoint string

const (
	EndpointChatCompletions   APIEndpoint = "chat/completions"
	EndpointCompletions       APIEndpoint = "completions"
	EndpointEmbeddings        APIEndpoint = "embeddings"
	EndpointImagesGenerations APIEndpoint = "images/generations"
	EndpointAudioTranscribe   APIEndpoint = "audio/transcriptions"
	EndpointAudioTranslate    APIEndpoint = "audio/translations"
	EndpointAudioSpeech       APIEndpoint = "audio/speech"
	EndpointModerations       APIEndpoint = "moderations"
	EndpointResponses         APIEndpoint = "responses"
	EndpointAnthropicMessages APIEndpoint = "anthropic/messages"
	EndpointModels            APIEndpoint = "models"
	EndpointUsage             APIEndpoint = "usage"
)

// Orchestrator coordinates the complete relay request lifecycle:
// auth → model mapping → channel selection → reserve → forward → commit → log
type Orchestrator interface {
	// Execute runs the complete relay pipeline for a request.
	Execute(ctx context.Context, req *RelayRequest) (*RelayResult, error)
}

// RelayRequest is the normalized input for orchestration.
type RelayRequest struct {
	// Token is the Bearer token from Authorization header.
	Token string
	// Model is the model name requested by the client.
	Model string
	// Endpoint specifies which API endpoint is being called.
	Endpoint APIEndpoint
	// Body contains the raw request body.
	Body io.Reader
	// IsStream indicates if the client expects a streaming response.
	IsStream bool
	// Headers contains the original HTTP headers.
	Headers http.Header
	// ClientID is a unique identifier for the client (for sticky routing).
	ClientID string
	// RequestID is a unique identifier for this request (for idempotency).
	RequestID string
	// SessionHash preserves caller stickiness without exposing transport types.
	SessionHash string
}

// RelayResult contains the response and metadata from orchestration.
type RelayResult struct {
	// Response is the upstream response body (may be streaming).
	Response io.ReadCloser
	// Headers contains the HTTP headers from the upstream response.
	Headers http.Header
	// StatusCode is the HTTP status code.
	StatusCode int
	// Usage contains the parsed usage envelope (reported + canonical +
	// parse verdict) of the FINAL successful attempt for billing.
	Usage *relaybiz.UsageEnvelope
	// ChannelID is the selected channel ID.
	ChannelID int64
	// SubscriptionAccountID is the selected subscription account ID (if applicable).
	SubscriptionAccountID int64
	// Latency is the total orchestration duration.
	Latency time.Duration
	// Error contains any error that occurred (non-nil if StatusCode >= 400).
	Error error
}

// Reservation captures a quota reservation made before upstream forwarding.
type Reservation struct {
	ID string
}

// RelayLifecycleHooks integrates side effects that are owned by the outer
// server layer, such as billing and usage logging.
type RelayLifecycleHooks interface {
	ReserveQuota(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, estimated relaybiz.UsageEnvelope) (*Reservation, error)
	CommitQuota(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, reservation *Reservation, usage relaybiz.UsageEnvelope, success bool, latency time.Duration) error
	ReleaseQuota(ctx context.Context, reservation *Reservation, reason string) error
	LogUsage(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, usage relaybiz.UsageEnvelope, latency time.Duration, stream bool)
}

type relayUserRateLimitHook interface {
	CheckUserRateLimit(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest) error
}

type relayStreamCompletionHook interface {
	CompleteStream(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, responseID string)
}

type relayAttemptAdmissionHook interface {
	AcquireRelayAttempt(ctx context.Context, plan *relaybiz.RelayPlan, req relaybiz.ExecutorRequest) (func(), error)
}

// OrchestratorConfig holds configuration for the orchestrator.
type OrchestratorConfig struct {
	// MaxAttempts is the maximum number of retry attempts (including initial).
	MaxAttempts int
	// ReserveTimeout is the timeout for quota reservation.
	ReserveTimeout time.Duration
	// CommitTimeout is the timeout for quota commit.
	CommitTimeout time.Duration
	// ForwardTimeout is the timeout for upstream forwarding.
	ForwardTimeout time.Duration
	// EnableRetry enables retry logic.
	EnableRetry bool
	// EnableFailover enables channel failover on retry.
	EnableFailover bool
}

// DefaultOrchestratorConfig returns the default orchestrator configuration.
func DefaultOrchestratorConfig() *OrchestratorConfig {
	return &OrchestratorConfig{
		MaxAttempts:    3,
		ReserveTimeout: 5 * time.Second,
		CommitTimeout:  5 * time.Second,
		ForwardTimeout: 300 * time.Second,
		EnableRetry:    true,
		EnableFailover: true,
	}
}

// relayOrchestrator is the concrete implementation of Orchestrator.
type relayOrchestrator struct {
	config          *OrchestratorConfig
	relayUsecase    *relaybiz.RelayUsecase
	planner         relaybiz.Planner
	quotaPort       relaybiz.QuotaPort
	forwardPort     relaybiz.Forwarder
	streamPort      relaybiz.StreamForwarder
	eventLogger     relaybiz.EventLogger
	providerFactory *relayprovider.ProviderFactory
	hooks           RelayLifecycleHooks
}

// NewRelayOrchestrator creates a new orchestrator instance.
func NewRelayOrchestrator(relayUsecase *relaybiz.RelayUsecase, cfg *OrchestratorConfig) Orchestrator {
	return NewRelayOrchestratorWithProviderFactory(relayUsecase, nil, cfg)
}

// NewRelayOrchestratorWithProviderFactory creates a relay orchestrator that can
// execute the upstream forwarding stage.
func NewRelayOrchestratorWithProviderFactory(relayUsecase *relaybiz.RelayUsecase, providerFactory *relayprovider.ProviderFactory, cfg *OrchestratorConfig) Orchestrator {
	return NewRelayOrchestratorWithDependencies(relayUsecase, providerFactory, nil, cfg)
}

// NewRelayOrchestratorWithDependencies creates a relay orchestrator with
// optional lifecycle hooks for quota and logging side effects.
func NewRelayOrchestratorWithDependencies(relayUsecase *relaybiz.RelayUsecase, providerFactory *relayprovider.ProviderFactory, hooks RelayLifecycleHooks, cfg *OrchestratorConfig) Orchestrator {
	return newRelayOrchestrator(relayUsecase, providerFactory, hooks, cfg)
}

// NewRelayExecutorWithDependencies exposes the same relay executor
// through the transport-neutral business contract used by the staging HTTP
// adapter. Legacy callers may keep NewRelayOrchestratorWithDependencies during
// the migration window.
func NewRelayExecutorWithDependencies(relayUsecase *relaybiz.RelayUsecase, providerFactory *relayprovider.ProviderFactory, hooks RelayLifecycleHooks, cfg *OrchestratorConfig) relaybiz.Executor {
	return relayExecutorAdapter{orchestrator: newRelayOrchestrator(relayUsecase, providerFactory, hooks, cfg)}
}

// NewRelayExecutorWithForwarder is the migration seam for transport-neutral
// executors that need a server-owned forwarding implementation, such as the
// adaptor registry path. The legacy constructor remains provider-backed for
// callers that have not opted into the staged route.
func NewRelayExecutorWithForwarder(relayUsecase *relaybiz.RelayUsecase, providerFactory *relayprovider.ProviderFactory, hooks RelayLifecycleHooks, customForwarder relaybiz.Forwarder, cfg *OrchestratorConfig) relaybiz.Executor {
	orchestrator := newRelayOrchestrator(relayUsecase, providerFactory, hooks, cfg)
	if customForwarder != nil {
		orchestrator.forwardPort = customForwarder
		if streamForwarder, ok := customForwarder.(relaybiz.StreamForwarder); ok {
			orchestrator.streamPort = streamForwarder
		}
	}
	return relayExecutorAdapter{orchestrator: orchestrator}
}

func newRelayOrchestrator(relayUsecase *relaybiz.RelayUsecase, providerFactory *relayprovider.ProviderFactory, hooks RelayLifecycleHooks, cfg *OrchestratorConfig) *relayOrchestrator {
	if cfg == nil {
		cfg = DefaultOrchestratorConfig()
	}
	orchestrator := &relayOrchestrator{
		config:          cfg,
		relayUsecase:    relayUsecase,
		providerFactory: providerFactory,
		hooks:           hooks,
	}
	if relayUsecase != nil {
		orchestrator.planner = relayUsecase
	}
	if hooks != nil {
		orchestrator.quotaPort = relayQuotaPort{hooks: hooks}
		orchestrator.eventLogger = relayEventLogger{hooks: hooks}
	}
	if providerFactory != nil {
		orchestrator.forwardPort = relayNonStreamForwarder{forwarder: forwarder.NewNonStreamForwarder(providerFactory)}
		orchestrator.streamPort = relayProviderStreamForwarder{forwarder: forwarder.NewStreamForwarder(providerFactory)}
	}
	return orchestrator
}

// Execute runs the complete relay pipeline.
//
// The pipeline consists of the following stages:
//
// 1. Auth Validation: Verify token and get user context
// 2. Model Mapping: Resolve client model to upstream model
// 3. Channel Selection: Select appropriate channel for the request
// 4. Quota Reservation: Reserve quota for the estimated cost
// 5. Request Forwarding: Forward request to upstream provider
// 6. Response Processing: Process response and extract usage
// 7. Quota Commit/Release: Commit actual usage or release reservation on error
// 8. Logging: Log the request for billing and analytics
func (o *relayOrchestrator) Execute(ctx context.Context, req *RelayRequest) (*RelayResult, error) {
	startTime := time.Now()
	result := &RelayResult{StatusCode: http.StatusOK}
	if req == nil {
		err := fmt.Errorf("relay request is nil")
		result.Error = err
		result.StatusCode = http.StatusBadRequest
		result.Latency = time.Since(startTime)
		return result, err
	}
	if o == nil || o.relayUsecase == nil {
		err := fmt.Errorf("relay orchestrator unavailable: no relay usecase configured")
		result.Error = err
		result.StatusCode = http.StatusInternalServerError
		result.Latency = time.Since(startTime)
		return result, err
	}
	if req.RequestID == "" {
		req.RequestID = generateRequestID()
	}

	// Stage 1-3: Planning (auth, model mapping, channel selection)
	// This reuses the existing RelayUsecase.Plan() logic
	plan, err := o.planner.Plan(ctx, relaybiz.RelayRequest{Token: req.Token, Model: req.Model, RequestID: req.RequestID, SessionHash: req.SessionHash})
	if err != nil {
		result.Error = err
		result.StatusCode = statusCodeFromError(err)
		result.Latency = time.Since(startTime)
		return result, err
	}
	if plan == nil || plan.Auth == nil || plan.Channel == nil {
		err := fmt.Errorf("relay plan is incomplete")
		result.Error = err
		result.StatusCode = http.StatusServiceUnavailable
		result.Latency = time.Since(startTime)
		return result, err
	}
	if limiter, ok := o.hooks.(relayUserRateLimitHook); ok {
		if err := limiter.CheckUserRateLimit(ctx, plan, req); err != nil {
			result.Error = err
			result.StatusCode = http.StatusTooManyRequests
			result.Latency = time.Since(startTime)
			return result, err
		}
	}

	// Store resolved information in result
	result.ChannelID = plan.Channel.ID
	if plan.Account != nil {
		result.SubscriptionAccountID = plan.Account.ID
	}

	if req.Body == nil {
		err := fmt.Errorf("relay request body is nil")
		result.Error = err
		result.StatusCode = http.StatusBadRequest
		result.Latency = time.Since(startTime)
		return result, err
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		result.Error = err
		result.StatusCode = http.StatusBadRequest
		result.Latency = time.Since(startTime)
		return result, err
	}
	rawBody := body
	body = rewriteRequestModel(body, plan.ResolvedModel)

	endpoint := endpointPath(req.Endpoint)
	if endpoint == "" {
		err := fmt.Errorf("unsupported endpoint %q", req.Endpoint)
		result.Error = err
		result.StatusCode = http.StatusNotFound
		result.Latency = time.Since(startTime)
		return result, err
	}
	if o.providerFactory == nil {
		err := fmt.Errorf("relay orchestrator unavailable: no provider factory configured")
		result.Error = err
		result.StatusCode = http.StatusInternalServerError
		result.Latency = time.Since(startTime)
		return result, err
	}

	if req.IsStream {
		var finalStream *relaybiz.StreamForwardResponse
		var finalPlan *relaybiz.RelayPlan
		var finalRequest relaybiz.ExecutorRequest
		var finalReservation *relaybiz.QuotaReservation
		var finalReleaseAdmission func()
		lastFailureStatus := 0
		attemptNumber := 0
		executeStreamAttempt := func(attemptCtx context.Context, channel *relaybiz.Channel) error {
			attemptPlan, planErr := o.relayPlanForAttempt(attemptCtx, plan, channel, req.Model)
			if planErr != nil {
				lastFailureStatus = http.StatusServiceUnavailable
				return planErr
			}
			attemptBody := rewriteRequestModel(rawBody, attemptPlan.ResolvedModel)
			attemptRequest := relaybiz.ExecutorRequest{
				Token:       req.Token,
				Model:       req.Model,
				Endpoint:    string(req.Endpoint),
				Body:        attemptBody,
				Headers:     httpHeaderToMap(req.Headers),
				RequestID:   req.RequestID,
				SessionHash: req.SessionHash,
				Stream:      true,
			}
			if attemptNumber > 0 {
				attemptRequest.RequestID = generateRequestID()
			}
			attemptNumber++
			estimatedUsage := estimateUsageFromBody(attemptBody)
			releaseAdmission := func() {}
			if admission, ok := o.hooks.(relayAttemptAdmissionHook); ok {
				releaseAdmission, err = admission.AcquireRelayAttempt(attemptCtx, attemptPlan, attemptRequest)
				if err != nil {
					lastFailureStatus = http.StatusServiceUnavailable
					return err
				}
			}
			var reservation *relaybiz.QuotaReservation
			if o.quotaPort != nil {
				reservation, err = o.quotaPort.Reserve(attemptCtx, attemptPlan, attemptRequest, estimatedUsage)
				if err != nil {
					releaseAdmission()
					lastFailureStatus = http.StatusPaymentRequired
					return err
				}
			}
			if o.streamPort == nil {
				err := fmt.Errorf("relay stream forwarder unavailable")
				if o.quotaPort != nil {
					_ = o.quotaPort.Release(attemptCtx, reservation, "stream forwarder unavailable")
				}
				releaseAdmission()
				lastFailureStatus = http.StatusInternalServerError
				return err
			}
			streamResponse, forwardErr := o.streamPort.ForwardStream(attemptCtx, attemptPlan, attemptRequest)
			if forwardErr != nil {
				if o.quotaPort != nil {
					_ = o.quotaPort.Release(attemptCtx, reservation, "upstream stream error")
				}
				releaseAdmission()
				lastFailureStatus = mapUpstreamOrInternalStatus(forwardErr)
				return forwardErr
			}
			if streamResponse == nil || streamResponse.Stream == nil {
				err := fmt.Errorf("stream forwarder returned no stream")
				if o.quotaPort != nil {
					_ = o.quotaPort.Release(attemptCtx, reservation, "empty upstream stream")
				}
				releaseAdmission()
				lastFailureStatus = http.StatusBadGateway
				return err
			}
			finalStream = streamResponse
			finalPlan = attemptPlan
			finalRequest = attemptRequest
			finalReservation = reservation
			finalReleaseAdmission = releaseAdmission
			return nil
		}

		retryResult := o.relayUsecase.NewRetryExecutor().ExecuteWithCandidates(ctx, plan, subscriptionAccountIDFromPlan(plan), executeStreamAttempt)
		if retryResult != nil && retryResult.Err != nil {
			latency := time.Since(startTime)
			o.finalizeSelectionFromRetryResult(plan, retryResult, latency)
			if retryResult.Fallback {
				recordRelayFailover(ctx, "exhausted", retryResult.FallbackReason)
			}
			result.Error = retryResult.Err
			result.StatusCode = lastFailureStatus
			if result.StatusCode == 0 {
				result.StatusCode = mapUpstreamOrInternalStatus(retryResult.Err)
			}
			result.Latency = latency
			return result, retryResult.Err
		}
		if finalStream == nil || finalPlan == nil {
			err := fmt.Errorf("relay executor returned no stream")
			result.Error = err
			result.StatusCode = http.StatusBadGateway
			result.Latency = time.Since(startTime)
			return result, err
		}
		if retryResult != nil && retryResult.Fallback {
			recordRelayFailover(ctx, "switched", retryResult.FallbackReason)
		}
		estimatedUsage := estimateUsageFromBody(finalRequest.Body)
		result.StatusCode = finalStream.StatusCode
		result.Headers = headerMapToHTTP(finalStream.Headers)
		result.ChannelID = finalPlan.Channel.ID
		result.SubscriptionAccountID = subscriptionAccountIDFromPlan(finalPlan)
		result.Response = newFinalizingRelayStream(req.Endpoint, finalStream.Stream, estimatedUsage, func(streamUsage relaybiz.UsageEnvelope, responseID string, completed bool) error {
			if finalReleaseAdmission != nil {
				defer finalReleaseAdmission()
			}
			// §7: the semantics come from the parser's verdict on the final
			// attempt's stream; they MUST NOT be re-derived here from the plan
			// (no IsPromptExclusiveChannel override). A verified envelope that
			// carries cache buckets without proven semantics is a parser bug:
			// count it and let billing's ambiguous safety path handle it.
			if streamUsage.ParseStatus == relaybiz.UsageParseVerified && streamUsage.Semantics == "" &&
				streamUsage.Canonical != nil && (streamUsage.Canonical.CacheReadTokens > 0 ||
				streamUsage.Canonical.CacheCreation5mTokens > 0 || streamUsage.Canonical.CacheCreation1hTokens > 0) {
				recordTokenUsageAnomaly(relaybiz.UsageReasonFinalAttemptSemanticsMissing)
			}
			latency := time.Since(startTime)
			settleCtx := settlementContext(ctx)
			if !completed {
				setRelayObservationResult(settleCtx, "stream_error")
				o.finalizeSelectionResult(plan, "error", latency)
				if o.quotaPort != nil {
					return o.quotaPort.Release(settleCtx, finalReservation, "downstream stream interrupted")
				}
				return nil
			}
			o.finalizeSelectionFromRetryResult(plan, retryResult, latency)
			if o.quotaPort != nil {
				if err := o.quotaPort.Commit(settleCtx, finalPlan, finalRequest, finalReservation, streamUsage, true, latency); err != nil {
					setRelayObservationResult(settleCtx, "quota_error")
					return err
				}
			}
			if o.eventLogger != nil {
				o.eventLogger.LogUsage(settleCtx, finalPlan, relaybiz.UsageEvent{
					Model:     finalRequest.Model,
					Endpoint:  finalRequest.Endpoint,
					RequestID: finalRequest.RequestID,
					Stream:    true,
				}, streamUsage, latency, true)
			}
			if hook, ok := o.hooks.(relayStreamCompletionHook); ok {
				hook.CompleteStream(settleCtx, finalPlan, req, responseID)
			}
			setRelayObservationResult(settleCtx, "success")
			return nil
		})
		result.Latency = time.Since(startTime)
		return result, nil
	}

	var finalResult *RelayResult
	var finalPlan *relaybiz.RelayPlan
	var finalResponseID string
	lastFailureStatus := 0
	attemptNumber := 0
	executeAttempt := func(attemptCtx context.Context, channel *relaybiz.Channel) error {
		attemptPlan, planErr := o.relayPlanForAttempt(attemptCtx, plan, channel, req.Model)
		if planErr != nil {
			lastFailureStatus = http.StatusServiceUnavailable
			return planErr
		}
		attemptBody := rewriteRequestModel(rawBody, attemptPlan.ResolvedModel)
		attemptReq := relaybiz.ExecutorRequest{
			Token:       req.Token,
			Model:       req.Model,
			Endpoint:    string(req.Endpoint),
			Body:        attemptBody,
			Headers:     httpHeaderToMap(req.Headers),
			RequestID:   req.RequestID,
			SessionHash: req.SessionHash,
			Stream:      false,
		}
		// Billing reserves are idempotent by (user_id, request_id). Keep the
		// original ID for the first attempt, but mint a new one before retrying:
		// if releasing the failed reservation was unavailable, reusing its ID
		// would return that stale reservation for a different channel.
		if attemptNumber > 0 {
			attemptReq.RequestID = generateRequestID()
		}
		attemptNumber++
		estimatedUsage := estimateUsageFromBody(attemptBody)
		releaseAdmission := func() {}
		if admission, ok := o.hooks.(relayAttemptAdmissionHook); ok {
			releaseAdmission, err = admission.AcquireRelayAttempt(attemptCtx, attemptPlan, attemptReq)
			if err != nil {
				lastFailureStatus = http.StatusServiceUnavailable
				return err
			}
		}
		defer releaseAdmission()
		var reservation *relaybiz.QuotaReservation
		if o.quotaPort != nil {
			reservation, err = o.quotaPort.Reserve(attemptCtx, attemptPlan, attemptReq, estimatedUsage)
			if err != nil {
				lastFailureStatus = http.StatusPaymentRequired
				return err
			}
		}

		if o.forwardPort == nil {
			err := fmt.Errorf("relay forwarder unavailable")
			if o.quotaPort != nil {
				_ = o.quotaPort.Release(attemptCtx, reservation, "forwarder unavailable")
			}
			lastFailureStatus = http.StatusInternalServerError
			return err
		}
		forwardResp, forwardErr := o.forwardPort.Forward(attemptCtx, attemptPlan, attemptReq)
		if forwardErr != nil {
			if o.quotaPort != nil {
				_ = o.quotaPort.Release(attemptCtx, reservation, "upstream error")
			}
			lastFailureStatus = mapUpstreamOrInternalStatus(forwardErr)
			return forwardErr
		}
		if forwardResp == nil {
			err := fmt.Errorf("forwarder returned no response")
			if o.quotaPort != nil {
				_ = o.quotaPort.Release(attemptCtx, reservation, "empty upstream response")
			}
			lastFailureStatus = http.StatusBadGateway
			return err
		}

		attemptResult := &RelayResult{
			StatusCode:            forwardResp.StatusCode,
			Headers:               headerMapToHTTP(forwardResp.Headers),
			Response:              io.NopCloser(bytes.NewReader(forwardResp.Body)),
			ChannelID:             channel.ID,
			SubscriptionAccountID: subscriptionAccountIDFromPlan(attemptPlan),
		}
		if forwardResp.Usage != nil {
			attemptResult.Usage = forwardResp.Usage
		}
		if attemptResult.Usage == nil || attemptResult.Usage.BillableTotal() == 0 {
			attemptResult.Usage = &estimatedUsage
		}
		if o.quotaPort != nil {
			latency := time.Since(startTime)
			if err := o.quotaPort.Commit(attemptCtx, attemptPlan, attemptReq, reservation, *attemptResult.Usage, forwardResp.StatusCode < http.StatusBadRequest, latency); err != nil {
				_ = attemptResult.Response.Close()
				lastFailureStatus = http.StatusPaymentRequired
				// The upstream response is already complete. A billing transport
				// error may be retryable in isolation, but retrying the relay would
				// duplicate upstream cost and can produce a different response.
				return relaybiz.MarkPostForwardError(err)
			}
			if o.eventLogger != nil {
				// Usage logging only needs request metadata. Do not pass the
				// transport request through this boundary: it contains client
				// headers, body, and bearer credentials.
				o.eventLogger.LogUsage(attemptCtx, attemptPlan, relaybiz.UsageEvent{
					Model:     attemptReq.Model,
					Endpoint:  attemptReq.Endpoint,
					RequestID: attemptReq.RequestID,
					Stream:    false,
				}, *attemptResult.Usage, latency, false)
			}
		}
		finalResult = attemptResult
		finalPlan = attemptPlan
		if req.Endpoint == EndpointResponses {
			finalResponseID = extractResponseID(forwardResp.Body)
		}
		return nil
	}

	retryResult := o.relayUsecase.NewRetryExecutor().ExecuteWithCandidates(ctx, plan, subscriptionAccountIDFromPlan(plan), executeAttempt)

	latency := time.Since(startTime)
	o.finalizeSelectionFromRetryResult(plan, retryResult, latency)
	if retryResult != nil && retryResult.Fallback {
		failoverResult := "switched"
		if retryResult.Err != nil {
			failoverResult = "exhausted"
		}
		recordRelayFailover(ctx, failoverResult, retryResult.FallbackReason)
	}
	if retryResult != nil && retryResult.Err != nil {
		result.Error = retryResult.Err
		result.StatusCode = lastFailureStatus
		if result.StatusCode == 0 {
			result.StatusCode = mapUpstreamOrInternalStatus(retryResult.Err)
		}
		result.Latency = latency
		return result, retryResult.Err
	}
	if finalResult == nil {
		err := fmt.Errorf("relay executor returned no result")
		result.Error = err
		result.StatusCode = http.StatusBadGateway
		result.Latency = latency
		return result, err
	}
	finalResult.Latency = latency
	if hook, ok := o.hooks.(relayStreamCompletionHook); ok && finalPlan != nil {
		hook.CompleteStream(settlementContext(ctx), finalPlan, req, finalResponseID)
	}
	return finalResult, nil
}

// statusCodeFromError converts an error to an HTTP status code.
func statusCodeFromError(err error) int {
	if err == nil {
		return http.StatusOK
	}
	if apperrors.IsUnauthorized(err) {
		return http.StatusUnauthorized
	}
	if apperrors.IsForbidden(err) {
		return http.StatusForbidden
	}
	if apperrors.IsServiceUnavailable(err) || isChannelUnavailableMessage(err.Error()) {
		return http.StatusServiceUnavailable
	}
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unauthenticated, codes.NotFound:
			return http.StatusUnauthorized
		case codes.PermissionDenied:
			return http.StatusForbidden
		case codes.ResourceExhausted:
			return http.StatusTooManyRequests
		case codes.Unavailable:
			return http.StatusServiceUnavailable
		case codes.InvalidArgument:
			return http.StatusBadRequest
		}
	}
	return http.StatusInternalServerError
}

func endpointPath(endpoint APIEndpoint) string {
	switch endpoint {
	case EndpointChatCompletions:
		return "/chat/completions"
	case EndpointCompletions:
		return "/completions"
	case EndpointResponses:
		return "/responses"
	case EndpointAnthropicMessages:
		return "/messages"
	default:
		return ""
	}
}

func rewriteRequestModel(body []byte, model string) []byte {
	if model == "" || len(body) == 0 {
		return body
	}
	var payload map[string]any
	if err := jsonx.Unmarshal(body, &payload); err != nil {
		return body
	}
	payload["model"] = model
	rewritten, err := jsonx.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

func mapUpstreamOrInternalStatus(err error) int {
	if upstreamErr, ok := err.(*relayprovider.UpstreamHTTPError); ok {
		return upstreamErr.StatusCode
	}
	return http.StatusBadGateway
}

type chunkReadCloser struct {
	chunks   <-chan []byte
	buf      *bytes.Reader
	onClose  func(relaybiz.UsageEnvelope) error
	usage    rawUsage
	closeErr error
	closed   bool
}

func newChunkReadCloser(chunks <-chan []byte, onClose ...func(relaybiz.UsageEnvelope) error) io.ReadCloser {
	var closeFn func(relaybiz.UsageEnvelope) error
	if len(onClose) > 0 {
		closeFn = onClose[0]
	}
	return &chunkReadCloser{chunks: chunks, buf: bytes.NewReader(nil), onClose: closeFn}
}

func (r *chunkReadCloser) Read(p []byte) (int, error) {
	for r.buf.Len() == 0 {
		chunk, ok := <-r.chunks
		if !ok {
			return 0, io.EOF
		}
		if len(chunk) == 0 {
			continue
		}
		r.usage = mergeRawUsage(extractRawUsage(chunk, 0), r.usage)
		r.buf = bytes.NewReader(chunk)
	}
	return r.buf.Read(p)
}

func (r *chunkReadCloser) Close() error {
	if r.closed {
		return r.closeErr
	}
	r.closed = true
	for range r.chunks {
	}
	if r.onClose != nil {
		r.closeErr = r.onClose(envelopeFromRawUsage(normalizeRawUsage(r.usage, 0)))
	}
	return r.closeErr
}

func (o *relayOrchestrator) releaseReservedQuota(ctx context.Context, reservation *Reservation, reason string) {
	if o.hooks == nil || reservation == nil {
		return
	}
	_ = o.hooks.ReleaseQuota(ctx, reservation, reason)
}

func (o *relayOrchestrator) commitReservedQuota(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, reservation *Reservation, usage relaybiz.UsageEnvelope, success bool, latency time.Duration) error {
	if o.hooks == nil || reservation == nil {
		return nil
	}
	return o.hooks.CommitQuota(ctx, plan, req, reservation, usage, success, latency)
}

func (o *relayOrchestrator) logUsage(ctx context.Context, plan *relaybiz.RelayPlan, req *RelayRequest, usage relaybiz.UsageEnvelope, latency time.Duration, stream bool) {
	if o.hooks == nil {
		return
	}
	o.hooks.LogUsage(ctx, plan, req, usage, latency, stream)
}

// finalizeSelectionResult emits the execution-boundary half of the routing
// selection observation (Phase 3 §3.4). It fills in Result / Fallback /
// FallbackReason / ElapsedMS on the plan's SelectionEvent and re-emits it so
// the metrics + structured log carry the full selection+execution picture.
// Without this call, RoutingSelectionTotal{result=error} and
// RoutingFallbackTotal would never fire, hiding error rates and fallback
// rates from ops. The function is nil-safe and never blocks the hot path.
func (o *relayOrchestrator) finalizeSelectionResult(plan *relaybiz.RelayPlan, result string, latency time.Duration) {
	if o.relayUsecase == nil || plan == nil || plan.SelectionEvent == nil {
		return
	}
	recorder := o.relayUsecase.GetSelectionRecorder()
	relaybiz.FinalizeSelectionResult(recorder, *plan.SelectionEvent, result, "", false, latency)
}

func relayPlanForChannel(base *relaybiz.RelayPlan, channel *relaybiz.Channel) *relaybiz.RelayPlan {
	if base == nil {
		return nil
	}
	plan := *base
	plan.Channel = channel
	if plan.Account != nil && channel != nil && channel.SubscriptionAccountID != plan.Account.ID {
		plan.Account = nil
	}
	plan.ResolvedModel = relaybiz.ResolveChannelModel(channel, base.BaseModel())
	return &plan
}

func (o *relayOrchestrator) relayPlanForAttempt(ctx context.Context, base *relaybiz.RelayPlan, channel *relaybiz.Channel, clientModel string) (*relaybiz.RelayPlan, error) {
	plan := relayPlanForChannel(base, channel)
	if plan == nil || channel == nil || channel.SubscriptionAccountID <= 0 {
		return plan, nil
	}
	if plan.Account != nil && plan.Account.ID == channel.SubscriptionAccountID {
		return plan, nil
	}
	if o == nil || o.relayUsecase == nil || base == nil || base.Auth == nil {
		return nil, fmt.Errorf("subscription routing source cannot be materialized")
	}
	resolvedChannel, account, err := o.relayUsecase.ResolveSubscriptionRoutingSource(
		ctx,
		channel.SubscriptionAccountID,
		base.Auth.Group,
		clientModel,
		base.BaseModel(),
	)
	if err != nil {
		return nil, err
	}
	plan.Channel = resolvedChannel
	plan.Account = account
	plan.ResolvedModel = relaybiz.ResolveChannelModel(resolvedChannel, base.BaseModel())
	return plan, nil
}

func (o *relayOrchestrator) finalizeSelectionFromRetryResult(plan *relaybiz.RelayPlan, retryResult *relaybiz.ExecuteResult, latency time.Duration) {
	if o == nil || o.relayUsecase == nil || plan == nil || plan.SelectionEvent == nil {
		return
	}
	recorder := o.relayUsecase.GetSelectionRecorder()
	resultLabel := "success"
	fallback := false
	fallbackReason := ""
	if retryResult != nil {
		fallback = retryResult.Fallback
		fallbackReason = retryResult.FallbackReason
		if retryResult.Err != nil {
			resultLabel = classifyResultLabel(retryResult.Err)
		}
		if retryResult.Channel != nil {
			plan.SelectionEvent.FinalSourceID = retryResult.Channel.ID
			if retryResult.Channel.SubscriptionAccountID > 0 {
				plan.SelectionEvent.FinalKind = relaybiz.UpstreamRouteSubscription.String()
				plan.SelectionEvent.FinalSourceID = retryResult.Channel.SubscriptionAccountID
			} else {
				plan.SelectionEvent.FinalKind = relaybiz.UpstreamRouteChannel.String()
			}
		}
	}
	relaybiz.FinalizeSelectionResult(recorder, *plan.SelectionEvent, resultLabel, fallbackReason, fallback, latency)
}

// estimateUsageFromBody builds the pre-request estimate envelope. The
// estimator only proves the uncached input bucket; it never fabricates cache
// (§4.1 estimated).
func estimateUsageFromBody(body []byte) relaybiz.UsageEnvelope {
	raw := estimateRawUsage(body)
	return relaybiz.UsageEnvelope{
		ContractVersion: relaybiz.UsageContractVersionV1,
		ParseStatus:     relaybiz.UsageParseEstimated,
		Canonical: &relaybiz.CanonicalUsage{
			UncachedInputTokens: raw.PromptTokens,
			OutputTokens:        raw.CompletionTokens,
		},
	}
}
