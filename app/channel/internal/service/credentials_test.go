package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
)

func TestCredentialClaimValidatesBoundaryAndReturnsRevision(t *testing.T) {
	svc := NewChannelService(biz.NewChannelUsecase(&channelServiceRepo{}, nil))
	for _, req := range []*channelv1.ClaimSubscriptionCredentialRefreshRequest{nil, {Id: 0}, {Id: 1, ExpectedRevision: -1}} {
		_, err := svc.ClaimSubscriptionCredentialRefresh(context.Background(), req)
		require.ErrorIs(t, err, biz.ErrCredentialConflict)
	}
	reply, err := svc.ClaimSubscriptionCredentialRefresh(context.Background(), &channelv1.ClaimSubscriptionCredentialRefreshRequest{Id: 1, ExpectedRevision: 4})
	require.NoError(t, err)
	require.EqualValues(t, 5, reply.Revision)
	info := toSubscriptionAccountInfo(&biz.SubscriptionAccount{CredentialRefreshPending: true, CredentialRevision: 5})
	require.True(t, info.CredentialRefreshPending)
}
