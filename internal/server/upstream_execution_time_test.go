package server

import (
	"context"
	"fmt"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/evidence"
)

func TestDurableWSTurnsKeepTheirOwnSendTimestamp(t *testing.T) {
	t.Setenv("RELAY_UPSTREAM_EXECUTION_TIME_V1", "true")
	client, upstream := newFakeFrameConn(8), newFakeFrameConn(8)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	go func() {
		for n := 1; n <= 2; n++ {
			select {
			case <-ctx.Done():
				return
			case <-upstream.out:
				upstream.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_%d","usage":{"input_tokens":1,"output_tokens":1}}}`, n))}
			}
		}
	}()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-client.out:
			}
		}
	}()
	var records []evidence.Record
	turns := 0
	opts := openAIWSRelayOptions{writeTimeout: time.Second, idleTimeout: time.Second, durableRecord: func(r evidence.Record) error { records = append(records, r); return nil }, onTurnComplete: func(openAIWSTurnResult) {
		turns++
		if turns == 1 {
			client.in <- fakeFrame{msgType: coderws.MessageText, payload: []byte(`{"type":"response.create"}`)}
		} else {
			close(upstream.in)
		}
	}}
	_, exit := relayOpenAIWSFrames(ctx, client, upstream, []byte(`{"type":"response.create"}`), opts)
	require.NotNil(t, exit)
	require.Len(t, records, 2)
	require.Greater(t, records[0].StartedAtUnixNano, int64(0))
	require.Greater(t, records[1].StartedAtUnixNano, records[0].StartedAtUnixNano)
	require.Equal(t, "resp_1", records[0].Observation.ProviderRequestID)
	require.Equal(t, "resp_2", records[1].Observation.ProviderRequestID)
}
