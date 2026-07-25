package data

import (
	"context"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"

	"google.golang.org/grpc"
)

type accountHealthClient struct {
	channelv1.ChannelServiceClient
	request *channelv1.RecordSubscriptionAccountHealthRequest
}

func (c *accountHealthClient) RecordSubscriptionAccountHealth(_ context.Context, req *channelv1.RecordSubscriptionAccountHealthRequest, _ ...grpc.CallOption) (*channelv1.RecordSubscriptionAccountHealthResponse, error) {
	c.request = req
	return &channelv1.RecordSubscriptionAccountHealthResponse{Success: true}, nil
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
