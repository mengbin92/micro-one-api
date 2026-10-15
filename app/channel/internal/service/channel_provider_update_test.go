package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
)

func TestChannelUpdateProviderPresencePreservesCredential(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		repo := &channelServiceRepo{channel: &biz.Channel{ID: 11, Type: 14, Name: "ds", Key: "fixture-key", Group: "default", Status: 1}}
		svc := NewChannelService(biz.NewChannelUsecase(repo, nil))
		req := &channelv1.UpdateChannelRequest{ChannelId: 11}
		if explicit {
			value := int32(6)
			req.Type = &value
		}
		reply, err := svc.UpdateChannel(context.Background(), req)
		require.NoError(t, err)
		require.True(t, reply.Success)
		want := int32(14)
		if explicit {
			want = 6
		}
		require.Equal(t, want, repo.updated.Type)
		require.Equal(t, "fixture-key", repo.updated.Key)
	}
	for _, invalid := range []int32{0, -1, 37} {
		svc := NewChannelService(biz.NewChannelUsecase(&channelServiceRepo{}, nil))
		_, err := svc.UpdateChannel(context.Background(), &channelv1.UpdateChannelRequest{ChannelId: 11, Type: &invalid})
		require.Error(t, err)
	}
}
