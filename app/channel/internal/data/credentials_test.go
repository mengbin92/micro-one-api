package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/channel/internal/biz"
	"testing"
	"time"
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

func TestPendingCredentialClaimRequiresNewAuthorization(t *testing.T) {
	ctx := context.Background()
	repo := setupChannelTestDB(t)
	a := &biz.SubscriptionAccount{Platform: "claude", AccessToken: "old", RefreshToken: "single-use"}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	preClaim := *a
	require.NoError(t, repo.ClaimSubscriptionCredentialRefresh(ctx, a))
	require.ErrorIs(t, repo.StoreSubscriptionCredentials(ctx, &preClaim), biz.ErrCredentialConflict)
	lateOwner := *a
	a.Name = "metadata edit must not clear the claim"
	require.ErrorIs(t, repo.UpdateSubscriptionAccount(ctx, a), biz.ErrCredentialConflict)
	a.AccessToken, a.RefreshToken = "manual-access", "manual-refresh"
	require.NoError(t, repo.UpdateSubscriptionAccount(ctx, a))
	require.False(t, a.CredentialRefreshPending)
	lateOwner.AccessToken, lateOwner.RefreshToken = "late", "late-refresh"
	require.ErrorIs(t, repo.StoreSubscriptionCredentials(ctx, &lateOwner), biz.ErrCredentialConflict)
	got, err := repo.FindSubscriptionAccountByID(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "manual-refresh", got.RefreshToken)
	require.False(t, got.CredentialRefreshPending)
}

func TestPendingCredentialClaimRemainsInColdAccountSweep(t *testing.T) {
	ctx := context.Background()
	repo := setupChannelTestDB(t)
	a := &biz.SubscriptionAccount{Platform: "claude", RefreshToken: "refresh", ExpiresAt: time.Now().Add(30 * 24 * time.Hour).Unix()}
	require.NoError(t, repo.CreateSubscriptionAccount(ctx, a))
	ids, err := repo.ListOAuthRefreshCandidates(ctx, time.Hour)
	require.NoError(t, err)
	require.Empty(t, ids)
	require.NoError(t, repo.ClaimSubscriptionCredentialRefresh(ctx, a))
	ids, err = repo.ListOAuthRefreshCandidates(ctx, time.Hour)
	require.NoError(t, err)
	require.Equal(t, []int64{a.ID}, ids)
}
