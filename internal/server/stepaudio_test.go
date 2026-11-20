package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/upstream/evidence"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	relaydata "micro-one-api/internal/data"
	"micro-one-api/pkg/jsonx"
)

func TestStepAudioMeteringUsesInputBytesAndCharacters(t *testing.T) {
	tts, err := parseStepAudioInput("/audio/speech", "stepaudio-2.5-tts", []byte(`{"model":"stepaudio-2.5-tts","input":"你好ABC!","stream_format":"sse"}`))
	require.NoError(t, err)
	require.EqualValues(t, 8, tts.bound.AudioUnits)
	require.Equal(t, "speech.audio.done", tts.streamEvent)

	body := func(size int, rate int) []byte {
		return fmt.Appendf(nil, `{"audio":{"data":%q,"input":{"transcription":{"model":"stepaudio-2.5-asr","language":"zh"},"format":{"type":"pcm","codec":"pcm_s16le","rate":%d,"bits":16,"channel":1}}}}`, base64.StdEncoding.EncodeToString(make([]byte, size)), rate)
	}
	asr, err := parseStepAudioASR(body(32_000, 16_000))
	require.NoError(t, err)
	require.EqualValues(t, 1_000_000, asr.bound.AudioUnits)
	require.Equal(t, "transcript.text.done", asr.streamEvent)
	_, err = parseStepAudioASR(body(31_999, 16_000))
	require.Error(t, err)
	_, err = parseStepAudioASR(body(32_000, 0))
	require.Error(t, err)
}

func TestStepAudioRealtimeKeepsDetectionAndTranscriptionInRelay(t *testing.T) {
	var input stepAudioRealtimeInput
	_, _, err := stepAudioRealtimeClientEvent([]byte(`{"type":"session.update","session":{"turn_detection":{"type":"server_vad","create_response":true}}}`), &input)
	require.Error(t, err)
	_, _, err = stepAudioRealtimeClientEvent([]byte(`{"type":"session.update","session":{"input_audio_transcription":{"model":"other"}}}`), &input)
	require.Error(t, err)
	_, out, err := stepAudioRealtimeClientEvent([]byte(`{"type":"session.update","session":{"voice":"official","turn_detection":{"type":"server_vad"},"input_audio_transcription":{"model":"stepaudio-2.5-asr"}}}`), &input)
	require.NoError(t, err)
	require.NotNil(t, input.vad)
	require.True(t, input.transcribe)
	var v map[string]any
	require.NoError(t, jsonx.Unmarshal(out, &v))
	require.Equal(t, "official", v["session"].(map[string]any)["voice"])
	require.Contains(t, v["session"].(map[string]any), "turn_detection")
	require.Nil(t, v["session"].(map[string]any)["turn_detection"])
	require.NotContains(t, v["session"], "input_audio_transcription")
}

func TestStepAudioRealtimeSessionUpdatePreservesBufferedAudio(t *testing.T) {
	var input stepAudioRealtimeInput
	const update = `{"type":"session.update","session":{"turn_detection":{"type":"server_vad"}}}`
	_, _, err := stepAudioRealtimeClientEvent([]byte(update), &input)
	require.NoError(t, err)
	input.pcm = []byte{0, 0}
	_, _, err = stepAudioRealtimeClientEvent([]byte(update), &input)
	require.NoError(t, err)
	require.Equal(t, []byte{0, 0}, input.pcm)
	_, _, err = stepAudioRealtimeClientEvent([]byte(`{"type":"session.update","session":{"turn_detection":null}}`), &input)
	require.Error(t, err)
	require.NotNil(t, input.vad)
}

func TestStepAudioRealtimeUpstreamUsesBidirectionalTransport(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		kind, data, err := conn.Read(r.Context())
		if err == nil {
			_ = conn.Write(r.Context(), kind, data)
		}
	}))
	defer upstream.Close()

	srv := NewHTTPServer(nil, nil, nil, relayprovider.NewProviderFactory(time.Second), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := srv.dialStepAudioRealtime(ctx, "ws"+strings.TrimPrefix(upstream.URL, "http"), "fixture")
	require.NoError(t, err)
	defer conn.CloseNow()
	require.NoError(t, conn.Write(ctx, coderws.MessageText, []byte("test turn")))
	kind, data, err := conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, coderws.MessageText, kind)
	require.Equal(t, "test turn", string(data))
}

func TestStepAudioRealtimeObservesSettledAndTruncatedTurns(t *testing.T) {
	t.Setenv("RELAY_USAGE_OBSERVATION_V1", "true")
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "false")
	t.Setenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST", "")
	billing := &rawBillingClient{}
	channel := &relaybiz.Channel{ID: 11, Config: relaybiz.ChannelConfig{APIVersion: "fixture"}}
	channelClient := rawChannelClient{}
	server := NewHTTPServer(nil, channelClient, billing, nil, nil)
	repo := &observingRepo{}
	server.SetObservationDelivery(relaybiz.NewObservationDelivery(repo, nil))
	plan := &relaybiz.RelayPlan{Auth: &relaybiz.AuthSnapshot{UserID: 42, Group: "default"}}
	client, upstream := newFakeFrameConn(4), newFakeFrameConn(4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- server.bridgeStepAudioRealtime(ctx, client, upstream, stepAudioSession{plan: plan, channel: channel, endpoint: "/step_plan/v1/realtime"})
	}()
	read := func(ch <-chan fakeFrame) string {
		select {
		case frame := <-ch:
			return string(frame.payload)
		case <-ctx.Done():
			t.Fatal("missing relayed frame")
			return ""
		}
	}
	require.Contains(t, read(upstream.out), "session.update")
	create := fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.create"}`)}
	client.in <- create
	require.JSONEq(t, string(create.payload), read(upstream.out))
	terminal := `{"type":"response.done","response":{"id":"resp-first","output":[{"text":"private"}],"usage":{"input_tokens":12,"output_tokens":3,"total_tokens":15,"input_token_details":{"cached_tokens":2}}}}`
	upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(terminal)}
	require.Equal(t, terminal, read(client.out))
	client.in <- create
	require.JSONEq(t, string(create.payload), read(upstream.out))
	close(upstream.in)
	require.Error(t, <-done)

	events := repo.snapshot()
	require.Len(t, events, 4)
	for _, event := range events {
		require.NoError(t, event.Validate())
		require.Equal(t, evidence.StepAudioRealtime, event.Send.Protocol)
		require.EqualValues(t, 42, event.Send.UserID)
		require.Equal(t, evidence.PurposeUser, event.Send.Purpose)
		require.Equal(t, "/step_plan/v1/realtime", event.Send.Endpoint)
	}
	require.Equal(t, "started", events[0].Category)
	require.Equal(t, "terminal", events[1].Category)
	require.Equal(t, events[0].Send, events[1].Send)
	require.Equal(t, evidence.CaptureComplete, events[1].Capture)
	require.Equal(t, "verified", events[1].Record.Observation.Status)
	require.Equal(t, &[5]int64{10, 2, 0, 0, 3}, events[1].Record.Observation.Buckets)
	require.NotContains(t, string(events[1].Record.Observation.Usage), "private")
	require.Equal(t, "started", events[2].Category)
	require.Equal(t, "terminal", events[3].Category)
	require.Equal(t, events[2].Send, events[3].Send)
	require.Equal(t, evidence.CaptureTruncated, events[3].Capture)
	require.NotEqual(t, events[0].Send.SendID, events[2].Send.SendID)
	require.Len(t, billing.reserveRequests, 2)
	require.Len(t, billing.commitRequests, 1)
	require.EqualValues(t, 15, billing.commitRequests[0].ActualTokens)
	require.Equal(t, 1, billing.releases)
	require.Equal(t, billing.reserveRequests[0].RequestId, events[0].Send.RequestID)
	require.Equal(t, billing.reserveRequests[1].RequestId, events[2].Send.RequestID)
}

type stepAudioSSEProvider struct {
	relayprovider.Provider
	lines string
}

func (p stepAudioSSEProvider) ForwardStream(context.Context, *relayprovider.RawRequest) (*relayprovider.RawStreamResponse, error) {
	return &relayprovider.RawStreamResponse{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(p.lines)),
	}, nil
}

func TestStepAudioASRStreamSettlesOnlyOnTerminal(t *testing.T) {
	const delta = "data: {\"type\":\"transcript.text.delta\",\"delta\":\"ok\"}\n\n"
	const terminal = "data: {\"type\":\"transcript.text.done\",\"text\":\"ok\",\"usage\":{\"input_tokens\":25,\"output_tokens\":2,\"total_tokens\":27}}\n"
	for _, tc := range []struct {
		name, suffix string
		completed    bool
	}{
		{"no terminal", "", false},
		{"unterminated event", terminal, false},
		{"malformed transcript", "data: {\"type\":\"transcript.text.done\",\"text\":123}\n\n", false},
		{"terminal", terminal + "\n", true},
		{"multiline terminal", strings.Replace(terminal, `,"usage"`, ",\ndata: \"usage\"", 1) + "\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billing := &rawBillingClient{}
			server := NewHTTPServer(nil, nil, billing, nil, nil)
			logs := &rawLogClient{}
			server.logClient = logs
			w := httptest.NewRecorder()
			reservation := &billingv1.ReserveQuotaResponse{ReservationId: "audio-reservation", RootRequestId: "audio-root", AttemptNumber: 2}
			started, err := server.forwardStepAudioSSE(context.Background(), w, stepAudioSSEProvider{lines: delta + tc.suffix}, &relayprovider.RawRequest{}, "transcript.text.done", reservation, usageLogInput{}, time.Now())
			require.True(t, started)
			require.Contains(t, w.Body.String(), delta)
			if tc.completed {
				require.NoError(t, err)
				require.Len(t, billing.commitRequests, 1)
				require.EqualValues(t, 27, billing.commitRequests[0].ActualTokens)
				require.Zero(t, billing.releases)
				require.Len(t, logs.entries, 1)
				require.Equal(t, "audio-reservation", logs.entries[0].ReservationId)
				require.Equal(t, "audio-root", logs.entries[0].RootRequestId)
				require.EqualValues(t, 2, logs.entries[0].AttemptNumber)
			} else {
				require.Error(t, err)
				require.Empty(t, billing.commitRequests)
				require.Equal(t, 1, billing.releases)
			}
		})
	}
}

func TestStepAudioChatPreservesAudioInputOnDefaultChatRoute(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	var received []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"stepaudio-2.5-chat","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":25,"completion_tokens":2,"total_tokens":27}}`)
	}))
	defer upstream.Close()

	identity := rawIdentityClient{}
	channels := rawChannelClient{baseURL: upstream.URL + "/v1", key: "sk-test", getModels: "stepaudio-2.5-chat"}
	billing := &rawBillingClient{}
	uc := relaybiz.NewRelayUsecase(relaydata.NewIdentityAdapter(identity), relaydata.NewChannelAdapter(channels), nil, nil)
	srv := NewHTTPServer(identity, channels, billing, relayprovider.NewProviderFactory(time.Second), uc)

	const payload = `{"model":"stepaudio-2.5-chat","messages":[{"role":"user","content":[{"type":"input_audio","input_audio":{"data":"AA==","format":"wav"}}]}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
	req.Header.Set("Authorization", "Bearer fixture")
	rec := httptest.NewRecorder()
	srv.relayOrchestratorChatHandler(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, string(received), `"type":"input_audio"`)
	require.Len(t, billing.commitRequests, 1)
	require.EqualValues(t, 27, billing.commitRequests[0].ActualTokens)
}
