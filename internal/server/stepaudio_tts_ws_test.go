package server

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/routing"
)

func TestStepAudioTTSTerminalAfterClientDisconnectStillSettles(t *testing.T) {
	s, session, billing := newStepAudioTestBridge(t, "")
	logs := &rawLogClient{}
	s.logClient = logs
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
	client.in <- stepAudioTextFrame(`{"type":"tts.text.delta","data":{"session_id":"s","text":"你好abc"}}`)
	waitStepAudioFrame(t, ctx, upstream.out)
	upstream.in <- stepAudioTextFrame(`{"type":"tts.response.audio.delta","data":{"session_id":"s","audio":"AA=="}}`)
	waitStepAudioFrame(t, ctx, client.out)

	close(client.in)
	select {
	case frame := <-upstream.out:
		require.JSONEq(t, `{"type":"tts.text.done","data":{"session_id":"s"}}`, string(frame.payload))
	case <-done:
		t.Fatal("relay released audio already streamed to the client without requesting the supplier terminal")
	case <-ctx.Done():
		t.Fatal("relay did not complete the already-forwarded text")
	}

	upstream.in <- stepAudioTextFrame(`{"type":"tts.response.audio.done","data":{"session_id":"s"}}`)
	require.Error(t, <-done)
	require.Len(t, billing.commitRequests, 1)
	require.EqualValues(t, 7, billing.commitRequests[0].ActualAudioUnits)
	require.Zero(t, billing.releases)
	require.Len(t, logs.entries, 1)
	require.Contains(t, logs.entries[0].Message, "actual_audio_units=7")
}

func TestStepAudioTTSRejectsAudioBeforeReservation(t *testing.T) {
	s, session, billing := newStepAudioTestBridge(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, upstream := newFakeFrameConn(4), newFakeFrameConn(4)
	done := make(chan error, 1)
	go func() { done <- s.bridgeStepAudioTTS(ctx, client, upstream, session) }()

	upstream.in <- stepAudioTextFrame(`{"type":"tts.connection.done","data":{"session_id":"s"}}`)
	waitStepAudioFrame(t, ctx, client.out)
	upstream.in <- stepAudioTextFrame(`{"type":"tts.response.audio.delta","data":{"session_id":"s","audio":"AA=="}}`)

	select {
	case frame := <-client.out:
		t.Fatalf("unreserved output reached client: %s", frame.payload)
	case err := <-done:
		require.ErrorContains(t, err, "reservation")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.Empty(t, billing.reserveRequests)
	require.Empty(t, billing.commitRequests)
}

func TestStepAudioTTSSupplierIdleTerminalSettlesForwardedInput(t *testing.T) {
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
	client.in <- stepAudioTextFrame(`{"type":"tts.text.delta","data":{"session_id":"s","text":"你好abc"}}`)
	waitStepAudioFrame(t, ctx, upstream.out)

	// The supplier may finish an idle session itself and return the full audio.
	terminal := `{"type":"tts.response.audio.done","data":{"session_id":"s","audio":"AA=="}}`
	upstream.in <- stepAudioTextFrame(terminal)
	select {
	case frame := <-client.out:
		require.Equal(t, terminal, string(frame.payload))
	case err := <-done:
		t.Fatalf("supplier completion was rejected: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NoError(t, <-done)
	require.Len(t, billing.commitRequests, 1)
	require.EqualValues(t, 7, billing.commitRequests[0].ActualAudioUnits)
	require.Zero(t, billing.releases)
}

func TestStepAudioTTSForwardsDocumentedMarkdownFilter(t *testing.T) {
	s, session, _ := newStepAudioTestBridge(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	upstream := newFakeFrameConn(1)
	payload := `{"type":"tts.create","data":{"session_id":"s","voice_id":"fixture","markdown_filter":true}}`
	state := &stepAudioTTSState{sessionID: "s"}
	require.NoError(t, s.writeStepAudioTTSClientEvent(ctx, ctx, upstream, session, state, []byte(payload)))
	require.JSONEq(t, payload, waitStepAudioFrame(t, ctx, upstream.out))
}

func TestStepAudioMeasuredReserveFailsClosedUnderNativeFinancePilot(t *testing.T) {
	s, session, billing := newStepAudioTestBridge(t, "")
	t.Setenv("RELAY_EXECUTION_DELIVERY_V1", "true")
	t.Setenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST", "channel:11:stepaudio-2.5-tts,channel:11:stepaudio-2.5-asr")
	const requestID = "step-audio-pilot"
	for _, bound := range []routing.CostBound{
		{Protocol: routing.StepAudioTTSWSProtocol, UpstreamModel: routing.StepAudioTTSModel, AudioUnits: 20},
		{Protocol: routing.AudioInputProtocol(routing.StepAudioTTSModel), UpstreamModel: routing.StepAudioTTSModel, AudioUnits: 20},
		{Protocol: routing.AudioInputProtocol(stepAudioASRModel), UpstreamModel: stepAudioASRModel, AudioUnits: 20},
	} {
		t.Run(bound.Protocol, func(t *testing.T) {
			ctx := channelAttemptContext(context.Background(), requestID, 1, session.channel, bound.UpstreamModel)
			ctx = routing.WithCostBound(ctx, bound)
			_, err := s.reserveQuota(ctx, "1", requestID, 0, bound.UpstreamModel, "11", 0, session.plan.Auth.RoutingContext)
			require.ErrorContains(t, err, "native execution")
			require.Empty(t, billing.reserveRequests)
		})
	}
}

type emptyStepAudioReservationID struct{ *stepAudioBillingFixture }

func (f emptyStepAudioReservationID) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	res, err := f.stepAudioBillingFixture.ReserveQuota(ctx, req, opts...)
	res.ReservationId = ""
	return res, err
}

func TestStepAudioMeasuredReserveRequiresID(t *testing.T) {
	s, session, billing := newStepAudioTestBridge(t, "")
	s.billingClient = emptyStepAudioReservationID{billing}
	const model, requestID = routing.StepAudioTTSModel, "step-audio-no-reservation-id"
	ctx := channelAttemptContext(context.Background(), requestID, 1, session.channel, model)
	ctx = routing.WithCostBound(ctx, routing.CostBound{
		Protocol: routing.StepAudioTTSWSProtocol, UpstreamModel: model, AudioUnits: routing.StepAudioTTSMaxUnits,
	})
	reservation, err := s.reserveQuota(ctx, "1", requestID, 0, model, "11", 0, session.plan.Auth.RoutingContext)
	require.ErrorContains(t, err, "reservation ID")
	require.Nil(t, reservation)
}
