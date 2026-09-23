package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"testing"
)

func TestCredentialCASFencesReauthorizationAndReplays(t *testing.T) {
	ctx := context.Background()
	repo := setupChannelTestDB(t)
	account := &biz.SubscriptionAccount{Platform: "claude", AccessToken: "old", RefreshToken: "old-refresh"}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, account))
	pending := *account
	pending.AccessToken, pending.RefreshToken = "rotated", "rotated-refresh"
	replay := pending
	require.NoError(t, repo.StoreSubscriptionCredentials(ctx, &pending))
	require.EqualValues(t, 1, pending.CredentialRevision)
	require.NoError(t, repo.StoreSubscriptionCredentials(ctx, &replay))
	require.EqualValues(t, 1, replay.CredentialRevision)
	manual, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	stale := *manual
	manual.AccessToken, manual.RefreshToken = "reauthorized", "manual-refresh"
	require.NoError(t, repo.UpdateSubscriptionAccount(ctx, manual))
	stale.AccessToken, stale.RefreshToken = "late", "late-refresh"
	require.ErrorIs(t, repo.StoreSubscriptionCredentials(ctx, &stale), biz.ErrCredentialConflict)
	require.ErrorIs(t, repo.UpdateSubscriptionAccount(ctx, &stale), biz.ErrCredentialConflict)
	got, err := repo.FindSubscriptionAccountByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "manual-refresh", got.RefreshToken)
}
