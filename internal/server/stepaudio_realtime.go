package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	coderws "github.com/coder/websocket"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

const stepAudioRealtimeModel = "stepaudio-2.5-realtime"

func (s *HTTPServer) handleStepAudioRealtime(w http.ResponseWriter, r *http.Request) {
	if !isOpenAIWSUpgradeRequest(r) || r.Method != http.MethodGet {
		s.writeError(w, http.StatusBadRequest, "a WebSocket upgrade is required")
		return
	}
	token, err := bearerTokenFromRequest(r)
	if err != nil {
		s.writeError(w, http.StatusUnauthorized, "missing API key")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 1 || len(q["model"]) != 1 || q.Get("model") != stepAudioRealtimeModel {
		s.writeError(w, http.StatusBadRequest, "stepaudio-2.5-realtime is required")
		return
	}
	if s.IsWSDraining() {
		s.writeError(w, http.StatusServiceUnavailable, "server is draining websocket connections")
		return
	}
	ctx := r.Context()
	plan, err := s.relayUsecase.Plan(ctx, relaybiz.RelayRequest{ClientIP: relayClientIP(r), Token: token, Model: stepAudioRealtimeModel})
	if err != nil {
		s.handleRelayPlanError(w, err)
		return
	}
	channel := plan.Channel
	if plan.ResolvedModel != stepAudioRealtimeModel || channel == nil || !stepAudioChannel(channel) || plan.Auth.RoutingContext == nil {
		s.writeError(w, http.StatusServiceUnavailable, "realtime routing unavailable")
		return
	}
	channel, err = s.relayUsecase.AdmitUpstreamExecution(ctx, channel)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "realtime routing unavailable")
		return
	}
	release, err := s.relayUsecase.AcquireChannelSlot(ctx, channel)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "realtime channel busy")
		return
	}
	defer release()

	client, err := coderws.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer client.CloseNow()
	if s.wsConnTracker != nil {
		tracked := s.wsConnTracker.NewConnection(rootRequestID(r), client.CloseNow)
		if tracked == nil {
			closeOpenAIWSClientConn(client, coderws.StatusTryAgainLater, "realtime connection tracker full")
			return
		}
		tracked.SetMetadata("endpoint", "/v1/realtime")
		tracked.SetMetadata("model", stepAudioRealtimeModel)
		defer tracked.Unregister()
	}
	client.SetReadLimit(8 << 20)
	upstreamURL := "wss://" + strings.TrimPrefix(strings.TrimRight(channel.BaseURL, "/"), "https://") + "/realtime?" + q.Encode()
	upstream, err := s.dialStepAudioRealtime(ctx, upstreamURL, channel.Key)
	if err != nil {
		closeOpenAIWSClientConn(client, coderws.StatusTryAgainLater, "upstream realtime unavailable")
		return
	}
	defer upstream.CloseNow()
	upstream.SetReadLimit(8 << 20)

	bridgeCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	source := routing.Source{Kind: routing.Channel, ID: channel.ID}
	var inputBytes int64
	turns := &routingWSTurns{first: true}
	turns.admit = func(ctx context.Context, payload []byte) (*billingv1.ReserveQuotaResponse, []byte, error) {
		if err := relaybiz.RecheckRoutingAdmission(ctx, plan.Auth, stepAudioRealtimeModel); err != nil {
			return nil, nil, err
		}
		permission, err := s.checkStoredSource(ctx, plan.Auth.Group, stepAudioRealtimeModel, source)
		if err != nil || !permission.Allowed {
			return nil, nil, fmt.Errorf("realtime source permission changed")
		}
		if err := s.checkUserRPM(ctx, plan.Auth.UserID); err != nil {
			return nil, nil, err
		}
		requestID := generateRequestID()
		ctx = channelAttemptContext(ctx, requestID, 1, channel, stepAudioRealtimeModel)
		reservation, err := s.reserveQuota(ctx, strconv.FormatInt(plan.Auth.UserID, 10), requestID, inputBytes/4+relayRealtimeOutputReserve, stepAudioRealtimeModel, strconv.FormatInt(channel.ID, 10), 0, plan.Auth.RoutingContext)
		return reservation, payload, err
	}

	errs := make(chan error, 2)
	go func() {
		errs <- s.relayStepAudioClient(bridgeCtx, client, upstream, turns, &inputBytes)
	}()
	go func() {
		errs <- s.relayStepAudioEvents(bridgeCtx, client, upstream, turns, plan, channel.ID)
	}()
	err = <-errs
	cancel()
	<-errs
	if pending := turns.take(); pending != nil {
		_ = s.releaseQuota(context.WithoutCancel(ctx), pending.ReservationId, "realtime ended without proven usage")
	}
	if err != nil && !errors.Is(err, context.Canceled) && coderws.CloseStatus(err) == -1 {
		closeOpenAIWSClientConn(client, coderws.StatusPolicyViolation, "realtime turn rejected or incomplete")
	}
}

func (s *HTTPServer) dialStepAudioRealtime(ctx context.Context, upstreamURL, key string) (*coderws.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	upstream, _, err := coderws.Dial(dialCtx, upstreamURL, &coderws.DialOptions{
		HTTPClient: s.apiKeyHTTPClient,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + key}},
	})
	return upstream, err
}

const relayRealtimeOutputReserve int64 = 8192

func (s *HTTPServer) writeStepAudioFrame(ctx context.Context, conn *coderws.Conn, kind coderws.MessageType, payload []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, s.openAIWSWriteTimeout())
	defer cancel()
	return conn.Write(writeCtx, kind, payload)
}

func (s *HTTPServer) relayStepAudioClient(ctx context.Context, client, upstream *coderws.Conn, turns *routingWSTurns, inputBytes *int64) error {
	// Disable server VAD so every generated response has a corresponding
	// client turn and a pre-existing reservation.
	initial := []byte(`{"type":"session.update","session":{"turn_detection":null}}`)
	if err := s.writeStepAudioFrame(ctx, upstream, coderws.MessageText, initial); err != nil {
		return err
	}
	for {
		kind, payload, err := client.Read(ctx)
		if err != nil {
			return err
		}
		if kind != coderws.MessageText {
			return errWSRoutingAdmission
		}
		payload, err = stepAudioRealtimeClientEvent(payload)
		if err != nil {
			return err
		}
		*inputBytes += int64(len(payload))
		if *inputBytes > 16<<20 {
			return fmt.Errorf("realtime input limit reached")
		}
		payload, err = turns.beforeWrite(ctx, kind, payload)
		if err != nil {
			return err
		}
		if err := s.writeStepAudioFrame(ctx, upstream, kind, payload); err != nil {
			return err
		}
	}
}

func stepAudioRealtimeClientEvent(raw []byte) ([]byte, error) {
	var payload map[string]jsonx.RawMessage
	var kind string
	if jsonx.Unmarshal(raw, &payload) != nil || jsonx.Unmarshal(payload["type"], &kind) != nil {
		return nil, errWSRoutingAdmission
	}
	switch kind {
	case "session.update":
		var session map[string]jsonx.RawMessage
		if jsonx.Unmarshal(payload["session"], &session) != nil || session == nil {
			return nil, errWSRoutingAdmission
		}
		for key, value := range session {
			if strings.EqualFold(key, "model") || ((strings.EqualFold(key, "turn_detection") || strings.EqualFold(key, "input_audio_transcription")) && !bytes.Equal(bytes.TrimSpace(value), []byte("null"))) {
				return nil, errWSRoutingAdmission
			}
		}
		session["turn_detection"] = jsonx.RawMessage("null")
		payload = map[string]jsonx.RawMessage{"type": payload["type"]}
		payload["session"], _ = jsonx.Marshal(session)
	case "response.create":
		// This upstream uses the session model from the URL. Keep a create
		// event free of alternate model or automatic-generation directives.
		payload = map[string]jsonx.RawMessage{"type": payload["type"]}
	case "response.cancel", "input_audio_buffer.append", "input_audio_buffer.commit", "input_audio_buffer.clear", "conversation.item.create", "conversation.item.delete", "conversation.item.truncate":
		for key := range payload {
			if strings.EqualFold(key, "type") && key != "type" {
				return nil, errWSRoutingAdmission
			}
		}
	default:
		return nil, errWSRoutingAdmission
	}
	return jsonx.Marshal(payload)
}

func (s *HTTPServer) relayStepAudioEvents(ctx context.Context, client, upstream *coderws.Conn, turns *routingWSTurns, plan *relaybiz.RelayPlan, channelID int64) error {
	for {
		kind, payload, err := upstream.Read(ctx)
		if err != nil {
			return err
		}
		var event struct {
			Type     string `json:"type"`
			Response struct {
				Usage *struct {
					Input   int64 `json:"input_tokens"`
					Output  int64 `json:"output_tokens"`
					Total   int64 `json:"total_tokens"`
					Details struct {
						Cached int64 `json:"cached_tokens"`
					} `json:"input_token_details"`
				} `json:"usage"`
			} `json:"response"`
		}
		if kind != coderws.MessageText || jsonx.Unmarshal(payload, &event) != nil {
			return fmt.Errorf("invalid upstream realtime event")
		}
		if strings.HasPrefix(event.Type, "response.") && !turns.hasActive() {
			return fmt.Errorf("unexpected upstream response without reservation")
		}
		if event.Type == "response.done" {
			u := event.Response.Usage
			if u == nil || u.Total <= 0 || u.Input < 0 || u.Output < 0 || u.Output > u.Total || u.Input != u.Total-u.Output || u.Details.Cached < 0 || u.Details.Cached > u.Input {
				return fmt.Errorf("invalid upstream realtime usage")
			}
			reservation := turns.take()
			if reservation == nil {
				return fmt.Errorf("unexpected upstream realtime completion")
			}
			info := usageLogInput{
				UserID: plan.Auth.UserID, TokenID: plan.Auth.TokenID, TokenName: plan.Auth.TokenName,
				ModelName: stepAudioRealtimeModel, UpstreamModelID: stepAudioRealtimeModel, ChannelID: channelID, RequestID: reservation.ReservationId,
				SourceKind: relaybiz.UpstreamSourceChannel, Quota: u.Total, PromptTokens: u.Input, CompletionTokens: u.Output,
				CacheReadTokens: u.Details.Cached, IsStream: true, Endpoint: "/v1/realtime",
			}
			info.applyReservation(reservation)
			if err := s.commitQuota(ctx, reservation.ReservationId, u.Total, true, info); err != nil {
				return err
			}
			s.ingestUsageLog(ctx, info)
		}
		if err := s.writeStepAudioFrame(ctx, client, kind, payload); err != nil {
			return err
		}
	}
}
