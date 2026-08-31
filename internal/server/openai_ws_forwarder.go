package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"go.uber.org/zap"
	"micro-one-api/pkg/jsonx"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relaybiz "micro-one-api/internal/biz"
	servererrors "micro-one-api/pkg/errors"
	applogger "micro-one-api/platform/logging"
)

// OpenAI Responses WebSocket protocol constants. The OpenAI-Beta header value
// matches the Responses WebSocket beta advertised by the Codex CLI and sub2api.
const (
	openAIWSBetaResponsesValue = "responses_websockets=2026-02-06"
	// openAIWSClientReadLimitBytes is the per-frame read limit applied to the
	// client-side connection. The Codex CLI can send very large response.create
	// payloads (tool call history, file context); the coder/websocket default
	// of 32 KiB would reject them, so we raise it to 64 MiB to match the HTTP
	// request body limit used elsewhere in this server.
	openAIWSClientReadLimitBytes int64 = 64 * 1024 * 1024
	openAIWSFirstMessageTimeout        = 30 * time.Second
)

// handleResponsesWebSocket handles the inbound side of a Codex Responses
// WebSocket connection: it accepts the upgrade, reads the first
// response.create frame, authenticates + plans the relay (model mapping,
// channel selection), dials the upstream Responses WebSocket, and runs the
// bidirectional relay with per-turn quota commit / usage logging.
//
// It is the WebSocket counterpart of handleResponsesCreateLike and reuses the
// same relaybiz.Plan + billing pipeline. The HTTP handler must guarantee that
// the request is a WebSocket upgrade (see isOpenAIWSUpgradeRequest) before
// calling this.
func (s *HTTPServer) handleResponsesWebSocket(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	// Drain gate (Phase 3.3): once the process is draining for shutdown,
	// reject new Responses-WS upgrades with 503 + Retry-After so load
	// balancers route to a healthy replica. Existing tracked connections are
	// allowed to finish their in-flight turns before force-close.
	if s != nil && s.IsWSDraining() {
		drainSec := int64(s.drainTimeout().Seconds())
		if drainSec <= 0 {
			drainSec = 30
		}
		w.Header().Set("Retry-After", fmt.Sprintf("%d", drainSec))
		s.writeError(w, http.StatusServiceUnavailable, "server is draining websocket connections")
		return
	}
	// Accept the upgrade. Compression with context takeover matches both the
	// Codex CLI and the sub2api upstream dialer.
	wsConn, err := coderws.Accept(w, r, &coderws.AcceptOptions{
		CompressionMode: coderws.CompressionContextTakeover,
	})
	if err != nil {
		applogger.Log.Warn("failed to accept openai responses websocket upgrade", zap.Error(err))
		return
	}
	defer func() {
		_ = wsConn.CloseNow()
	}()
	wsConn.SetReadLimit(openAIWSClientReadLimitBytes)

	clientFrameConn := &coderWSFrameConn{conn: wsConn}

	// Read the first frame: it must be a response.create JSON object carrying a
	// model field (the Codex CLI always opens the connection this way).
	readCtx, cancelRead := context.WithTimeout(ctx, s.openAIWSFirstMessageTimeout())
	msgType, firstMessage, err := clientFrameConn.ReadFrame(readCtx)
	cancelRead()
	if err != nil {
		closeOpenAIWSClientConn(wsConn, coderws.StatusPolicyViolation, "missing first response.create message")
		return
	}
	if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
		closeOpenAIWSClientConn(wsConn, coderws.StatusPolicyViolation, "unsupported websocket message type")
		return
	}

	clientModel := extractOpenAIWSClientModel(firstMessage)
	if clientModel == "" {
		closeOpenAIWSClientConn(wsConn, coderws.StatusPolicyViolation, "model is required in first response.create payload")
		return
	}

	token := extractOpenAIBearerToken(r)
	if token == "" {
		closeOpenAIWSClientConn(wsConn, coderws.StatusPolicyViolation, "missing authorization token")
		return
	}

	// Authenticate + plan (model mapping, channel selection). The first frame
	// is JSON-rewritten with the resolved upstream model before dialing.
	//
	var plan *relaybiz.RelayPlan
	requestID := generateRequestID()
	selectionStartedAt := time.Now()
	previousResponseID := extractOpenAIWSPreviousResponseIDFromRequest(firstMessage)
	sessionHash := extractOpenAIWSSessionHashFromRequest(r, firstMessage)
	if s.wsScheduler != nil {
		scheduledPlan, planErr := s.wsScheduler.ResolvePlan(ctx, token, clientModel, previousResponseID, sessionHash)
		if planErr != nil {
			s.closeOpenAIWSWithPlanError(wsConn, planErr)
			return
		}
		plan = scheduledPlan
	}
	if plan == nil {
		normalPlan, planErr := s.relayUsecase.Plan(ctx, relaybiz.RelayRequest{
			Token:     token,
			Model:     clientModel,
			RequestID: requestID,
		})
		if planErr != nil {
			s.closeOpenAIWSWithPlanError(wsConn, planErr)
			return
		}
		plan = normalPlan
		if s.wsScheduler != nil {
			s.wsScheduler.BindSession(ctx, plan, sessionHash)
		}
	}
	execution := responsesWSExecutionOutcome{resultLabel: "error", finalChannel: plan.Channel}
	defer func() {
		var finalSourceID int64
		if execution.finalChannel != nil {
			finalSourceID = execution.finalChannel.ID
			if execution.finalChannel.SubscriptionAccountID > 0 {
				finalSourceID = execution.finalChannel.SubscriptionAccountID
			}
			if plan.SelectionEvent != nil {
				plan.SelectionEvent.FinalSourceID = finalSourceID
				if execution.finalChannel.SubscriptionAccountID > 0 {
					plan.SelectionEvent.FinalKind = relaybiz.UpstreamRouteSubscription.String()
				} else {
					plan.SelectionEvent.FinalKind = relaybiz.UpstreamRouteChannel.String()
				}
			}
		}
		s.finalizeSelectionDirect(plan, execution.resultLabel, execution.fallbackReason, execution.fallback, finalSourceID, time.Since(selectionStartedAt))
		if execution.fallback {
			failoverResult := "switched"
			if execution.resultLabel != "success" {
				failoverResult = "exhausted"
			}
			recordRelayFailover(ctx, failoverResult, execution.fallbackReason)
		}
	}()
	if err := s.checkUserRPM(ctx, plan.Auth.UserID); err != nil {
		execution.resultLabel = "client_error"
		closeOpenAIWSClientConn(wsConn, coderws.StatusTryAgainLater, "user rpm limit exceeded")
		return
	}

	rewrittenFirstMessage := rewriteOpenAIWSModel(firstMessage, clientModel, plan.ResolvedModel)

	// Reservations mirror the HTTP path: estimate tokens from the request body
	// and commit per terminal turn.
	reservation, err := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimateRawTokens(rewrittenFirstMessage), s.BillingModelName(clientModel, plan.ResolvedModel, plan.ResolvedModel), fmt.Sprintf("%d", plan.Channel.ID), subscriptionAccountIDFromPlan(plan))
	if err != nil {
		execution.resultLabel = "client_error"
		closeOpenAIWSClientConn(wsConn, coderws.StatusTryAgainLater, "quota reservation failed")
		return
	}

	// Dial the upstream via the connection pool (reuses an idle conn for this
	// channel when available, otherwise dials fresh) and run the relay with
	// multi-channel failover: on a retryable upstream error (dial failure or a
	// relay error before any data reached the client) we switch to a different
	// channel and retry, up to the configured switch limit.
	maxSwitches := s.openAIWSFailoverMaxSwitches()

	// Register the client connection with the graceful-drain tracker (Phase
	// 3.3). DrainWSConnections, called from a BeforeStop hook on shutdown,
	// waits for tracked relays to complete (or force-closes them after the
	// drain timeout). The close func delegates to wsConn.CloseNow so the
	// tracker never touches coderws internals directly. Unregister happens
	// automatically when the connection closes; here we also defer an explicit
	// Unregister so a relay that returns without the close func firing (e.g.
	// context cancel) is removed from the active set.
	if s.wsConnTracker != nil {
		tracked := s.wsConnTracker.NewConnection(requestID, func() error {
			return wsConn.CloseNow()
		})
		if tracked == nil {
			// NewConnection returns nil when the tracker is already draining or
			// its capacity is exhausted. The connection is then invisible to the
			// drain logic, so DrainWSConnections will not wait for or force-close
			// it — the process exit will cut it off. Surface it as a warning so
			// an operator knows the drain window excluded an active relay.
			applogger.Log.Warn("openai ws connection not tracked; drain will not wait for it",
				zap.String("request_id", requestID),
				zap.String("model", clientModel),
			)
		} else {
			tracked.SetMetadata("endpoint", "/v1/responses")
			tracked.SetMetadata("model", clientModel)
			tracked.SetMetadata("user_id", fmt.Sprintf("%d", plan.Auth.UserID))
			defer tracked.Unregister()
		}
	}

	execution = s.runResponsesWSRelayWithFailover(ctx, wsConn, clientFrameConn, r, clientModel, sessionHash, plan, firstMessage, reservation, requestID, maxSwitches)
}

type responsesWSExecutionOutcome struct {
	resultLabel    string
	fallback       bool
	fallbackReason string
	finalChannel   *relaybiz.Channel
}

func routingSubscriptionAccountID(channel *relaybiz.Channel) int64 {
	if channel == nil {
		return 0
	}
	return channel.SubscriptionAccountID
}

// replaceResponsesWSReservation moves the pending first-turn reservation to
// the source selected after failover. The reservation stores channel/account
// attribution, so committing the original one would charge the right user but
// write the ledger against the failed source.
func (s *HTTPServer) replaceResponsesWSReservation(
	ctx context.Context,
	plan *relaybiz.RelayPlan,
	clientModel string,
	firstMessage []byte,
	requestID string,
	reservation *billingv1.ReserveQuotaResponse,
	channel *relaybiz.Channel,
) (*billingv1.ReserveQuotaResponse, error) {
	if plan == nil || plan.Auth == nil || channel == nil {
		return nil, errors.New("invalid websocket failover reservation inputs")
	}
	if reservation != nil && reservation.GetReservationId() != "" {
		if err := s.releaseQuota(ctx, reservation.GetReservationId(), "routing source failover"); err != nil {
			return nil, fmt.Errorf("release failed-source reservation: %w", err)
		}
	}
	resolvedModel := relaybiz.ResolveChannelModel(channel, plan.BaseModel())
	rewritten := rewriteOpenAIWSModel(firstMessage, clientModel, resolvedModel)
	return s.reserveQuota(
		ctx,
		fmt.Sprintf("%d", plan.Auth.UserID),
		requestID,
		estimateRawTokens(rewritten),
		s.BillingModelName(clientModel, resolvedModel, resolvedModel),
		fmt.Sprintf("%d", channel.ID),
		routingSubscriptionAccountID(channel),
	)
}

// buildOpenAIWSUpstreamTarget computes the upstream Responses WebSocket URL and
// request headers for the selected channel. The channel's base URL (already
// normalized by the provider factory to https) is converted to wss/ws, and the
// Authorization + OpenAI-Beta headers are set.
func (s *HTTPServer) buildOpenAIWSUpstreamTarget(ctx context.Context, r *http.Request, ch *relaybiz.Channel) (string, http.Header, error) {
	if ch == nil {
		return "", nil, errors.New("upstream channel is nil")
	}
	baseURL := ch.BaseURL
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", nil, fmt.Errorf("invalid channel base url: %w", err)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "wss", "ws":
		// keep as-is
	default:
		return "", nil, fmt.Errorf("unsupported scheme for ws: %s", parsed.Scheme)
	}
	// Ensure the path ends with /responses. Most OpenAI-compatible base URLs are
	// configured as ".../v1"; the Responses WS endpoint is /v1/responses.
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if !strings.HasSuffix(parsed.Path, "/responses") {
		parsed.Path = parsed.Path + "/responses"
	}
	wsURL := parsed.String()

	credential := ch.Key
	if ch.SubscriptionAccountID > 0 {
		if s.accountResolver == nil {
			return "", nil, errors.New("subscription account credential resolver is not configured")
		}
		metadata, resolveErr := s.accountResolver.Resolve(ctx, ch.SubscriptionAccountID)
		if resolveErr != nil {
			return "", nil, fmt.Errorf("resolve subscription account credential: %w", resolveErr)
		}
		if metadata == nil || strings.TrimSpace(metadata.AccessToken) == "" {
			return "", nil, errors.New("resolve subscription account credential: empty access token")
		}
		credential = metadata.AccessToken
	}
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+credential)
	headers.Set("OpenAI-Beta", openAIWSBetaResponsesValue)
	if ua := strings.TrimSpace(r.Header.Get("User-Agent")); ua != "" {
		headers.Set("User-Agent", ua)
	}
	return wsURL, headers, nil
}

// closeOpenAIWSWithPlanError maps a biz Plan() error to a WebSocket close
// status/reason, mirroring handleRelayPlanError / handleIdentityError.
func (s *HTTPServer) closeOpenAIWSWithPlanError(conn *coderws.Conn, err error) {
	if servererrors.IsUnauthorized(err) {
		closeOpenAIWSClientConn(conn, coderws.StatusPolicyViolation, "unauthorized")
		return
	}
	if servererrors.IsForbidden(err) {
		closeOpenAIWSClientConn(conn, coderws.StatusPolicyViolation, "forbidden")
		return
	}
	if servererrors.IsServiceUnavailable(err) {
		closeOpenAIWSClientConn(conn, coderws.StatusTryAgainLater, "service unavailable")
		return
	}
	closeOpenAIWSClientConn(conn, coderws.StatusInternalError, "internal server error")
}

// closeOpenAIWSWithDialError maps an upstream dial failure to a WebSocket close
// status/reason, mirroring sub2api's mapOpenAIWSPassthroughDialError.
func (s *HTTPServer) closeOpenAIWSWithDialError(conn *coderws.Conn, statusCode int, err error, headers http.Header) {
	switch statusCode {
	case http.StatusTooManyRequests:
		closeOpenAIWSClientConn(conn, coderws.StatusTryAgainLater, "upstream rate limit exceeded, please retry later")
	case http.StatusUnauthorized, http.StatusForbidden:
		closeOpenAIWSClientConn(conn, coderws.StatusPolicyViolation, "upstream websocket authentication failed")
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		closeOpenAIWSClientConn(conn, coderws.StatusTryAgainLater, "upstream service temporarily unavailable")
	default:
		closeOpenAIWSClientConn(conn, coderws.StatusInternalError, "upstream websocket proxy failed")
	}
}

// closeOpenAIWSClientConn closes a client WebSocket connection with the given
// status / reason, truncating the reason to the protocol limit (125 bytes).
func closeOpenAIWSClientConn(conn *coderws.Conn, status coderws.StatusCode, reason string) {
	if conn == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 120 {
		reason = reason[:120]
	}
	_ = conn.Close(status, reason)
	_ = conn.CloseNow()
}

// extractOpenAIWSClientModel returns the model field from the first
// response.create frame.
func extractOpenAIWSClientModel(message []byte) string {
	node, _ := jsonx.Get(message, "model")
	model, _ := node.String()
	return strings.TrimSpace(model)
}

// rewriteOpenAIWSModel replaces the model in a response.create frame when model
// mapping resolved it to a different upstream model. It mirrors the HTTP path's
// rewriteRawModel behaviour: if the resolved model equals the client model the
// payload is returned unchanged.
func rewriteOpenAIWSModel(message []byte, clientModel, resolvedModel string) []byte {
	if clientModel == resolvedModel || clientModel == "" || resolvedModel == "" {
		return message
	}
	var payload map[string]interface{}
	if err := jsonx.Unmarshal(message, &payload); err != nil {
		return message
	}
	payload["model"] = resolvedModel
	rewritten, err := jsonx.Marshal(payload)
	if err != nil {
		return message
	}
	return rewritten
}

// extractOpenAIBearerToken extracts the bearer token from the Authorization
// header.
func extractOpenAIBearerToken(r *http.Request) string {
	if r == nil {
		return ""
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
}

// isOpenAIWSUpgradeRequest reports whether the inbound request is a WebSocket
// upgrade request. Mirrors sub2api's isOpenAIWSUpgradeRequest.
func isOpenAIWSUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(r.Header.Get("Connection"))), "upgrade")
}

// openAIWSWriteTimeout / openAIWSIdleTimeout / openAIWSDialTimeout /
// openAIWSFirstMessageTimeout resolve the relay timeouts from config (set via
// SetOpenAIWSTimeouts) with sensible defaults. Zero / unset values fall back.
func (s *HTTPServer) openAIWSWriteTimeout() time.Duration {
	if s != nil && s.wsTimeouts.writeTimeout > 0 {
		return s.wsTimeouts.writeTimeout
	}
	return 2 * time.Minute
}

func (s *HTTPServer) openAIWSIdleTimeout() time.Duration {
	if s != nil && s.wsTimeouts.idleTimeout > 0 {
		return s.wsTimeouts.idleTimeout
	}
	return 5 * time.Minute
}

func (s *HTTPServer) openAIWSDialTimeout() time.Duration {
	if s != nil && s.wsTimeouts.dialTimeout > 0 {
		return s.wsTimeouts.dialTimeout
	}
	return openAIWSDialTimeoutDefault
}

func (s *HTTPServer) openAIWSFirstMessageTimeout() time.Duration {
	if s != nil && s.wsTimeouts.firstMessageTimeout > 0 {
		return s.wsTimeouts.firstMessageTimeout
	}
	return openAIWSFirstMessageTimeout
}

// errOpenAIWSForwarderUnused is retained to keep the `errors` import meaningful
// if future code paths add direct error construction here.
var _ = errors.New

// extractOpenAIWSResponseIDFromEvent pulls the response id from an upstream WS
// event frame (response.created / response.completed / ...). It reuses the same
// JSON shape as the HTTP stream path: response.id (preferred) or response_id.
func extractOpenAIWSResponseIDFromEvent(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	if node, _ := jsonx.Get(payload, "response", "id"); node.Exists() {
		if rid, _ := node.String(); strings.TrimSpace(rid) != "" {
			return strings.TrimSpace(rid)
		}
	}
	if node, _ := jsonx.Get(payload, "response_id"); node.Exists() {
		if rid, _ := node.String(); strings.TrimSpace(rid) != "" {
			return strings.TrimSpace(rid)
		}
	}
	return ""
}

// extractOpenAIWSPreviousResponseIDFromRequest pulls previous_response_id from a
// client response.create frame, mirroring the HTTP extractPreviousResponseID.
func extractOpenAIWSPreviousResponseIDFromRequest(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	node, _ := jsonx.Get(payload, "previous_response_id")
	rid, _ := node.String()
	rid = strings.TrimSpace(rid)
	if !isOpenAIResponseID(rid) {
		return ""
	}
	return rid
}

func extractOpenAIWSSessionHashFromRequest(r *http.Request, payload []byte) string {
	if r != nil {
		for _, key := range []string{"X-Session-Hash", "OpenAI-Session-Hash"} {
			if value := strings.TrimSpace(r.Header.Get(key)); value != "" {
				return value
			}
		}
	}
	if len(payload) == 0 {
		return ""
	}
	for _, key := range []string{"session_hash", "sessionHash"} {
		node, _ := jsonx.Get(payload, key)
		if value, _ := node.String(); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// openAIWSFailoverMaxSwitches returns the configured failover switch limit
// (default 2): the number of alternative channels to try when the initial
// channel fails before reaching the client.
func (s *HTTPServer) openAIWSFailoverMaxSwitches() int {
	if s != nil && s.wsPoolCfg.failoverMaxSwitches > 0 {
		return s.wsPoolCfg.failoverMaxSwitches
	}
	return 2
}

// openAIWSStickyTTL returns the configured sticky binding TTL (default 1h).
func (s *HTTPServer) openAIWSStickyTTL() time.Duration {
	if s != nil && s.wsPoolCfg.stickyTTL > 0 {
		return s.wsPoolCfg.stickyTTL
	}
	return openAIWSStickyTTL
}

// runResponsesWSRelayWithFailover dials the upstream via the pool, runs the
// relay, and on a retryable failure retries against a freshly selected channel
// (excluding the failed one's priority) up to maxSwitches times. It owns the
// turn-committed usage logging / quota commit and the pool release semantics.
//
// Retry is only attempted before the relay has written any data downstream
// (wroteDownstream == false); once bytes have flowed to the client, switching
// channels mid-stream would corrupt the client's view of the response.
func (s *HTTPServer) runResponsesWSRelayWithFailover(
	ctx context.Context,
	wsConn *coderws.Conn,
	clientFrameConn *coderWSFrameConn,
	r *http.Request,
	clientModel string,
	sessionHash string,
	plan *relaybiz.RelayPlan,
	firstMessage []byte,
	reservation *billingv1.ReserveQuotaResponse,
	requestID string,
	maxSwitches int,
) responsesWSExecutionOutcome {
	currentChannel := plan.Channel
	outcome := responsesWSExecutionOutcome{resultLabel: "error", finalChannel: currentChannel}
	var firstFailure error
	// start from the globally-resolved model so failover remaps
	// from a clean base. plan.ResolvedModel already carries the first channel's
	// mapping; the loop below recomputes it per-channel from the plan base model.
	resolvedModel := plan.BaseModel()
	failedSources := make(map[relaybiz.RoutingSourceIdentity]bool, maxSwitches+1)

	for attempt := 0; ; attempt++ {
		attemptStartedAt := time.Now()
		// re-apply the current channel's per-channel model mapping on
		// each (re)entry and after a failover switch, so the upstream model
		// matches the channel actually serving the request.
		resolvedModel = relaybiz.ResolveChannelModel(currentChannel, plan.BaseModel()) // recompute from global model
		rewrittenFirstMessage := rewriteOpenAIWSModel(firstMessage, clientModel, resolvedModel)
		// Resolve the upstream target for the current channel.
		wsURL, headers, err := s.buildOpenAIWSUpstreamTarget(ctx, r, currentChannel)
		if err != nil {
			s.relayUsecase.RecordRoutingSourceHealth(ctx, currentChannel, false, err.Error(), time.Since(attemptStartedAt).Milliseconds())
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream target error")
			closeOpenAIWSClientConn(wsConn, coderws.StatusInternalError, "failed to build upstream websocket target")
			return outcome
		}

		// Acquire a (possibly pooled) upstream connection.
		pooledConn, err := s.acquireOpenAIWSUpstreamConn(ctx, currentChannel, wsURL, headers)
		if err != nil {
			s.relayUsecase.RecordRoutingSourceHealth(ctx, currentChannel, false, err.Error(), time.Since(attemptStartedAt).Milliseconds())
			// Dial failed. Try failover if we haven't exhausted switches.
			// Capture the failed channel id before maybeFailoverChannel mutates
			// currentChannel, otherwise the log records the channel we switched to.
			failedChannelID := currentChannel.ID
			failedSources[relaybiz.RoutingSourceIdentityForChannel(currentChannel)] = true
			if attempt < maxSwitches && s.maybeFailoverChannel(ctx, plan, clientModel, currentChannel, err, failedSources, &currentChannel) {
				if firstFailure == nil {
					firstFailure = err
				}
				outcome.fallback = true
				outcome.fallbackReason = relaybiz.ClassifyRetryFallbackReason(firstFailure)
				outcome.finalChannel = currentChannel
				applogger.Log.Info("openai ws failover after dial error",
					zap.String("request_id", requestID),
					zap.Int("attempt", attempt+1),
					zap.Int64("failed_channel", failedChannelID),
					zap.Error(err),
				)
				var reserveErr error
				reservation, reserveErr = s.replaceResponsesWSReservation(ctx, plan, clientModel, firstMessage, requestID, reservation, currentChannel)
				if reserveErr != nil {
					closeOpenAIWSClientConn(wsConn, coderws.StatusTryAgainLater, "quota reservation failed after failover")
					return outcome
				}
				continue
			}
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream dial error")
			closeOpenAIWSClientConn(wsConn, coderws.StatusInternalError, "upstream dial failed")
			return outcome
		}

		turnCommits := 0
		// Per-turn usage logging / quota commit. Closure captures the current
		// channel so failover switches log against the right channel.
		onTurnComplete := func(turn openAIWSTurnResult) {
			usage := turn.usage
			actualTotal := usage.totalTokens
			if actualTotal <= 0 {
				actualTotal = usage.promptTokens + usage.completionTokens
			}
			turnID := turn.requestID
			if turnID == "" {
				turnID = requestID
			}
			logInput := usageLogInput{
				UserID:                plan.Auth.UserID,
				TokenID:               plan.Auth.TokenID,
				TokenName:             plan.Auth.TokenName,
				RequestID:             turnID,
				Endpoint:              "/v1/responses",
				ModelName:             s.BillingModelName(clientModel, resolvedModel, resolvedModel),
				Quota:                 actualTotal,
				PromptTokens:          usage.promptTokens,
				CompletionTokens:      usage.completionTokens,
				CacheReadTokens:       usage.cacheReadTokens,
				CacheCreation5mTokens: usage.cacheCreation5mTokens,
				CacheCreation1hTokens: usage.cacheCreation1hTokens,
				ChannelID:             currentChannel.ID,
				IsStream:              true,
			}
			logInput.applyChannelInputs(currentChannel)
			logInput.applyEnvelope(envelopeFromRawUsage(rawUsage{
				PromptTokens:          usage.promptTokens,
				CompletionTokens:      usage.completionTokens,
				CacheReadTokens:       usage.cacheReadTokens,
				CacheCreation5mTokens: usage.cacheCreation5mTokens,
				CacheCreation1hTokens: usage.cacheCreation1hTokens,
				TotalTokens:           usage.totalTokens,
				ReportedTotalTokens:   usage.reportedTotalTokens,
				Shape:                 usage.shape,
			}))
			logUpstreamUsage(logInput)
			// Each turn commits against its own reservation. The connection-level
			// reservation only covers the first turn; a Responses WebSocket is
			// long-lived and multi-turn, so reusing one reservation id for every
			// turn would under-bill (or double-commit) every turn after the first.
			turnReservationID := reservation.ReservationId
			if turnCommits > 0 {
				turnReservationID = ""
				if s.billingClient != nil {
					if turnRes, rerr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), turnID, actualTotal, s.BillingModelName(clientModel, resolvedModel, resolvedModel), fmt.Sprintf("%d", currentChannel.ID), routingSubscriptionAccountID(currentChannel)); rerr == nil && turnRes != nil {
						turnReservationID = turnRes.ReservationId
					} else {
						applogger.Log.Warn("failed to reserve openai ws turn quota",
							zap.String("request_id", turnID),
							zap.Error(rerr),
						)
					}
				}
			}
			if turnReservationID != "" {
				if commitErr := s.commitQuotaAfterResponseObserved(ctx, turnReservationID, actualTotal, true, logInput); commitErr != nil {
					applogger.Log.Warn("failed to commit openai ws turn quota",
						zap.String("request_id", turnID),
						zap.Error(commitErr),
					)
				} else {
					s.ingestUsageLogAfterResponse(logInput)
				}
			}
			// Bind the upstream response id -> channel both locally and in the
			// cross-process sticky store (Redis) so multi-replica deployments
			// resume the chain on the same channel.
			if turn.requestID != "" {
				s.storeResponseRoute(turn.requestID, responseRoute{
					Model:                 clientModel,
					GlobalModel:           plan.BaseModel(),
					ResolvedModel:         resolvedModel,
					Channel:               *currentChannel,
					UserID:                plan.Auth.UserID,
					SubscriptionAccountID: routingSubscriptionAccountID(currentChannel),
				})
				if s.wsSticky != nil {
					s.wsSticky.BindResponseRoute(ctx, plan.Auth.Group, turn.requestID, currentChannel, s.openAIWSStickyTTL())
				}
			}
			if s.wsSticky != nil && strings.TrimSpace(sessionHash) != "" {
				s.wsSticky.BindSessionRoute(ctx, plan.Auth.Group, sessionHash, currentChannel, s.openAIWSStickyTTL())
			}
			turnCommits++
		}

		relayResult, relayExit := relayOpenAIWSFrames(ctx, clientFrameConn, pooledConn.FrameConn(), rewrittenFirstMessage, openAIWSRelayOptions{
			writeTimeout:   s.openAIWSWriteTimeout(),
			idleTimeout:    s.openAIWSIdleTimeout(),
			onTurnComplete: onTurnComplete,
		})

		// Release the pooled connection. Mark broken if the relay errored so the
		// pool doesn't hand a dead conn to the next request.
		broken := relayExit != nil && relayExit.err != nil && !relayExit.graceful
		s.releaseOpenAIWSUpstreamConn(pooledConn, broken)
		if relayExit != nil && relayExit.err != nil && !relayExit.graceful {
			s.relayUsecase.RecordRoutingSourceHealth(ctx, currentChannel, false, relayExit.err.Error(), time.Since(attemptStartedAt).Milliseconds())
		} else {
			s.relayUsecase.RecordRoutingSourceHealth(ctx, currentChannel, true, "", time.Since(attemptStartedAt).Milliseconds())
		}

		// Failover decision: only retry if nothing was written downstream yet
		// and we haven't exhausted switches. A relay that wrote bytes must
		// terminate; retrying would double-send to the client.
		canFailover := relayExit != nil &&
			relayExit.err != nil &&
			!relayExit.wroteDownstream &&
			turnCommits == 0 &&
			attempt < maxSwitches

		if canFailover {
			applogger.Log.Info("openai ws failover after relay error",
				zap.String("request_id", requestID),
				zap.Int("attempt", attempt+1),
				zap.String("stage", relayExit.stage),
				zap.Int64("failed_channel", currentChannel.ID),
				zap.Error(relayExit.err),
			)
			failedSources[relaybiz.RoutingSourceIdentityForChannel(currentChannel)] = true
			if s.maybeFailoverChannel(ctx, plan, clientModel, currentChannel, relayExit.err, failedSources, &currentChannel) {
				if firstFailure == nil {
					firstFailure = relayExit.err
				}
				outcome.fallback = true
				outcome.fallbackReason = relaybiz.ClassifyRetryFallbackReason(firstFailure)
				outcome.finalChannel = currentChannel
				var reserveErr error
				reservation, reserveErr = s.replaceResponsesWSReservation(ctx, plan, clientModel, firstMessage, requestID, reservation, currentChannel)
				if reserveErr != nil {
					closeOpenAIWSClientConn(wsConn, coderws.StatusTryAgainLater, "quota reservation failed after failover")
					return outcome
				}
				continue
			}
		}

		// Terminal path: either success or unrecoverable failure.
		if turnCommits == 0 {
			if releaseErr := s.releaseQuota(ctx, reservation.ReservationId, "no completed ws turn"); releaseErr != nil {
				applogger.Log.Warn("failed to release openai ws reservation", zap.String("request_id", requestID), zap.Error(releaseErr))
			}
		}
		if relayExit != nil && relayExit.err != nil {
			applogger.Log.Info("openai responses websocket relay ended",
				zap.String("request_id", requestID),
				zap.String("stage", relayExit.stage),
				zap.Bool("graceful", relayExit.graceful),
				zap.Bool("wrote_downstream", relayExit.wroteDownstream),
				zap.Int64("c2u_frames", relayResult.clientToUpstream),
				zap.Int64("u2c_frames", relayResult.upstreamToClient),
				zap.Error(relayExit.err),
			)
		}
		if relayExit == nil || relayExit.err == nil || relayExit.graceful {
			outcome.resultLabel = "success"
		}
		outcome.finalChannel = currentChannel
		return outcome
	}
}

// openAIWSPoolKey returns the namespace-safe pool key for a routing source.
// Channel IDs and subscription account IDs are independent namespaces, so the
// key must carry the kind — otherwise channel #5 and subscription account #5
// would share pooled connections across credentials/upstreams.
func openAIWSPoolKey(ch *relaybiz.Channel) string {
	id := relaybiz.RoutingSourceIdentityForChannel(ch)
	return fmt.Sprintf("%s:%d", id.Kind.String(), id.ID)
}

// acquireOpenAIWSUpstreamConn returns a usable upstream connection. It prefers
// the connection pool (reusing idle conns for the source) and falls back to a
// direct dial when the pool is disabled (e.g. in tests).
func (s *HTTPServer) acquireOpenAIWSUpstreamConn(ctx context.Context, ch *relaybiz.Channel, wsURL string, headers http.Header) (*openAIWSPooledConn, error) {
	poolKey := openAIWSPoolKey(ch)
	if s.wsPool != nil {
		return s.wsPool.AcquireOrDial(ctx, poolKey, wsURL, headers)
	}
	// Pool disabled: dial directly.
	dialer := newCoderWSUpstreamDialer()
	dialCtx, cancel := context.WithTimeout(ctx, s.openAIWSDialTimeout())
	defer cancel()
	conn, statusCode, _, err := dialer.Dial(dialCtx, wsURL, headers)
	if err != nil {
		_ = statusCode
		return nil, err
	}
	pc := &openAIWSPooledConn{conn: conn, poolKey: poolKey, fingerprint: openAIWSConnFingerprint(wsURL, headers), lastUsedAt: time.Now()}
	pc.inUse.Store(true)
	return pc, nil
}

// releaseOpenAIWSUpstreamConn returns a connection to the pool, or closes it
// when no pool is configured.
func (s *HTTPServer) releaseOpenAIWSUpstreamConn(pc *openAIWSPooledConn, broken bool) {
	if s.wsPool != nil {
		s.wsPool.Release(pc, broken)
		return
	}
	if pc != nil {
		_ = pc.conn.Close()
	}
}

// maybeFailoverChannel selects an alternative routing source for the
// model/group while excluding sources that already failed in this request. On
// success it sets *next to the new source projection and returns true; on
// failure it returns false and the caller must surface the original error.
func (s *HTTPServer) maybeFailoverChannel(
	ctx context.Context,
	plan *relaybiz.RelayPlan,
	clientModel string,
	failed *relaybiz.Channel,
	cause error,
	excluded map[relaybiz.RoutingSourceIdentity]bool,
	next **relaybiz.Channel,
) bool {
	if s == nil || s.relayUsecase == nil || plan == nil || plan.Auth == nil || failed == nil || next == nil || cause == nil {
		return false
	}
	if !relaybiz.DefaultRetryPolicy().IsRetryable(cause) {
		return false
	}
	selected, err := s.relayUsecase.SelectFallbackRoutingSource(ctx, plan.Auth.Group, clientModel, plan.BaseModel(), excluded)
	if err != nil || selected == nil || relaybiz.SameRoutingSource(selected, failed) {
		return false
	}
	*next = selected
	return true
}

// lookupWSStickyRoute resolves a previous_response_id via the Redis-backed
// sticky store. The stored value includes the source namespace so a
// subscription account and an ordinary channel with the same numeric ID can
// never be confused.
func (s *HTTPServer) lookupWSStickyRoute(ctx context.Context, token, clientModel, responseID string, route *responseRoute) bool {
	if s == nil || s.wsSticky == nil || route == nil {
		return false
	}
	authSnapshot, err := s.getAuthSnapshot(ctx, token)
	if err != nil {
		return false
	}
	source := s.wsSticky.LookupResponseRoute(ctx, authSnapshot.Group, responseID)
	return s.materializeWSStickySource(ctx, authSnapshot, clientModel, source, route)
}

func (s *HTTPServer) lookupWSStickySessionRoute(ctx context.Context, token, clientModel, sessionHash string, route *responseRoute) bool {
	if s == nil || s.wsSticky == nil || route == nil {
		return false
	}
	authSnapshot, err := s.getAuthSnapshot(ctx, token)
	if err != nil {
		return false
	}
	source := s.wsSticky.LookupSessionRoute(ctx, authSnapshot.Group, sessionHash)
	return s.materializeWSStickySource(ctx, authSnapshot, clientModel, source, route)
}

func (s *HTTPServer) materializeWSStickySource(
	ctx context.Context,
	authSnapshot *identityv1.GetAuthSnapshotReply,
	clientModel string,
	source openAIWSStickySource,
	route *responseRoute,
) bool {
	if s == nil || authSnapshot == nil || route == nil || source.id <= 0 {
		return false
	}
	switch source.kind {
	case relaybiz.UpstreamRouteSubscription:
		if s.relayUsecase == nil {
			return false
		}
		resolvedModel := s.relayUsecase.ResolveModel(clientModel)
		channel, account, err := s.relayUsecase.ResolveSubscriptionRoutingSource(
			ctx, source.id, authSnapshot.Group, clientModel, resolvedModel,
		)
		if err != nil || channel == nil || account == nil {
			return false
		}
		*route = responseRoute{
			Channel:               *channel,
			Account:               account,
			UserID:                authSnapshot.UserId,
			SubscriptionAccountID: account.ID,
		}
		return true
	case relaybiz.UpstreamRouteChannel:
		if s.channelClient == nil {
			return false
		}
		chInfo, err := s.channelClient.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: source.id})
		if err != nil || chInfo == nil || chInfo.Channel == nil {
			return false
		}
		ch := relaybiz.Channel{
			ID:              chInfo.Channel.Id,
			Type:            chInfo.Channel.Type,
			Name:            chInfo.Channel.Name,
			Status:          chInfo.Channel.Status,
			BaseURL:         chInfo.Channel.BaseUrl,
			Group:           chInfo.Channel.Group,
			Priority:        chInfo.Channel.Priority,
			Weight:          chInfo.Channel.Weight,
			Key:             chInfo.Channel.Key,
			ModelMapping:    chInfo.Channel.ModelMapping,
			UpstreamModelID: chInfo.Channel.UpstreamModelId,
			RestrictModels:  chInfo.Channel.RestrictModels,
		}
		if chInfo.Channel.Config != nil {
			ch.Config = relaybiz.ChannelConfig{APIVersion: chInfo.Channel.Config.ApiVersion}
		}
		*route = responseRoute{Channel: ch, UserID: authSnapshot.UserId}
		return true
	default:
		return false
	}
}
