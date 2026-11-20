package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/http"
	"strconv"
	"time"

	coderws "github.com/coder/websocket"

	"micro-one-api/domain/routing"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

const (
	realtimePCMRate     = 24000
	realtimePCMPerMS    = realtimePCMRate * 2 / 1000
	realtimeVADFrameMS  = 10
	realtimeMaxInputPCM = 30_000 * realtimePCMPerMS
	stepAudioASRModel   = "stepaudio-2.5-asr"
)

type stepAudioRealtimeInput struct {
	vad              *stepAudioVAD
	transcribe       bool
	pcm              []byte
	upstreamBuffered bool
}

type stepAudioTranscription struct {
	pcm       []byte
	ack       <-chan string
	localItem string
}

func (s *HTTPServer) relayStepAudioTranscriptions(ctx context.Context, client openAIWSFrameConn, session stepAudioSession, jobs <-chan stepAudioTranscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-jobs:
			timer := time.NewTimer(10 * time.Second)
			var err error
			select {
			case item := <-job.ack:
				timer.Stop()
				err = s.transcribeStepAudioItem(ctx, client, session, job.pcm, item)
			case <-timer.C:
				err = s.writeStepAudioTranscriptionFailure(ctx, client, job.localItem)
			case <-ctx.Done():
				timer.Stop()
				return
			}
			if err != nil {
				return
			}
		}
	}
}

func (s *HTTPServer) transcribeStepAudioItem(ctx context.Context, client openAIWSFrameConn, session stepAudioSession, pcm []byte, item string) error {
	err := s.transcribeStepAudioInput(ctx, client, session, pcm, item)
	if err == nil || ctx.Err() != nil {
		return err
	}
	return s.writeStepAudioTranscriptionFailure(ctx, client, item)
}

func (s *HTTPServer) writeStepAudioTranscriptionFailure(ctx context.Context, client openAIWSFrameConn, item string) error {
	return s.writeStepAudioInputEvent(ctx, client, map[string]any{
		"type": "conversation.item.input_audio_transcription.failed", "item_id": item, "content_index": 0,
		"error": map[string]string{"code": "transcription_failed", "message": "Input transcription failed"},
	})
}

type stepAudioVAD struct {
	prefixMS  int
	silenceMS int
	threshold int64
	tail      []byte
	preRoll   []byte
	quietMS   int
	elapsedMS int64
	itemID    string
}

func (input *stepAudioRealtimeInput) configure(session map[string]jsonx.RawMessage) error {
	vad, transcribe := input.vad, input.transcribe
	if raw, ok := session["turn_detection"]; ok {
		if bytes.Equal(raw, []byte("null")) {
			vad = nil
		} else {
			var options struct {
				Type      string `json:"type"`
				Prefix    int    `json:"prefix_padding_ms"`
				Silence   int    `json:"silence_duration_ms"`
				Threshold int64  `json:"energy_awakeness_threshold"`
			}
			options.Prefix, options.Silence, options.Threshold = 500, 100, 2500
			dec := jsonx.NewDecoder(bytes.NewReader(raw))
			dec.DisallowUnknownFields()
			if dec.Decode(&options) != nil || options.Type != "server_vad" ||
				options.Prefix < 0 || options.Prefix > 2000 || options.Silence < 10 || options.Silence > 2000 ||
				options.Threshold < 0 || options.Threshold > 5000 {
				return errWSRoutingAdmission
			}
			if vad == nil || vad.prefixMS != options.Prefix || vad.silenceMS != options.Silence || vad.threshold != options.Threshold {
				vad = &stepAudioVAD{prefixMS: options.Prefix, silenceMS: options.Silence, threshold: options.Threshold}
			}
		}
	}
	if raw, ok := session["input_audio_transcription"]; ok {
		transcribe = !bytes.Equal(raw, []byte("null"))
		if transcribe {
			var config map[string]string
			if jsonx.Unmarshal(raw, &config) != nil || len(config) != 1 || config["model"] != stepAudioASRModel {
				return errWSRoutingAdmission
			}
		}
		delete(session, "input_audio_transcription")
	}
	if raw, ok := session["input_audio_format"]; ok {
		var format string
		if jsonx.Unmarshal(raw, &format) != nil || format != "pcm16" {
			return errWSRoutingAdmission
		}
	}
	if (vad != input.vad || transcribe != input.transcribe) &&
		(input.upstreamBuffered || len(input.pcm) != 0 || (input.vad != nil && len(input.vad.tail) != 0)) {
		return fmt.Errorf("%w: clear the audio buffer before changing detection or transcription", errWSRoutingAdmission)
	}
	input.vad, input.transcribe = vad, transcribe
	session["turn_detection"] = jsonx.RawMessage("null")
	return nil
}

func decodeStepAudioPCM(payload []byte) ([]byte, error) {
	var event struct {
		Audio string `json:"audio"`
	}
	if jsonx.Unmarshal(payload, &event) != nil || len(event.Audio) > base64.StdEncoding.EncodedLen(realtimeMaxInputPCM) {
		return nil, errWSRoutingAdmission
	}
	pcm, err := base64.StdEncoding.Strict().DecodeString(event.Audio)
	if err != nil || len(pcm) == 0 || len(pcm)%2 != 0 || len(pcm) > realtimeMaxInputPCM {
		return nil, errWSRoutingAdmission
	}
	return pcm, nil
}

func (input *stepAudioRealtimeInput) push(pcm []byte, speechStarted func(string, int64) error, utterance func([]byte, string, int64) error) error {
	if input.vad == nil {
		if len(input.pcm)+len(pcm) > realtimeMaxInputPCM {
			return fmt.Errorf("transcription audio exceeds 30 seconds")
		}
		input.pcm = append(input.pcm, pcm...)
		return nil
	}
	vad := input.vad
	const frameBytes = realtimePCMPerMS * realtimeVADFrameMS
	pcm = append(vad.tail, pcm...)
	vad.tail = nil
	for len(pcm) >= frameBytes {
		frame := pcm[:frameBytes]
		pcm = pcm[frameBytes:]
		vad.elapsedMS += realtimeVADFrameMS
		var energy int64
		for offset := 0; offset < frameBytes; offset += 2 {
			sample := int64(int16(binary.LittleEndian.Uint16(frame[offset:])))
			energy += sample * sample
		}
		voiced := energy > vad.threshold*vad.threshold*(frameBytes/2)
		if vad.itemID == "" {
			if !voiced {
				vad.preRoll = append(vad.preRoll, frame...)
				if extra := len(vad.preRoll) - vad.prefixMS*realtimePCMPerMS; extra > 0 {
					vad.preRoll = vad.preRoll[:copy(vad.preRoll, vad.preRoll[extra:])]
				}
				continue
			}
			vad.itemID = "item_" + generateRequestID()
			input.pcm, vad.preRoll = vad.preRoll, nil
			if err := speechStarted(vad.itemID, vad.elapsedMS-realtimeVADFrameMS-int64(len(input.pcm)/realtimePCMPerMS)); err != nil {
				return err
			}
		}
		input.pcm = append(input.pcm, frame...)
		if len(input.pcm) > realtimeMaxInputPCM {
			return fmt.Errorf("VAD utterance exceeds 30 seconds")
		}
		if voiced {
			vad.quietMS = 0
			continue
		}
		vad.quietMS += realtimeVADFrameMS
		if vad.quietMS >= vad.silenceMS {
			if err := utterance(input.pcm, vad.itemID, vad.elapsedMS); err != nil {
				return err
			}
			input.pcm, vad.itemID, vad.quietMS = nil, "", 0
		}
	}
	vad.tail = append([]byte(nil), pcm...)
	return nil
}

func (s *HTTPServer) transcribeStepAudioInput(ctx context.Context, client openAIWSFrameConn, session stepAudioSession, pcm []byte, itemID string) error {
	asrCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.recheckStepAudioTurn(asrCtx, session, stepAudioASRModel); err != nil {
		return err
	}
	var input stepAudioASRRequest
	input.Audio.Data = base64.StdEncoding.EncodeToString(pcm)
	input.Audio.Input.Transcription.Model = stepAudioASRModel
	input.Audio.Input.Format = stepAudioASRFormat{Type: "pcm", Codec: "pcm_s16le", Rate: realtimePCMRate, Bits: 16, Channel: 1}
	body, err := jsonx.Marshal(input)
	if err != nil {
		return err
	}
	auth, ch := session.plan.Auth, session.channel
	provider, err := s.providerFactory.CreateProviderWithConfig(ch.Type, ch.BaseURL, ch.Key, relayprovider.ProviderConfig{APIVersion: ch.Config.APIVersion})
	if err != nil {
		return err
	}
	requestID := generateRequestID()
	asrCtx = channelAttemptContext(asrCtx, requestID, 1, ch, stepAudioASRModel)
	bound := routing.CostBound{
		Protocol: routing.AudioInputProtocol(stepAudioASRModel), UpstreamModel: stepAudioASRModel,
		AudioUnits: (int64(len(pcm)/2)*1_000_000 + realtimePCMRate - 1) / realtimePCMRate,
	}
	asrCtx = routing.WithCostBound(asrCtx, bound)
	reservation, err := s.reserveQuota(asrCtx, strconv.FormatInt(auth.UserID, 10), requestID, 0, stepAudioASRModel, strconv.FormatInt(ch.ID, 10), 0, auth.RoutingContext)
	if err != nil {
		return err
	}
	asrCtx = s.observeNativeUsage(asrCtx, ch, requestID, stepAudioASRModel)
	req := &relayprovider.RawRequest{
		Method: http.MethodPost, Path: stepAudioASRPath, Body: body,
		Header: http.Header{"Content-Type": {"application/json"}, "Accept": {"text/event-stream"}},
	}
	log := usageLogInput{
		UserID: auth.UserID, TokenID: auth.TokenID, TokenName: auth.TokenName, RequestID: requestID,
		ModelName: stepAudioASRModel, Endpoint: stepAudioASRPath, ChannelID: ch.ID, IsStream: true,
		SourceKind: relaybiz.UpstreamSourceChannel, UpstreamModelID: stepAudioASRModel,
	}
	if err := asrCtx.Err(); err != nil {
		_ = s.releaseQuota(ctx, reservation.ReservationId, "transcription did not start")
		return err
	}
	deadline, _ := asrCtx.Deadline()
	upstreamCtx, stopUpstream := context.WithDeadline(context.WithoutCancel(asrCtx), deadline)
	defer stopUpstream()
	stopDrain := context.AfterFunc(asrCtx, func() {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			stopUpstream()
		case <-upstreamCtx.Done():
		}
	})
	defer stopDrain()
	started, err := s.relayStepAudioSSE(upstreamCtx, provider, req, "transcript.text.done", reservation, log, time.Now(), nil, func(kind string, raw, _ []byte) error {
		if asrCtx.Err() != nil || (kind != "transcript.text.delta" && kind != "transcript.text.done") {
			return nil
		}
		var upstream struct {
			Delta string `json:"delta"`
			Text  string `json:"text"`
		}
		if err := jsonx.Unmarshal(raw, &upstream); err != nil {
			return err
		}
		event := map[string]any{"item_id": itemID, "content_index": 0}
		if kind == "transcript.text.delta" {
			event["type"], event["delta"] = "conversation.item.input_audio_transcription.delta", upstream.Delta
		} else {
			event["type"], event["transcript"] = "conversation.item.input_audio_transcription.completed", upstream.Text
		}
		if err := s.writeStepAudioInputEvent(asrCtx, client, event); err != nil {
			cancel()
		}
		return nil
	})
	if err != nil && !started {
		_ = s.releaseQuota(context.WithoutCancel(ctx), reservation.ReservationId, "transcription did not start")
	}
	return err
}

func (s *HTTPServer) writeStepAudioInputEvent(ctx context.Context, conn openAIWSFrameConn, event map[string]any) error {
	payload, err := jsonx.Marshal(event)
	if err != nil {
		return err
	}
	return s.writeStepAudioFrame(ctx, conn, coderws.MessageText, payload)
}
