package service

import (
	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
	"testing"
)

func TestIAMB2PermissionFailureRemainsAnError(t *testing.T) {
	svc := NewChannelService(biz.NewChannelUsecase(&channelServiceRepo{}, nil))
	svc.SetResourceAuthorizer(&authztest.Resolver{ActorID: 1, Scopes: map[string]authorization.QueryScope{}})
	reply, err := svc.CreateChannel(authztest.Context(), &channelv1.CreateChannelRequest{Name: "denied", Type: 1})
	require.ErrorIs(t, err, authorization.ErrDenied)
	require.Nil(t, reply)
}
