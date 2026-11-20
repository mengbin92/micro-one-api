package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	coderws "github.com/coder/websocket"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

const (
	stepAudioTTSEndpoint     = "/v1/realtime/audio"
	stepAudioTTSDrainTimeout = 5 * time.Second
)

type stepAudioTTSState struct {
	mu          sync.Mutex
	sessionID   string
	reservation *billingv1.ReserveQuotaResponse
	created     bool
	ready       bool
	inputDone   bool
	hasAudio    bool
	characters  int
	units       int64
	startedAt   time.Time
}

func (s *HTTPServer) handleStepAudioTTS(w http.ResponseWriter, r *http.Request) {
	s.handleStepAudioWS(w, r, "/realtime/audio", routing.StepAudioTTSModel, s.bridgeStepAudioTTS)
}

func (s *HTTPServer) bridgeStepAudioTTS(ctx context.Context, client, upstream openAIWSFrameConn, session stepAudioSession) error {
	// The supplier can finish after the downstream disappears. Keep its
	// reader alive long enough to receive the proof needed for settlement.
	upstreamCtx, cancelUpstream := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancelUpstream()
	clientCtx, cancelClient := context.WithCancel(ctx)
	defer cancelClient()
	state := &stepAudioTTSState{}
	clientDone, upstreamDone := make(chan error, 1), make(chan error, 1)
	go func() {
		clientDone <- s.readStepAudioTTSClient(clientCtx, upstreamCtx, client, upstream, session, state)
	}()
	go func() {
		upstreamDone <- s.readStepAudioTTSUpstream(upstreamCtx, clientCtx, cancelClient, client, upstream, session, state)
	}()
	var err error
	select {
	case err = <-upstreamDone:
		cancelUpstream()
		cancelClient()
		<-clientDone
	case err = <-clientDone:
		cancelClient()
		s.drainStepAudioTTS(upstreamCtx, cancelUpstream, upstream, state, upstreamDone)
	}
	if state.reservation != nil {
		_ = s.releaseQuota(context.WithoutCancel(ctx), state.reservation.ReservationId, "speech websocket ended without completion")
	}
	return err
}

func (s *HTTPServer) drainStepAudioTTS(ctx context.Context, cancelUpstream context.CancelFunc, upstream openAIWSFrameConn, state *stepAudioTTSState, done <-chan error) {
	drainCtx, cancel := context.WithTimeout(ctx, stepAudioTTSDrainTimeout)
	defer cancel()
	state.mu.Lock()
	wait := state.reservation != nil && (state.inputDone || state.hasAudio)
	if wait && !state.inputDone {
		// Earlier audio already reached the relay. Finish only the text
		// successfully written before the client disconnected.
		payload, err := jsonx.Marshal(map[string]any{
			"type": "tts.text.done",
			"data": map[string]string{"session_id": state.sessionID},
		})
		if err == nil {
			err = s.writeStepAudioFrame(drainCtx, upstream, coderws.MessageText, payload)
		}
		wait = err == nil
		state.inputDone = wait
	}
	state.mu.Unlock()
	if wait {
		select {
		case <-done:
			return
		case <-drainCtx.Done():
		}
	}
	cancelUpstream()
	<-done
}

func (s *HTTPServer) readStepAudioTTSClient(ctx, upstreamCtx context.Context, client, upstream openAIWSFrameConn, session stepAudioSession, state *stepAudioTTSState) error {
	for {
		kind, payload, err := client.ReadFrame(ctx)
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if kind != coderws.MessageText || len(payload) > 32<<10 {
			return errWSRoutingAdmission
		}
		if err := s.writeStepAudioTTSClientEvent(ctx, upstreamCtx, upstream, session, state, payload); err != nil {
			return err
		}
	}
}

func (s *HTTPServer) writeStepAudioTTSClientEvent(ctx, upstreamCtx context.Context, upstream openAIWSFrameConn, session stepAudioSession, state *stepAudioTTSState, payload []byte) error {
	var frame map[string]jsonx.RawMessage
	var kind, sessionID string
	var data map[string]jsonx.RawMessage
	if jsonx.Unmarshal(payload, &frame) != nil || jsonx.Unmarshal(frame["type"], &kind) != nil ||
		jsonx.Unmarshal(frame["data"], &data) != nil || jsonx.Unmarshal(data["session_id"], &sessionID) != nil {
		return errWSRoutingAdmission
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if sessionID == "" || sessionID != state.sessionID || state.inputDone {
		return errWSRoutingAdmission
	}

	var input string
	var characters int
	switch kind {
	case "tts.create":
		var voice string
		if state.created || jsonx.Unmarshal(data["voice_id"], &voice) != nil || strings.TrimSpace(voice) == "" {
			return errWSRoutingAdmission
		}
		for key := range data {
			switch key {
			case "session_id", "voice_id", "response_format", "sample_rate", "speed_ratio", "volume_ratio",
				"pronunciation_map", "text_normalization", "mode", "timestamp", "instruction", "language", "markdown_filter":
			default:
				return errWSRoutingAdmission
			}
		}
	case "tts.text.delta":
		if !state.ready || len(data) != 2 || !utf8.Valid(payload) || jsonx.Unmarshal(data["text"], &input) != nil || input == "" {
			return errWSRoutingAdmission
		}
		characters = utf8.RuneCountInString(input)
		if state.characters+characters > 1000 {
			return fmt.Errorf("%w: maximum 1000 speech characters per session", errWSRoutingAdmission)
		}
		if state.reservation == nil {
			if err := s.recheckStepAudioTurn(ctx, session, routing.StepAudioTTSModel); err != nil {
				return err
			}
			auth, channel := session.plan.Auth, session.channel
			id := generateRequestID()
			attempt := channelAttemptContext(ctx, id, 1, channel, routing.StepAudioTTSModel)
			attempt = routing.WithCostBound(attempt, routing.CostBound{
				Protocol: routing.StepAudioTTSWSProtocol, UpstreamModel: routing.StepAudioTTSModel, AudioUnits: routing.StepAudioTTSMaxUnits,
			})
			reservation, err := s.reserveQuota(attempt, strconv.FormatInt(auth.UserID, 10), id, 0, routing.StepAudioTTSModel,
				strconv.FormatInt(channel.ID, 10), 0, auth.RoutingContext)
			if err != nil {
				return err
			}
			state.reservation = reservation
		}
	case "tts.text.flush", "tts.text.done":
		if !state.ready || state.reservation == nil || len(data) != 1 {
			return errWSRoutingAdmission
		}
	default:
		return errWSRoutingAdmission
	}
	// Re-encode the measured fields so duplicate JSON keys cannot change what
	// the supplier synthesizes.
	canonicalData, err := jsonx.Marshal(data)
	if err != nil {
		return err
	}
	out, err := jsonx.Marshal(map[string]jsonx.RawMessage{"type": frame["type"], "data": canonicalData})
	if err != nil {
		return err
	}
	if err := s.writeStepAudioFrame(upstreamCtx, upstream, coderws.MessageText, out); err != nil {
		return err
	}
	switch kind {
	case "tts.create":
		state.created = true
	case "tts.text.delta":
		if state.startedAt.IsZero() {
			state.startedAt = time.Now()
		}
		state.characters += characters
		state.units += stepAudioSpeechUnits(input)
	case "tts.text.done":
		state.inputDone = true
	}
	return nil
}

func (s *HTTPServer) readStepAudioTTSUpstream(ctx, clientCtx context.Context, cancelClient context.CancelFunc, client, upstream openAIWSFrameConn, session stepAudioSession, state *stepAudioTTSState) error {
	for {
		kind, payload, err := upstream.ReadFrame(ctx)
		if err != nil {
			return err
		}
		var event struct {
			Type string `json:"type"`
			Data struct {
				SessionID string `json:"session_id"`
				Audio     string `json:"audio"`
			} `json:"data"`
		}
		if kind != coderws.MessageText || jsonx.Unmarshal(payload, &event) != nil {
			return fmt.Errorf("invalid upstream speech event")
		}
		var reservation *billingv1.ReserveQuotaResponse
		var units int64
		var elapsed int64
		state.mu.Lock()
		switch {
		case event.Type == "tts.connection.done" && state.sessionID == "" && event.Data.SessionID != "" && len(event.Data.SessionID) <= 256:
			state.sessionID = event.Data.SessionID
		case state.sessionID == "" || event.Data.SessionID != state.sessionID:
			err = fmt.Errorf("upstream speech session mismatch")
		case event.Type == "tts.response.created" && state.created:
			state.ready = true
		case event.Type == "tts.response.audio.delta":
			if state.reservation == nil || state.units == 0 {
				err = fmt.Errorf("upstream speech output without a reservation and forwarded text")
			} else if event.Data.Audio != "" {
				if !stepAudioTTSHasAudio(event.Data.Audio) {
					err = fmt.Errorf("invalid upstream speech audio")
				} else {
					state.hasAudio = true
				}
			}
		case event.Type == "tts.response.audio.done":
			invalidAudio := event.Data.Audio != "" && !stepAudioTTSHasAudio(event.Data.Audio)
			// The supplier can complete an idle session without tts.text.done.
			if state.reservation == nil || state.units == 0 || invalidAudio || (!state.hasAudio && event.Data.Audio == "") {
				err = fmt.Errorf("speech terminal without proven output")
				break
			}
			reservation, units = state.reservation, state.units
			elapsed = time.Since(state.startedAt).Milliseconds()
			state.reservation = nil
			state.inputDone = true
		}
		state.mu.Unlock()
		if err != nil {
			return err
		}
		if reservation != nil {
			if session.reportHealth != nil {
				session.reportHealth(ctx, nil)
			}
			auth := session.plan.Auth
			info := usageLogInput{
				UserID: auth.UserID, TokenID: auth.TokenID, TokenName: auth.TokenName, RequestID: reservation.RequestId,
				ModelName: routing.StepAudioTTSModel, UpstreamModelID: routing.StepAudioTTSModel, ChannelID: session.channel.ID,
				SourceKind: relaybiz.UpstreamSourceChannel, IsStream: true, Endpoint: stepAudioTTSEndpoint, ActualAudioUnits: units, ElapsedTime: elapsed,
			}
			info.applyReservation(reservation)
			if err := s.commitQuota(ctx, reservation.ReservationId, 0, true, info); err != nil {
				return err
			}
			s.ingestUsageLogAfterResponse(info, ctx)
		}
		if clientCtx.Err() == nil {
			if err := s.writeStepAudioFrame(clientCtx, client, kind, payload); err != nil {
				cancelClient()
			}
		}
		if reservation != nil || event.Type == "tts.response.error" {
			return nil
		}
	}
}

func stepAudioTTSHasAudio(encoded string) bool {
	n, err := io.Copy(io.Discard, base64.NewDecoder(base64.StdEncoding, strings.NewReader(encoded)))
	return err == nil && n > 0
}
