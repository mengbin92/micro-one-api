package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	coderws "github.com/coder/websocket"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/routing"
	"micro-one-api/domain/upstream/evidence"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/metrics"
)

const stepAudioRealtimeModel = "stepaudio-2.5-realtime"

type stepAudioSession struct {
	plan         *relaybiz.RelayPlan
	channel      *relaybiz.Channel
	endpoint     string
	reportHealth func(context.Context, error)
}

type stepAudioRealtimeTurn struct {
	reservation *billingv1.ReserveQuotaResponse
	observation evidence.Sink
	expected    bool
	sent        atomic.Bool
	responseID  string
	settling    bool
	audioAck    chan string
	audioAcked  bool
}

type stepAudioRealtimeTurns struct {
	mu            sync.Mutex
	active        *stepAudioRealtimeTurn
	admit         func(context.Context, int64) (*stepAudioRealtimeTurn, error)
	transcribe    atomic.Bool
	clientGone    atomic.Bool
	manualCommits atomic.Int32
}

func (t *stepAudioRealtimeTurns) start(ctx context.Context, inputBytes int64, audioAck chan string) (*stepAudioRealtimeTurn, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.active != nil {
		return nil, fmt.Errorf("%w: previous response still active", errWSRoutingAdmission)
	}
	turn, err := t.admit(ctx, inputBytes)
	if err == nil {
		turn.audioAck = audioAck
		t.active = turn
	}
	return turn, err
}

func (t *stepAudioRealtimeTurns) current() *stepAudioRealtimeTurn {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

func (t *stepAudioRealtimeTurns) clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active = nil
}

func (s *HTTPServer) handleStepAudioRealtime(w http.ResponseWriter, r *http.Request) {
	s.handleStepAudioWS(w, r, "/realtime", stepAudioRealtimeModel, s.bridgeStepAudioRealtime)
}

func (s *HTTPServer) handleStepAudioWS(w http.ResponseWriter, r *http.Request, path, model string, bridge func(context.Context, openAIWSFrameConn, openAIWSFrameConn, stepAudioSession) error) {
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
	if err != nil || len(q) != 1 || len(q["model"]) != 1 || q.Get("model") != model {
		s.writeError(w, http.StatusBadRequest, model+" is required")
		return
	}
	if s.IsWSDraining() {
		s.writeError(w, http.StatusServiceUnavailable, "server is draining websocket connections")
		return
	}
	ctx := r.Context()
	clientIP := relayClientIP(r)
	plan, err := s.relayUsecase.Plan(ctx, relaybiz.RelayRequest{ClientIP: clientIP, Token: token, Model: model})
	if err != nil {
		s.handleRelayPlanError(w, err)
		return
	}
	channel := plan.Channel
	if plan.ResolvedModel != model || channel == nil || !stepAudioChannel(channel) || plan.Auth.RoutingContext == nil {
		s.writeError(w, http.StatusServiceUnavailable, "realtime routing unavailable")
		return
	}
	channel, err = s.relayUsecase.AdmitUpstreamExecution(ctx, channel)
	if err != nil {
		s.writeError(w, http.StatusServiceUnavailable, "realtime routing unavailable")
		return
	}
	started := time.Now()
	var healthOnce sync.Once
	reportHealth := func(call context.Context, result error) {
		healthOnce.Do(func() {
			s.relayUsecase.RecordUpstreamResult(call, channel, result, time.Since(started).Milliseconds(), started)
		})
	}
	defer func() {
		if channel.HealthProbeID != "" {
			reportHealth(ctx, relayprovider.BeforeForward(errWSRoutingAdmission))
		}
	}()
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
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		tracked := s.wsConnTracker.NewConnection(rootRequestID(r), func() error {
			cancel()
			return client.CloseNow()
		})
		if tracked == nil {
			closeOpenAIWSClientConn(client, coderws.StatusTryAgainLater, "server is draining websocket connections")
			return
		}
		tracked.SetMetadata("endpoint", "/v1"+path)
		tracked.SetMetadata("model", model)
		defer tracked.Unregister()
	}
	client.SetReadLimit(8 << 20)
	target, _ := url.Parse(channel.BaseURL) // Validated by stepAudioChannel.
	target.Scheme, target.RawQuery = "wss", q.Encode()
	target.Path = strings.TrimRight(target.Path, "/") + path
	upstream, err := s.dialStepAudioRealtime(ctx, target.String(), channel.Key)
	if err != nil {
		reportHealth(ctx, err)
		closeOpenAIWSClientConn(client, coderws.StatusTryAgainLater, "upstream realtime unavailable")
		return
	}
	defer upstream.CloseNow()
	if model == routing.StepAudioTTSModel {
		// TTS done can carry the entire three-minute, base64-encoded audio stream.
		upstream.SetReadLimit(24 << 20)
	} else {
		upstream.SetReadLimit(8 << 20)
	}

	session := stepAudioSession{plan: plan, channel: channel, endpoint: target.EscapedPath(), reportHealth: reportHealth}
	err = bridge(ctx, &coderWSFrameConn{conn: client}, &coderWSFrameConn{conn: upstream}, session)
	if err == nil {
		closeOpenAIWSClientConn(client, coderws.StatusNormalClosure, "")
	} else if !errors.Is(err, context.Canceled) && coderws.CloseStatus(err) == -1 {
		closeOpenAIWSClientConn(client, coderws.StatusPolicyViolation, "realtime turn rejected or incomplete")
	}
}

func (s *HTTPServer) recheckStepAudioTurn(ctx context.Context, session stepAudioSession, model string) error {
	auth := session.plan.Auth
	if err := relaybiz.RecheckRoutingAdmission(ctx, auth, model); err != nil {
		return err
	}
	source := routing.Source{Kind: routing.Channel, ID: session.channel.ID}
	permission, err := s.checkStoredSource(ctx, auth.Group, model, source)
	if err != nil || !permission.Allowed || (permission.UpstreamModelID != "" && permission.UpstreamModelID != model) {
		return fmt.Errorf("audio source permission changed")
	}
	return s.checkUserRPM(ctx, auth.UserID)
}

func (s *HTTPServer) bridgeStepAudioRealtime(ctx context.Context, client, upstream openAIWSFrameConn, session stepAudioSession) error {
	plan, channel := session.plan, session.channel
	observationCtx := relaybiz.WithObservationDelivery(requesttrace.WithVerifiedUser(ctx, plan.Auth.UserID), s.observationDelivery)
	clientCtx, stopClient := context.WithCancel(ctx)
	defer stopClient()
	upstreamCtx, stopUpstream := context.WithCancel(context.WithoutCancel(ctx))
	defer stopUpstream()
	turns := &stepAudioRealtimeTurns{}
	turns.admit = func(ctx context.Context, inputBytes int64) (*stepAudioRealtimeTurn, error) {
		channelID := strconv.FormatInt(channel.ID, 10)
		if executionPilot(relaybiz.UpstreamSourceChannel, channelID, 0, stepAudioRealtimeModel) {
			return nil, fmt.Errorf("realtime audio does not support native usage settlement")
		}
		if err := s.recheckStepAudioTurn(ctx, session, stepAudioRealtimeModel); err != nil {
			return nil, err
		}
		requestID := generateRequestID()
		ctx = channelAttemptContext(ctx, requestID, 1, channel, stepAudioRealtimeModel)
		reservation, err := s.reserveQuota(ctx, strconv.FormatInt(plan.Auth.UserID, 10), requestID, inputBytes/4+relayRealtimeOutputReserve, stepAudioRealtimeModel, channelID, 0, plan.Auth.RoutingContext)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(reservation.GetReservationId()) == "" {
			return nil, fmt.Errorf("billing returned no realtime audio reservation ID")
		}
		turnCtx := channelAttemptContext(observationCtx, requestID, 1, channel, stepAudioRealtimeModel)
		turnCtx = relaybiz.WithUsageObservation(turnCtx, channel, requestID, stepAudioRealtimeModel)
		return &stepAudioRealtimeTurn{reservation: reservation, observation: evidence.ObservationSinkFrom(turnCtx), expected: evidence.ObservationExpected(turnCtx)}, nil
	}

	clientDone, upstreamDone := make(chan error, 1), make(chan error, 1)
	disconnect := func() {
		turns.clientGone.Store(true)
		stopClient()
	}
	go func() {
		err := s.relayStepAudioClient(clientCtx, client, upstream, turns, session)
		disconnect()
		clientDone <- err
	}()
	go func() {
		upstreamDone <- s.relayStepAudioEvents(upstreamCtx, clientCtx, client, upstream, turns, session, disconnect)
	}()
	var err error
	select {
	case err = <-upstreamDone:
		stopClient()
		<-clientDone
	case err = <-clientDone:
		s.drainStepAudioRealtime(turns.current(), upstreamDone, stopUpstream)
	}
	if pending := turns.current(); pending != nil {
		if pending.observation != nil {
			pending.observation.TransportFail(evidence.StepAudioRealtime, "truncated")
		}
		if !pending.settling {
			_ = s.releaseQuota(context.WithoutCancel(ctx), pending.reservation.ReservationId, "realtime ended without proven usage")
		}
	}
	return err
}

func (s *HTTPServer) drainStepAudioRealtime(turn *stepAudioRealtimeTurn, done <-chan error, stopUpstream context.CancelFunc) {
	if turn != nil && turn.sent.Load() {
		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case <-done:
			return
		case <-timer.C:
		}
	}
	stopUpstream()
	<-done
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

func (s *HTTPServer) writeStepAudioFrame(ctx context.Context, conn openAIWSFrameConn, kind coderws.MessageType, payload []byte) error {
	writeCtx, cancel := context.WithTimeout(ctx, s.openAIWSWriteTimeout())
	defer cancel()
	return conn.WriteFrame(writeCtx, kind, payload)
}

func (s *HTTPServer) readStepAudioFrame(ctx context.Context, conn openAIWSFrameConn) (coderws.MessageType, []byte, error) {
	readCtx, cancel := context.WithTimeout(ctx, s.openAIWSIdleTimeout())
	defer cancel()
	return conn.ReadFrame(readCtx)
}

func (s *HTTPServer) relayStepAudioClient(ctx context.Context, client, upstream openAIWSFrameConn, turns *stepAudioRealtimeTurns, session stepAudioSession) error {
	// Detection runs in Relay. Enabling supplier VAD would generate before the
	// per-turn reservation, as soon as a silence is detected upstream.
	initial := []byte(`{"type":"session.update","session":{"turn_detection":null}}`)
	if err := s.writeStepAudioFrame(ctx, upstream, coderws.MessageText, initial); err != nil {
		return err
	}
	var inputBytes int64
	var input stepAudioRealtimeInput
	var transcriptions chan stepAudioTranscription
	asrCtx, stopASR := context.WithCancel(ctx)
	asrDone := make(chan struct{})
	defer func() {
		stopASR()
		if transcriptions != nil {
			<-asrDone
		}
	}()
	const create = `{"type":"response.create"}`
	sendTurn := func(turn *stepAudioRealtimeTurn) error {
		sink := turn.observation
		metrics.RecordUsageObservationTransportSend(evidence.StepAudioRealtime, turn.expected, sink != nil)
		if sink != nil {
			sink.Started(evidence.StepAudioRealtime, session.endpoint, session.channel.Config.APIVersion)
		}
		turn.sent.Store(true)
		err := s.writeStepAudioFrame(ctx, upstream, coderws.MessageText, []byte(create))
		if err != nil && sink != nil {
			sink.TransportFail(evidence.StepAudioRealtime, "transport_error")
		}
		return err
	}
	speechStarted := func(item string, startMS int64) error {
		return s.writeStepAudioInputEvent(ctx, client, map[string]any{"type": "input_audio_buffer.speech_started", "item_id": item, "audio_start_ms": startMS})
	}
	utterance := func(pcm []byte, item string, endMS int64) error {
		if err := s.writeStepAudioInputEvent(ctx, client, map[string]any{"type": "input_audio_buffer.speech_stopped", "item_id": item, "audio_end_ms": endMS}); err != nil {
			return err
		}
		var ack chan string
		if input.transcribe {
			ack = make(chan string, 1)
		}
		turn, err := turns.start(ctx, inputBytes, ack)
		if err != nil {
			return err
		}
		if err := s.writeStepAudioInputEvent(ctx, upstream, map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(pcm)}); err != nil {
			if turn.observation != nil {
				turn.observation.NotSent(evidence.StepAudioRealtime, evidence.NotSentTransportRefused)
			}
			return err
		}
		if err := s.writeStepAudioFrame(ctx, upstream, coderws.MessageText, []byte(`{"type":"input_audio_buffer.commit"}`)); err != nil {
			if turn.observation != nil {
				turn.observation.NotSent(evidence.StepAudioRealtime, evidence.NotSentTransportRefused)
			}
			return err
		}
		if err := sendTurn(turn); err != nil {
			return err
		}
		if ack != nil {
			if transcriptions == nil {
				transcriptions = make(chan stepAudioTranscription, 1)
				go func(jobs <-chan stepAudioTranscription) {
					defer close(asrDone)
					s.relayStepAudioTranscriptions(asrCtx, client, session, jobs)
				}(transcriptions)
			}
			select {
			case transcriptions <- stepAudioTranscription{pcm: pcm, ack: ack, localItem: item}:
			default:
				return s.writeStepAudioTranscriptionFailure(ctx, client, item)
			}
		}
		return nil
	}
	for {
		kind, payload, err := s.readStepAudioFrame(ctx, client)
		if err != nil {
			return err
		}
		if kind != coderws.MessageText {
			return errWSRoutingAdmission
		}
		previousVAD, previousTranscription := input.vad, input.transcribe
		event, payload, err := stepAudioRealtimeClientEvent(payload, &input)
		if err != nil {
			return err
		}
		inputBytes += int64(len(payload))
		if inputBytes > 16<<20 {
			return fmt.Errorf("realtime input limit reached")
		}
		switch event {
		case "session.update":
			if (previousVAD != input.vad || previousTranscription != input.transcribe) && turns.manualCommits.Load() != 0 {
				return fmt.Errorf("%w: wait for the manual input commit acknowledgement", errWSRoutingAdmission)
			}
			turns.transcribe.Store(input.transcribe)
		case "input_audio_buffer.append":
			if input.vad == nil && !input.transcribe {
				input.upstreamBuffered = true
				break
			}
			pcm, err := decodeStepAudioPCM(payload)
			if err != nil {
				return err
			}
			if err := input.push(pcm, speechStarted, utterance); err != nil {
				return err
			}
			continue
		case "input_audio_buffer.commit":
			switch {
			case input.vad != nil:
				continue
			case input.transcribe:
				if len(input.pcm) == 0 {
					return fmt.Errorf("transcription audio is empty")
				}
				item := "item_" + generateRequestID()
				if err := s.writeStepAudioInputEvent(ctx, client, map[string]any{"type": "input_audio_buffer.committed", "item_id": item}); err != nil {
					return err
				}
				if err := s.writeStepAudioInputEvent(ctx, client, map[string]any{
					"type": "conversation.item.created",
					"item": map[string]any{
						"id": item, "type": "message", "role": "user", "status": "completed",
						"content": []map[string]any{{"type": "input_audio"}},
					},
				}); err != nil {
					return err
				}
				if err := s.transcribeStepAudioItem(ctx, client, session, input.pcm, item); err != nil {
					return err
				}
				input.pcm = nil
				continue
			}
			turns.manualCommits.Add(1)
			input.upstreamBuffered = false
		case "input_audio_buffer.clear":
			input.pcm = nil
			input.upstreamBuffered = false
			if vad := input.vad; vad != nil {
				vad.tail, vad.preRoll, vad.itemID, vad.quietMS = nil, nil, "", 0
			}
		case "response.create":
			turn, err := turns.start(ctx, inputBytes, nil)
			if err != nil {
				return err
			}
			if err := sendTurn(turn); err != nil {
				return err
			}
			continue
		}
		if err := s.writeStepAudioFrame(ctx, upstream, kind, payload); err != nil {
			return err
		}
	}
}

func stepAudioRealtimeClientEvent(raw []byte, input *stepAudioRealtimeInput) (string, []byte, error) {
	var payload map[string]jsonx.RawMessage
	var kind string
	if jsonx.Unmarshal(raw, &payload) != nil || jsonx.Unmarshal(payload["type"], &kind) != nil {
		return "", nil, errWSRoutingAdmission
	}
	switch kind {
	case "session.update":
		var session map[string]jsonx.RawMessage
		if jsonx.Unmarshal(payload["session"], &session) != nil || session == nil {
			return "", nil, errWSRoutingAdmission
		}
		for key := range session {
			if strings.EqualFold(key, "model") ||
				(strings.EqualFold(key, "turn_detection") && key != "turn_detection") ||
				(strings.EqualFold(key, "input_audio_transcription") && key != "input_audio_transcription") ||
				(strings.EqualFold(key, "input_audio_format") && key != "input_audio_format") {
				return "", nil, errWSRoutingAdmission
			}
		}
		if err := input.configure(session); err != nil {
			return "", nil, err
		}
		payload = map[string]jsonx.RawMessage{"type": payload["type"]}
		payload["session"], _ = jsonx.Marshal(session)
	case "response.create":
		// This upstream uses the session model from the URL. Keep a create
		// event free of alternate model or automatic-generation directives.
		payload = map[string]jsonx.RawMessage{"type": payload["type"]}
	case "response.cancel", "input_audio_buffer.append", "input_audio_buffer.commit", "input_audio_buffer.clear", "conversation.item.create", "conversation.item.delete", "conversation.item.truncate":
		for key := range payload {
			if strings.EqualFold(key, "type") && key != "type" {
				return "", nil, errWSRoutingAdmission
			}
		}
	default:
		return "", nil, errWSRoutingAdmission
	}
	out, err := jsonx.Marshal(payload)
	return kind, out, err
}

func (s *HTTPServer) relayStepAudioEvents(ctx, clientCtx context.Context, client, upstream openAIWSFrameConn, turns *stepAudioRealtimeTurns, session stepAudioSession, disconnect func()) error {
	seenResponses := make(map[string]struct{})
	for {
		kind, payload, err := s.readStepAudioFrame(ctx, upstream)
		if err != nil {
			return err
		}
		var event struct {
			Type       string `json:"type"`
			ResponseID string `json:"response_id"`
			ItemID     string `json:"item_id"`
			Response   struct {
				ID string `json:"id"`
			} `json:"response"`
		}
		if kind != coderws.MessageText || jsonx.Unmarshal(payload, &event) != nil {
			return fmt.Errorf("invalid upstream realtime event")
		}
		if turns.transcribe.Load() && strings.HasPrefix(event.Type, "conversation.item.input_audio_transcription.") {
			continue
		}
		if event.Type == "input_audio_buffer.committed" && event.ItemID != "" && len(event.ItemID) <= 256 {
			if turns.manualCommits.Load() > 0 {
				turns.manualCommits.Add(-1)
			} else if turn := turns.current(); turn != nil && turn.audioAck != nil && !turn.audioAcked {
				turn.audioAcked = true
				turn.audioAck <- event.ItemID
			}
		}
		var turn *stepAudioRealtimeTurn
		if strings.HasPrefix(event.Type, "response.") {
			turn = turns.current()
			if turn == nil || !turn.sent.Load() {
				return fmt.Errorf("unexpected upstream response without reservation")
			}
			id := event.ResponseID
			if event.Type == "response.created" || event.Type == "response.done" {
				id = event.Response.ID
				if id == "" || len(id) > 256 {
					return fmt.Errorf("upstream realtime response id missing or invalid")
				}
			}
			if id != "" {
				_, alreadySettled := seenResponses[id]
				if len(id) > 256 || alreadySettled || (turn.responseID != "" && turn.responseID != id) {
					return fmt.Errorf("upstream realtime response id mismatch")
				}
				turn.responseID = id
			}
		}
		if event.Type == "response.done" {
			if turn.observation != nil {
				observation := evidence.Parse(payload, evidence.StepAudioRealtime)
				turn.observation.Terminal(evidence.Record{Observation: observation, Stream: true, Complete: true})
			}
			if err := s.settleStepAudioRealtime(ctx, session, turn, payload); err != nil {
				return err
			}
			seenResponses[turn.responseID] = struct{}{}
			turns.clear()
		}
		if !turns.clientGone.Load() {
			if err := s.writeStepAudioFrame(clientCtx, client, kind, payload); err != nil {
				disconnect()
			}
		}
		if turns.clientGone.Load() && turns.current() == nil {
			return nil
		}
		if len(seenResponses) == 1024 {
			return fmt.Errorf("realtime response limit reached")
		}
	}
}

func (s *HTTPServer) settleStepAudioRealtime(ctx context.Context, session stepAudioSession, turn *stepAudioRealtimeTurn, payload []byte) error {
	var root, response map[string]jsonx.RawMessage
	var kind, id string
	if jsonx.ValidateUnique(payload) != nil || jsonx.Unmarshal(payload, &root) != nil ||
		jsonx.Unmarshal(root["type"], &kind) != nil || kind != "response.done" ||
		jsonx.Unmarshal(root["response"], &response) != nil || jsonx.Unmarshal(response["id"], &id) != nil || id != turn.responseID {
		return fmt.Errorf("invalid upstream realtime terminal")
	}
	failed := false
	if raw, exists := response["status"]; exists {
		var status string
		if jsonx.Unmarshal(raw, &status) != nil {
			return fmt.Errorf("invalid upstream realtime terminal status")
		}
		switch status {
		case "completed":
		case "cancelled", "canceled", "failed", "incomplete":
			failed = true
		default:
			return fmt.Errorf("invalid upstream realtime terminal status")
		}
	}
	reservation := turn.reservation
	raw := response["usage"]
	if failed && (len(raw) == 0 || string(raw) == "null") {
		return s.releaseQuota(ctx, reservation.ReservationId, "realtime upstream terminal without usage")
	}
	var usage map[string]jsonx.RawMessage
	var input, output, total *int64
	if jsonx.Unmarshal(raw, &usage) != nil ||
		jsonx.Unmarshal(usage["input_tokens"], &input) != nil || input == nil ||
		jsonx.Unmarshal(usage["output_tokens"], &output) != nil || output == nil ||
		jsonx.Unmarshal(usage["total_tokens"], &total) != nil || total == nil ||
		*input < 0 || *output < 0 || *total < 0 || *output > *total || *input != *total-*output ||
		(*total == 0 && !failed) {
		return fmt.Errorf("invalid upstream realtime usage")
	}
	cached := int64(0)
	if raw := usage["input_token_details"]; len(raw) != 0 {
		var details map[string]jsonx.RawMessage
		if jsonx.Unmarshal(raw, &details) != nil {
			return fmt.Errorf("invalid upstream realtime input token details")
		}
		if raw := details["cached_tokens"]; len(raw) != 0 {
			var count *int64
			if jsonx.Unmarshal(raw, &count) != nil || count == nil || *count < 0 || *count > *input {
				return fmt.Errorf("invalid upstream realtime cached tokens")
			}
			cached = *count
		}
	}
	if *total == 0 {
		return s.releaseQuota(ctx, reservation.ReservationId, "realtime upstream terminal with zero usage")
	}
	if !failed && session.reportHealth != nil {
		session.reportHealth(ctx, nil)
	}
	turn.settling = true
	auth := session.plan.Auth
	info := usageLogInput{
		UserID: auth.UserID, TokenID: auth.TokenID, TokenName: auth.TokenName,
		ModelName: stepAudioRealtimeModel, UpstreamModelID: stepAudioRealtimeModel, ChannelID: session.channel.ID, RequestID: reservation.RequestId,
		SourceKind: relaybiz.UpstreamSourceChannel, Quota: *total, PromptTokens: *input, CompletionTokens: *output,
		CacheReadTokens: cached, IsStream: true, Endpoint: "/v1/realtime",
	}
	info.applyReservation(reservation)
	if err := s.commitQuota(ctx, reservation.ReservationId, *total, true, info); err != nil {
		return err
	}
	s.ingestUsageLogAfterResponse(info, ctx)
	return nil
}
