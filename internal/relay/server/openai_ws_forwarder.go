package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	coderws "github.com/coder/websocket"
	"go.uber.org/zap"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	servererrors "micro-one-api/internal/pkg/errors"
	applogger "micro-one-api/internal/pkg/logger"
	relaybiz "micro-one-api/internal/relay/biz"
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
	// Accept the upgrade. Compression with context takeover matches both the
	// Codex CLI and the sub2api upstream dialer.
	wsConn, err := coderws.Accept(w, r, &coderws.AcceptOptions{
		CompressionMode: coderws.CompressionContextTakeover,
	})
	if err != nil {
		if applogger.Log != nil {
			applogger.Log.Warn("failed to accept openai responses websocket upgrade", zap.Error(err))
		}
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
			Token: token,
			Model: clientModel,
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

	rewrittenFirstMessage := rewriteOpenAIWSModel(firstMessage, clientModel, plan.ResolvedModel)

	// Reservations mirror the HTTP path: estimate tokens from the request body
	// and commit per terminal turn.
	requestID := generateRequestID()
	reservation, err := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), requestID, estimateRawTokens(rewrittenFirstMessage), plan.ResolvedModel, fmt.Sprintf("%d", plan.Channel.ID), subscriptionAccountIDFromPlan(plan))
	if err != nil {
		closeOpenAIWSClientConn(wsConn, coderws.StatusTryAgainLater, "quota reservation failed")
		return
	}

	// Dial the upstream via the connection pool (reuses an idle conn for this
	// channel when available, otherwise dials fresh) and run the relay with
	// multi-channel failover: on a retryable upstream error (dial failure or a
	// relay error before any data reached the client) we switch to a different
	// channel and retry, up to the configured switch limit.
	maxSwitches := s.openAIWSFailoverMaxSwitches()
	s.runResponsesWSRelayWithFailover(ctx, wsConn, clientFrameConn, r, token, clientModel, sessionHash, plan, rewrittenFirstMessage, reservation, requestID, maxSwitches)
}

// buildOpenAIWSUpstreamTarget computes the upstream Responses WebSocket URL and
// request headers for the selected channel. The channel's base URL (already
// normalized by the provider factory to https) is converted to wss/ws, and the
// Authorization + OpenAI-Beta headers are set.
func (s *HTTPServer) buildOpenAIWSUpstreamTarget(r *http.Request, ch *relaybiz.Channel) (string, http.Header, error) {
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

	headers := make(http.Header)
	headers.Set("Authorization", "Bearer "+ch.Key)
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
	node, _ := sonic.Get(message, "model")
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
	if err := sonic.Unmarshal(message, &payload); err != nil {
		return message
	}
	payload["model"] = resolvedModel
	rewritten, err := sonic.Marshal(payload)
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
	if node, _ := sonic.Get(payload, "response", "id"); node.Exists() {
		if rid, _ := node.String(); strings.TrimSpace(rid) != "" {
			return strings.TrimSpace(rid)
		}
	}
	if node, _ := sonic.Get(payload, "response_id"); node.Exists() {
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
	node, _ := sonic.Get(payload, "previous_response_id")
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
		node, _ := sonic.Get(payload, key)
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
	token string,
	clientModel string,
	sessionHash string,
	plan *relaybiz.RelayPlan,
	rewrittenFirstMessage []byte,
	reservation *billingv1.ReserveQuotaResponse,
	requestID string,
	maxSwitches int,
) {
	currentChannel := plan.Channel
	resolvedModel := plan.ResolvedModel

	for attempt := 0; ; attempt++ {
		// Resolve the upstream target for the current channel.
		wsURL, headers, err := s.buildOpenAIWSUpstreamTarget(r, currentChannel)
		if err != nil {
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream target error")
			closeOpenAIWSClientConn(wsConn, coderws.StatusInternalError, "failed to build upstream websocket target")
			return
		}

		// Acquire a (possibly pooled) upstream connection.
		pooledConn, err := s.acquireOpenAIWSUpstreamConn(ctx, currentChannel.ID, wsURL, headers)
		if err != nil {
			// Dial failed. Try failover if we haven't exhausted switches.
			// Capture the failed channel id before maybeFailoverChannel mutates
			// currentChannel, otherwise the log records the channel we switched to.
			failedChannelID := currentChannel.ID
			if attempt < maxSwitches && s.maybeFailoverChannel(ctx, wsConn, plan, currentChannel, clientModel, &currentChannel) {
				if applogger.Log != nil {
					applogger.Log.Info("openai ws failover after dial error",
						zap.String("request_id", requestID),
						zap.Int("attempt", attempt+1),
						zap.Int64("failed_channel", failedChannelID),
						zap.Error(err),
					)
				}
				continue
			}
			_ = s.releaseQuota(ctx, reservation.ReservationId, "upstream dial error")
			closeOpenAIWSClientConn(wsConn, coderws.StatusInternalError, "upstream dial failed")
			return
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
				ModelName:             resolvedModel,
				Quota:                 actualTotal,
				PromptTokens:          usage.promptTokens,
				CompletionTokens:      usage.completionTokens,
				CacheReadTokens:       usage.cacheReadTokens,
				ChannelID:             currentChannel.ID,
				SubscriptionAccountID: subscriptionAccountIDFromPlan(plan),
				IsStream:              true,
			}
			logUpstreamUsage(logInput)
			// Each turn commits against its own reservation. The connection-level
			// reservation only covers the first turn; a Responses WebSocket is
			// long-lived and multi-turn, so reusing one reservation id for every
			// turn would under-bill (or double-commit) every turn after the first.
			turnReservationID := reservation.ReservationId
			if turnCommits > 0 {
				turnReservationID = ""
				if s.billingClient != nil {
					if turnRes, rerr := s.reserveQuota(ctx, fmt.Sprintf("%d", plan.Auth.UserID), turnID, actualTotal, resolvedModel, fmt.Sprintf("%d", currentChannel.ID), subscriptionAccountIDFromPlan(plan)); rerr == nil && turnRes != nil {
						turnReservationID = turnRes.ReservationId
					} else if applogger.Log != nil {
						applogger.Log.Warn("failed to reserve openai ws turn quota",
							zap.String("request_id", turnID),
							zap.Error(rerr),
						)
					}
				}
			}
			if turnReservationID != "" {
				if commitErr := s.commitQuotaAfterResponse(turnReservationID, actualTotal, true, logInput); commitErr != nil {
					if applogger.Log != nil {
						applogger.Log.Warn("failed to commit openai ws turn quota",
							zap.String("request_id", turnID),
							zap.Error(commitErr),
						)
					}
				} else {
					s.ingestUsageLogAfterResponse(logInput)
				}
			}
			// Bind the upstream response id -> channel both locally and in the
			// cross-process sticky store (Redis) so multi-replica deployments
			// resume the chain on the same channel.
			if turn.requestID != "" {
				s.storeResponseRoute(turn.requestID, responseRoute{
					Model:         clientModel,
					ResolvedModel: resolvedModel,
					Channel:       *currentChannel,
					UserID:        plan.Auth.UserID,
				})
				if s.wsSticky != nil {
					s.wsSticky.BindResponseChannel(ctx, plan.Auth.Group, turn.requestID, currentChannel.ID, s.openAIWSStickyTTL())
				}
			}
			if s.wsSticky != nil && strings.TrimSpace(sessionHash) != "" {
				s.wsSticky.BindSessionChannel(ctx, plan.Auth.Group, sessionHash, currentChannel.ID, s.openAIWSStickyTTL())
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

		// Failover decision: only retry if nothing was written downstream yet
		// and we haven't exhausted switches. A relay that wrote bytes must
		// terminate; retrying would double-send to the client.
		canFailover := relayExit != nil &&
			relayExit.err != nil &&
			!relayExit.wroteDownstream &&
			turnCommits == 0 &&
			attempt < maxSwitches

		if canFailover {
			if applogger.Log != nil {
				applogger.Log.Info("openai ws failover after relay error",
					zap.String("request_id", requestID),
					zap.Int("attempt", attempt+1),
					zap.String("stage", relayExit.stage),
					zap.Int64("failed_channel", currentChannel.ID),
					zap.Error(relayExit.err),
				)
			}
			if s.maybeFailoverChannel(ctx, wsConn, plan, currentChannel, clientModel, &currentChannel) {
				continue
			}
		}

		// Terminal path: either success or unrecoverable failure.
		if turnCommits == 0 {
			if releaseErr := s.releaseQuota(ctx, reservation.ReservationId, "no completed ws turn"); releaseErr != nil && applogger.Log != nil {
				applogger.Log.Warn("failed to release openai ws reservation", zap.String("request_id", requestID), zap.Error(releaseErr))
			}
		}
		if relayExit != nil && relayExit.err != nil && applogger.Log != nil {
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
		return
	}
}

// acquireOpenAIWSUpstreamConn returns a usable upstream connection. It prefers
// the connection pool (reusing idle conns for the channel) and falls back to a
// direct dial when the pool is disabled (e.g. in tests).
func (s *HTTPServer) acquireOpenAIWSUpstreamConn(ctx context.Context, channelID int64, wsURL string, headers http.Header) (*openAIWSPooledConn, error) {
	if s.wsPool != nil {
		return s.wsPool.AcquireOrDial(ctx, channelID, wsURL, headers)
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
	pc := &openAIWSPooledConn{conn: conn, channelID: channelID, lastUsedAt: time.Now()}
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

// maybeFailoverChannel selects an alternative channel for the model/group,
// excluding the failed channel's priority tier so the selector picks a
// different upstream. On success it sets *next to the new channel and returns
// true; on failure (no alternative available) it returns false and the caller
// must surface the original error to the client.
func (s *HTTPServer) maybeFailoverChannel(ctx context.Context, wsConn *coderws.Conn, plan *relaybiz.RelayPlan, failed *relaybiz.Channel, clientModel string, next **relaybiz.Channel) bool {
	retryExecutor := s.relayUsecase.NewRetryExecutor()
	retryResult := retryExecutor.ExecuteWithInitialChannel(ctx, plan.Auth.Group, plan.ResolvedModel, failed, func(ctx context.Context, ch *relaybiz.Channel) error {
		// The executor selects a channel for us; we accept it by returning nil.
		// It excludes the failed channel's priority automatically.
		*next = ch
		return nil
	})
	if retryResult.Err != nil || *next == nil || (*next).ID == failed.ID {
		return false
	}
	return true
}

// lookupWSStickyRoute resolves a previous_response_id via the Redis-backed
// sticky store. On a channel-id hit it fetches the full channel info (via the
// channel gRPC client) and authenticates the token, then populates *route and
// returns true. Returns false on any miss/error (caller falls through to
// normal channel selection).
func (s *HTTPServer) lookupWSStickyRoute(ctx context.Context, token, responseID string, route *responseRoute) bool {
	if s == nil || s.wsSticky == nil || route == nil {
		return false
	}
	// Need the auth group to scope the Redis lookup.
	authSnapshot, err := s.getAuthSnapshot(ctx, token)
	if err != nil {
		return false
	}
	channelID := s.wsSticky.LookupResponseChannel(ctx, authSnapshot.Group, responseID)
	if channelID <= 0 {
		return false
	}
	chInfo, err := s.channelClient.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil || chInfo == nil || chInfo.Channel == nil {
		return false
	}
	ch := relaybiz.Channel{
		ID:       chInfo.Channel.Id,
		Type:     chInfo.Channel.Type,
		Name:     chInfo.Channel.Name,
		Status:   chInfo.Channel.Status,
		BaseURL:  chInfo.Channel.BaseUrl,
		Group:    chInfo.Channel.Group,
		Priority: chInfo.Channel.Priority,
		Key:      chInfo.Channel.Key,
	}
	if chInfo.Channel.Config != nil {
		ch.Config = relaybiz.ChannelConfig{APIVersion: chInfo.Channel.Config.ApiVersion}
	}
	*route = responseRoute{
		Channel: ch,
		UserID:  authSnapshot.UserId,
	}
	return true
}

func (s *HTTPServer) lookupWSStickySessionRoute(ctx context.Context, token, sessionHash string, route *responseRoute) bool {
	if s == nil || s.wsSticky == nil || route == nil {
		return false
	}
	authSnapshot, err := s.getAuthSnapshot(ctx, token)
	if err != nil {
		return false
	}
	channelID := s.wsSticky.LookupSessionChannel(ctx, authSnapshot.Group, sessionHash)
	if channelID <= 0 {
		return false
	}
	chInfo, err := s.channelClient.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: channelID})
	if err != nil || chInfo == nil || chInfo.Channel == nil {
		return false
	}
	ch := relaybiz.Channel{
		ID:       chInfo.Channel.Id,
		Type:     chInfo.Channel.Type,
		Name:     chInfo.Channel.Name,
		Status:   chInfo.Channel.Status,
		BaseURL:  chInfo.Channel.BaseUrl,
		Group:    chInfo.Channel.Group,
		Priority: chInfo.Channel.Priority,
		Key:      chInfo.Channel.Key,
	}
	if chInfo.Channel.Config != nil {
		ch.Config = relaybiz.ChannelConfig{APIVersion: chInfo.Channel.Config.ApiVersion}
	}
	*route = responseRoute{
		Channel: ch,
		UserID:  authSnapshot.UserId,
	}
	return true
}
