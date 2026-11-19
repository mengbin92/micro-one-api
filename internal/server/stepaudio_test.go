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

func TestStepAudioRealtimeRejectsUnmeteredAutomaticResponses(t *testing.T) {
	_, err := stepAudioRealtimeClientEvent([]byte(`{"type":"session.update","session":{"turn_detection":{"type":"server_vad"}}}`))
	require.Error(t, err)
	_, err = stepAudioRealtimeClientEvent([]byte(`{"type":"session.update","session":{"input_audio_transcription":{"model":"other"}}}`))
	require.Error(t, err)
	out, err := stepAudioRealtimeClientEvent([]byte(`{"type":"session.update","session":{"voice":"official"}}`))
	require.NoError(t, err)
	var v map[string]any
	require.NoError(t, jsonx.Unmarshal(out, &v))
	require.Equal(t, "official", v["session"].(map[string]any)["voice"])
	require.Contains(t, v["session"].(map[string]any), "turn_detection")
	require.Nil(t, v["session"].(map[string]any)["turn_detection"])
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
	for _, completed := range []bool{false, true} {
		const delta = "data: {\"type\":\"transcript.text.delta\",\"delta\":\"ok\"}\n\n"
		lines := delta
		if completed {
			lines += "data: {\"type\":\"transcript.text.done\",\"text\":\"ok\",\"usage\":{\"input_tokens\":25,\"output_tokens\":2,\"total_tokens\":27}}\n\n"
		}
		billing := &rawBillingClient{}
		server := NewHTTPServer(nil, nil, billing, nil, nil)
		w := httptest.NewRecorder()
		started, err := server.forwardStepAudioSSE(context.Background(), w, stepAudioSSEProvider{lines: lines}, &relayprovider.RawRequest{}, "transcript.text.done", "audio-reservation", usageLogInput{}, time.Now())
		require.True(t, started)
		require.Contains(t, w.Body.String(), delta)
		if completed {
			require.NoError(t, err)
			require.Len(t, billing.commitRequests, 1)
			require.EqualValues(t, 27, billing.commitRequests[0].ActualTokens)
			require.Zero(t, billing.releases)
		} else {
			require.Error(t, err)
			require.Empty(t, billing.commitRequests)
			require.Equal(t, 1, billing.releases)
		}
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
