package server

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
	relayprovider "micro-one-api/domain/upstream/provider"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

type stepAudioBillingFixture struct {
	rawBillingClient
	context *routing.ResolvedRoutingContext
}

func (*stepAudioBillingFixture) GetRoutingCapabilities(context.Context, *billingv1.GetRoutingCapabilitiesRequest, ...grpc.CallOption) (*billingv1.GetRoutingCapabilitiesResponse, error) {
	return &billingv1.GetRoutingCapabilitiesResponse{RequestSnapshotVersion: 2, AudioInputPricingVersion: 1, AudioInputActualUnits: true}, nil
}

func (f *stepAudioBillingFixture) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	res, err := f.rawBillingClient.ReserveQuota(ctx, req, opts...)
	res.RequestSnapshotVersion = 2
	res.RequestSnapshotHash = f.context.Digest()
	res.RoutingContextHash = res.RequestSnapshotHash
	return res, err
}

type stepAudioRouteFixture struct{ orderedGateChannel }

func (stepAudioRouteFixture) GetRoutingGroup(context.Context, int64) (*routing.Group, error) {
	return &routing.Group{ID: 3, Key: "default", Status: "enabled", AccessMode: "public", ModelAccessMode: "all_authorized", Revision: 1}, nil
}

type stepAudioIdentityFixture struct{}

func (stepAudioIdentityFixture) GetAuthSnapshot(context.Context, string, string) (*relaybiz.AuthSnapshot, error) {
	return &relaybiz.AuthSnapshot{
		Group: "default", UserID: 1, TokenID: 2, UserEnabled: true, TokenEnabled: true, RoutingContextVersion: 2,
		RoutingFacts: &routing.SubjectFacts{DefaultGroupID: 3, PublicGroupAccess: "all", AccessRevision: 1, TokenMode: "inherit", TokenRevision: 1},
	}, nil
}

func newStepAudioTestBridge(t *testing.T, baseURL string) (*HTTPServer, stepAudioSession, *stepAudioBillingFixture) {
	t.Helper()
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST", "")
	identity := stepAudioIdentityFixture{}
	uc := relaybiz.NewRelayUsecase(identity, &stepAudioRouteFixture{}, nil, nil)
	auth, err := identity.GetAuthSnapshot(context.Background(), "", "")
	require.NoError(t, err)
	require.NoError(t, uc.ResolveRoutingContext(context.Background(), auth, relaybiz.RoutingResolveOptions{}))
	uc.BindRoutingAdmission(auth, "fixture", "")
	billing := &stepAudioBillingFixture{context: auth.RoutingContext}
	s := NewHTTPServer(nil, nil, billing, relayprovider.NewProviderFactory(time.Second), uc)
	session := stepAudioSession{plan: &relaybiz.RelayPlan{Auth: auth}, channel: &relaybiz.Channel{
		ID: 11, Type: relayprovider.ChannelTypeOpenAI, BaseURL: baseURL, Key: "audio-fixture",
	}}
	return s, session, billing
}

func stepAudioTextFrame(data string) fakeFrame {
	return fakeFrame{msgType: coderws.MessageText, payload: []byte(data)}
}

func waitStepAudioFrame(t *testing.T, ctx context.Context, ch <-chan fakeFrame) string {
	t.Helper()
	select {
	case frame := <-ch:
		return string(frame.payload)
	case <-ctx.Done():
		t.Fatal("missing relayed frame")
		return ""
	}
}

func TestStepAudioTTSWebSocketStreamsMultipleDeltaFrames(t *testing.T) {
	s, session, billing := newStepAudioTestBridge(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
	done := make(chan error, 1)
	go func() { done <- s.bridgeStepAudioTTS(ctx, client, upstream, session) }()
	receive := func(kind string) string {
		event := fmt.Sprintf(`{"type":%q,"data":{"session_id":"s","audio":"AA=="}}`, kind)
		upstream.in <- stepAudioTextFrame(event)
		return event
	}
	send := func(kind, fields string) {
		event := fmt.Sprintf(`{"type":%q,"data":{"session_id":"s"%s}}`, kind, fields)
		client.in <- stepAudioTextFrame(event)
		require.JSONEq(t, event, waitStepAudioFrame(t, ctx, upstream.out))
	}
	require.Equal(t, receive("tts.connection.done"), waitStepAudioFrame(t, ctx, client.out))
	send("tts.create", `,"voice_id":"fixture"`)
	require.Equal(t, receive("tts.response.created"), waitStepAudioFrame(t, ctx, client.out))
	send("tts.text.delta", `,"text":"你a"`)
	require.Len(t, billing.reserveRequests, 1, "reserve before forwarding the first generating frame")
	require.Equal(t, routing.StepAudioTTSWSProtocol, billing.reserveRequests[0].CostBound.Protocol)
	require.EqualValues(t, routing.StepAudioTTSMaxUnits, billing.reserveRequests[0].CostBound.AudioUnits)
	require.Equal(t, receive("tts.response.audio.delta"), waitStepAudioFrame(t, ctx, client.out))
	send("tts.text.delta", `,"text":"b!"`)
	send("tts.text.flush", "")
	send("tts.text.done", "")
	require.Equal(t, receive("tts.response.audio.done"), waitStepAudioFrame(t, ctx, client.out))
	require.NoError(t, <-done)
	require.Len(t, billing.reserveRequests, 1)
	require.Len(t, billing.commitRequests, 1)
	require.EqualValues(t, 5, billing.commitRequests[0].ActualAudioUnits)
	require.Zero(t, billing.commitRequests[0].ActualTokens)
	require.Zero(t, billing.releases)
}

func TestStepAudioTTSWebSocketReleaseOnTruncation(t *testing.T) {
	s, session, billing := newStepAudioTestBridge(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, upstream := newFakeFrameConn(4), newFakeFrameConn(4)
	done := make(chan error, 1)
	go func() { done <- s.bridgeStepAudioTTS(ctx, client, upstream, session) }()
	upstream.in <- stepAudioTextFrame(`{"type":"tts.connection.done","data":{"session_id":"s"}}`)
	waitStepAudioFrame(t, ctx, client.out)
	client.in <- stepAudioTextFrame(`{"type":"tts.create","data":{"session_id":"s","voice_id":"fixture"}}`)
	waitStepAudioFrame(t, ctx, upstream.out)
	upstream.in <- stepAudioTextFrame(`{"type":"tts.response.created","data":{"session_id":"s"}}`)
	waitStepAudioFrame(t, ctx, client.out)
	client.in <- stepAudioTextFrame(`{"type":"tts.text.delta","data":{"session_id":"s","text":"audio"}}`)
	waitStepAudioFrame(t, ctx, upstream.out)
	close(upstream.in)
	require.Error(t, <-done)
	require.Len(t, billing.reserveRequests, 1)
	require.Empty(t, billing.commitRequests)
	require.Equal(t, 1, billing.releases)
}

func TestStepAudioRelayVADReservesBeforeFirstAudioWrite(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s, session, billing := newStepAudioTestBridge(t, "")
	client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
	done := make(chan error, 1)
	go func() { done <- s.bridgeStepAudioRealtime(ctx, client, upstream, session) }()
	require.Contains(t, waitStepAudioFrame(t, ctx, upstream.out), "session.update")
	client.in <- stepAudioTextFrame(`{"type":"session.update","session":{"turn_detection":{"type":"server_vad","prefix_padding_ms":0,"silence_duration_ms":20}}}`)
	require.JSONEq(t, `{"type":"session.update","session":{"turn_detection":null}}`, waitStepAudioFrame(t, ctx, upstream.out))
	pcm := make([]byte, 30*realtimePCMPerMS)
	for i := 0; i < 10*realtimePCMPerMS; i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], 6000)
	}
	for _, slice := range [][]byte{pcm[:240], pcm[240:]} {
		client.in <- stepAudioTextFrame(fmt.Sprintf(`{"type":"input_audio_buffer.append","audio":%q}`, base64.StdEncoding.EncodeToString(slice)))
	}
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "speech_started")
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "speech_stopped")
	require.Contains(t, waitStepAudioFrame(t, ctx, upstream.out), "input_audio_buffer.append")
	require.Len(t, billing.reserveRequests, 1)
	require.Contains(t, waitStepAudioFrame(t, ctx, upstream.out), "input_audio_buffer.commit")
	require.JSONEq(t, `{"type":"response.create"}`, waitStepAudioFrame(t, ctx, upstream.out))
	upstream.in <- stepAudioTextFrame(`{"type":"response.done","response":{"id":"r","usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`)
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "response.done")
	close(upstream.in)
	require.Error(t, <-done)
	require.Len(t, billing.commitRequests, 1)
	require.EqualValues(t, 7, billing.commitRequests[0].ActualTokens)
}

func TestStepAudioIndependentInputTranscriptionUsesMeasuredASR(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/audio/asr/sse", r.URL.Path)
		var input stepAudioASRRequest
		require.NoError(t, jsonx.NewDecoder(r.Body).Decode(&input))
		require.Equal(t, stepAudioASRModel, input.Audio.Input.Transcription.Model)
		require.EqualValues(t, realtimePCMRate, input.Audio.Input.Format.Rate)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"hello\"}\n\n")
	}))
	defer vendor.Close()
	s, session, billing := newStepAudioTestBridge(t, vendor.URL+"/v1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
	done := make(chan error, 1)
	go func() { done <- s.bridgeStepAudioRealtime(ctx, client, upstream, session) }()
	require.Contains(t, waitStepAudioFrame(t, ctx, upstream.out), "session.update")
	client.in <- stepAudioTextFrame(`{"type":"session.update","session":{"input_audio_transcription":{"model":"stepaudio-2.5-asr"}}}`)
	require.NotContains(t, waitStepAudioFrame(t, ctx, upstream.out), "input_audio_transcription")
	audio := base64.StdEncoding.EncodeToString(make([]byte, realtimePCMPerMS*100))
	client.in <- stepAudioTextFrame(fmt.Sprintf(`{"type":"input_audio_buffer.append","audio":%q}`, audio))
	client.in <- stepAudioTextFrame(`{"type":"input_audio_buffer.commit"}`)
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "input_audio_buffer.committed")
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "conversation.item.created")
	terminal := waitStepAudioFrame(t, ctx, client.out)
	require.Contains(t, terminal, "conversation.item.input_audio_transcription.completed")
	require.Contains(t, terminal, "hello")
	close(client.in)
	require.Error(t, <-done)
	require.Empty(t, upstream.out, "standalone transcription does not create a realtime response")
	require.Len(t, billing.reserveRequests, 1)
	require.EqualValues(t, 100_000, billing.reserveRequests[0].CostBound.AudioUnits)
	require.Len(t, billing.commitRequests, 1)
}

func TestStepAudioVADTranscriptionUsesUpstreamConversationItem(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"hello\"}\n\n")
	}))
	defer vendor.Close()
	s, session, billing := newStepAudioTestBridge(t, vendor.URL+"/v1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
	done := make(chan error, 1)
	go func() { done <- s.bridgeStepAudioRealtime(ctx, client, upstream, session) }()
	waitStepAudioFrame(t, ctx, upstream.out)
	client.in <- stepAudioTextFrame(`{"type":"session.update","session":{"turn_detection":{"type":"server_vad","silence_duration_ms":10},"input_audio_transcription":{"model":"stepaudio-2.5-asr"}}}`)
	waitStepAudioFrame(t, ctx, upstream.out)
	pcm := make([]byte, 120*realtimePCMPerMS)
	for i := 0; i < 110*realtimePCMPerMS; i += 2 {
		binary.LittleEndian.PutUint16(pcm[i:], 6000)
	}
	client.in <- stepAudioTextFrame(fmt.Sprintf(`{"type":"input_audio_buffer.append","audio":%q}`, base64.StdEncoding.EncodeToString(pcm)))
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "speech_started")
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "speech_stopped")
	require.Contains(t, waitStepAudioFrame(t, ctx, upstream.out), "input_audio_buffer.append")
	require.Contains(t, waitStepAudioFrame(t, ctx, upstream.out), "input_audio_buffer.commit")
	require.Contains(t, waitStepAudioFrame(t, ctx, upstream.out), "response.create")
	upstream.in <- stepAudioTextFrame(`{"type":"input_audio_buffer.committed","item_id":"vendor-item"}`)
	for range 2 {
		raw := waitStepAudioFrame(t, ctx, client.out)
		if strings.Contains(raw, "transcription.completed") {
			require.Contains(t, raw, `"item_id":"vendor-item"`)
		}
	}
	upstream.in <- stepAudioTextFrame(`{"type":"response.done","response":{"id":"r","usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`)
	require.Contains(t, waitStepAudioFrame(t, ctx, client.out), "response.done")
	close(upstream.in)
	require.Error(t, <-done)
	require.Len(t, billing.commitRequests, 2)
}

func TestStepAudioASRDisconnectKeepsPendingTerminal(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	providerFinished := make(chan struct{})
	vendor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.delta\",\"delta\":\"hi\"}\n\n")
		_ = http.NewResponseController(w).Flush()
		select {
		case <-providerFinished:
			_, _ = io.WriteString(w, "data: {\"type\":\"transcript.text.done\",\"text\":\"hi\"}\n\n")
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(vendor.Close)
	t.Cleanup(func() { close(providerFinished) })
	s, session, billing := newStepAudioTestBridge(t, vendor.URL+"/v1")
	testCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	clientCtx, disconnect := context.WithCancel(testCtx)
	defer disconnect()
	client, done := newFakeFrameConn(4), make(chan error, 1)
	go func() {
		done <- s.transcribeStepAudioInput(clientCtx, client, session, make([]byte, realtimePCMPerMS*100), "item")
	}()
	require.Contains(t, waitStepAudioFrame(t, testCtx, client.out), "transcription.delta")
	disconnect()
	select {
	case <-done:
		t.Fatal("client disconnect discarded the ASR stream before its terminal")
	case <-time.After(20 * time.Millisecond):
	}
	providerFinished <- struct{}{}
	require.NoError(t, <-done)
	require.Len(t, billing.commitRequests, 1)
	require.Zero(t, billing.releases)
}

func TestStepAudioRealtimeRejectsUnprovenTerminalUsage(t *testing.T) {
	const valid = `{"input_tokens":4,"output_tokens":3,"total_tokens":7}`
	for _, tc := range []struct {
		name, terminal string
	}{
		{"missing input count", `{"type":"response.done","response":{"id":"one","usage":{"output_tokens":7,"total_tokens":7}}}`},
		{"duplicate input count", `{"type":"response.done","response":{"id":"one","usage":{"input_tokens":40,"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`},
		{"malformed terminal status", `{"type":"response.done","response":{"id":"one","status":null,"usage":` + valid + `}}`},
		{"replayed earlier turn", `{"type":"response.done","response":{"id":"one","usage":` + valid + `}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			s, session, billing := newStepAudioTestBridge(t, "")
			client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
			done := make(chan error, 1)
			go func() { done <- s.bridgeStepAudioRealtime(ctx, client, upstream, session) }()
			waitStepAudioFrame(t, ctx, upstream.out)
			start := func() {
				client.in <- stepAudioTextFrame(`{"type":"response.create"}`)
				waitStepAudioFrame(t, ctx, upstream.out)
			}
			settled := 0
			if tc.name == "replayed earlier turn" {
				for _, id := range []string{"one", "two"} {
					start()
					upstream.in <- stepAudioTextFrame(fmt.Sprintf(`{"type":"response.done","response":{"id":%q,"usage":%s}}`, id, valid))
					waitStepAudioFrame(t, ctx, client.out)
					settled++
				}
			}
			start()
			upstream.in <- stepAudioTextFrame(tc.terminal)
			close(upstream.in)
			require.Error(t, <-done)
			require.Len(t, billing.commitRequests, settled)
			require.Equal(t, 1, billing.releases)
		})
	}
}

func TestStepAudioRealtimeDisconnectKeepsPendingTerminal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	s, session, billing := newStepAudioTestBridge(t, "")
	client, upstream := newFakeFrameConn(4), newFakeFrameConn(4)
	done := make(chan error, 1)
	go func() { done <- s.bridgeStepAudioRealtime(ctx, client, upstream, session) }()
	waitStepAudioFrame(t, ctx, upstream.out)
	client.in <- stepAudioTextFrame(`{"type":"response.create"}`)
	waitStepAudioFrame(t, ctx, upstream.out)
	close(client.in)
	select {
	case <-done:
		t.Fatal("client disconnect released a sent turn before the upstream terminal")
	case <-time.After(20 * time.Millisecond):
	}
	upstream.in <- stepAudioTextFrame(`{"type":"response.done","response":{"id":"one","usage":{"input_tokens":4,"output_tokens":3,"total_tokens":7}}}`)
	require.Error(t, <-done)
	require.Len(t, billing.commitRequests, 1)
	require.Zero(t, billing.releases)
}
