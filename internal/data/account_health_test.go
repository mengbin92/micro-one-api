package data

import (
	"context"
	"sync"
	"testing"
	"time"

	channelv1 "micro-one-api/api/channel/v1"

	"google.golang.org/grpc"
)

type accountHealthClient struct {
	channelv1.ChannelServiceClient
	request            *channelv1.RecordSubscriptionAccountHealthRequest
	modelHealthRequest *channelv1.RecordModelHealthRequest
}

func (c *accountHealthClient) RecordSubscriptionAccountHealth(_ context.Context, req *channelv1.RecordSubscriptionAccountHealthRequest, _ ...grpc.CallOption) (*channelv1.RecordSubscriptionAccountHealthResponse, error) {
	c.request = req
	return &channelv1.RecordSubscriptionAccountHealthResponse{Success: true}, nil
}

func (c *accountHealthClient) RecordModelHealth(_ context.Context, req *channelv1.RecordModelHealthRequest, _ ...grpc.CallOption) (*channelv1.RecordModelHealthResponse, error) {
	c.modelHealthRequest = req
	return &channelv1.RecordModelHealthResponse{Success: true}, nil
}

func TestChannelClientsRecordSubscriptionAccountHealth(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*accountHealthClient) error
	}{
		{
			name: "adapter",
			call: func(client *accountHealthClient) error {
				return NewChannelAdapter(client).RecordSubscriptionAccountHealth(context.Background(), 42, false)
			},
		},
		{
			name: "direct client",
			call: func(client *accountHealthClient) error {
				return (&channelClient{client: client}).RecordSubscriptionAccountHealth(context.Background(), 42, false)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &accountHealthClient{}
			if err := test.call(client); err != nil {
				t.Fatalf("RecordSubscriptionAccountHealth() error = %v", err)
			}
			if client.request == nil || client.request.GetAccountId() != 42 || client.request.GetSuccess() {
				t.Fatalf("unexpected request: %+v", client.request)
			}
		})
	}
}

func TestChannelAdapterRecordsModelHealth(t *testing.T) {
	client := &accountHealthClient{}
	adapter := NewChannelAdapter(client)
	// Model health is queued for background submission; flush makes the test
	// deterministic.
	if err := adapter.RecordModelHealth(context.Background(), "channel", 42, "gpt-4o", "gpt-4o-2024", false, "upstream unavailable", 125); err != nil {
		t.Fatalf("RecordModelHealth() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := adapter.FlushModelHealth(ctx); err != nil {
		t.Fatalf("FlushModelHealth() error = %v", err)
	}
	if client.modelHealthRequest == nil || client.modelHealthRequest.GetSourceId() != 42 || client.modelHealthRequest.GetUpstreamModelId() != "gpt-4o-2024" {
		t.Fatalf("unexpected request: %+v", client.modelHealthRequest)
	}
}

type legacyChannelClient struct {
	channelv1.ChannelServiceClient
}

func TestChannelAdapterLegacyClientDoesNotPanicOnModelHealth(t *testing.T) {
	adapter := NewChannelAdapter(&legacyChannelClient{})
	// The panic surfaces on the worker goroutine and is recovered there; the
	// enqueue itself must succeed and the failure must be counted, never
	// escaping to crash the process.
	if err := adapter.RecordModelHealth(context.Background(), "channel", 42, "gpt-4o", "gpt-4o", true, "", 125); err != nil {
		t.Fatalf("RecordModelHealth() error = %v", err)
	}
	if err := adapter.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, _, failed := adapter.ModelHealthStats()
	if failed != 1 {
		t.Fatalf("expected 1 failed sample from legacy client, got %d", failed)
	}
}

func TestModelHealthQueueDropsWhenFull(t *testing.T) {
	block := make(chan struct{})
	client := &blockingModelHealthClient{block: block, started: make(chan struct{})}
	q := newModelHealthQueue(client)
	defer close(block)

	// First record starts the worker, which parks on the blocked RPC.
	if !q.record(&channelv1.RecordModelHealthRequest{SourceId: 1}) {
		t.Fatal("first sample must be accepted")
	}
	// Wait until the worker has actually entered the (blocked) RPC before
	// filling the queue. Without this handshake the test races the scheduler:
	// if the worker has not picked the first sample up yet, the queue still
	// holds it and the final "beyond depth" record is accepted instead of the
	// last fill slot being rejected.
	<-client.started
	// Fill the queue: the worker is busy, so the bounded channel fills.
	for i := 0; i < modelHealthQueueDepth; i++ {
		if !q.record(&channelv1.RecordModelHealthRequest{SourceId: int64(i + 2)}) {
			t.Fatalf("sample %d dropped while queue should still have room", i+2)
		}
	}
	if q.record(&channelv1.RecordModelHealthRequest{SourceId: 999}) {
		t.Fatal("sample beyond queue depth must be dropped")
	}
	_, dropped, _ := q.stats()
	if dropped != 1 {
		t.Fatalf("expected 1 dropped sample, got %d", dropped)
	}
}

func TestModelHealthQueueCloseDrains(t *testing.T) {
	client := &accountHealthClient{}
	q := newModelHealthQueue(client)
	for i := 0; i < 16; i++ {
		if !q.record(&channelv1.RecordModelHealthRequest{SourceId: int64(i)}) {
			t.Fatal("sample dropped unexpectedly")
		}
	}
	q.close(2 * time.Second)
	submitted, dropped, failed := q.stats()
	if submitted != 16 || dropped != 0 || failed != 0 {
		t.Fatalf("stats after drain = submitted %d, dropped %d, failed %d; want 16/0/0", submitted, dropped, failed)
	}
	if q.record(&channelv1.RecordModelHealthRequest{SourceId: 99}) {
		t.Fatal("record after close must be rejected")
	}
}

type blockingModelHealthClient struct {
	channelv1.ChannelServiceClient
	block chan struct{}
	// started is closed when the worker goroutine enters the first (blocked)
	// RPC, proving it has taken the first sample off the queue.
	started chan struct{}
	once    sync.Once
}

func (c *blockingModelHealthClient) RecordModelHealth(_ context.Context, _ *channelv1.RecordModelHealthRequest, _ ...grpc.CallOption) (*channelv1.RecordModelHealthResponse, error) {
	c.once.Do(func() { close(c.started) })
	<-c.block
	return &channelv1.RecordModelHealthResponse{Success: true}, nil
}
