package data

import (
	"context"
	"testing"

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
	err := NewChannelAdapter(client).RecordModelHealth(context.Background(), "channel", 42, "gpt-4o", "gpt-4o-2024", false, "upstream unavailable", 125)
	if err != nil {
		t.Fatalf("RecordModelHealth() error = %v", err)
	}
	if client.modelHealthRequest == nil || client.modelHealthRequest.GetSourceId() != 42 || client.modelHealthRequest.GetUpstreamModelId() != "gpt-4o-2024" {
		t.Fatalf("unexpected request: %+v", client.modelHealthRequest)
	}
}

type legacyChannelClient struct {
	channelv1.ChannelServiceClient
}

func TestChannelAdapterLegacyClientDoesNotPanicOnModelHealth(t *testing.T) {
	err := NewChannelAdapter(&legacyChannelClient{}).RecordModelHealth(context.Background(), "channel", 42, "gpt-4o", "gpt-4o", true, "", 125)
	if err == nil {
		t.Fatal("expected unavailable error from legacy client")
	}
}
