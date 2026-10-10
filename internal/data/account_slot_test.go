package data

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	channelv1 "micro-one-api/api/channel/v1"
)

type accountSlotRPCClient struct {
	channelv1.ChannelServiceClient
	request *channelv1.RecordSubscriptionAccountSlotRequest
}

func (c *accountSlotRPCClient) RecordSubscriptionAccountSlot(_ context.Context, req *channelv1.RecordSubscriptionAccountSlotRequest, _ ...grpc.CallOption) (*channelv1.RecordSubscriptionAccountSlotResponse, error) {
	c.request = req
	return &channelv1.RecordSubscriptionAccountSlotResponse{Success: true}, nil
}

func TestChannelClientsPreserveAccountSlotLeaseIdentity(t *testing.T) {
	for _, adapter := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct client", true: "adapter"}[adapter], func(t *testing.T) {
			client := &accountSlotRPCClient{}
			var err error
			if adapter {
				err = NewChannelAdapter(client).RecordSubscriptionAccountSlot(context.Background(), 42, "execution-lease", false)
			} else {
				err = (&channelClient{client: client}).RecordSubscriptionAccountSlot(context.Background(), 42, "execution-lease", false)
			}
			require.NoError(t, err)
			require.EqualValues(t, 42, client.request.GetAccountId())
			require.Equal(t, "execution-lease", client.request.GetSlotId())
			require.False(t, client.request.GetAcquired())
		})
	}
}
