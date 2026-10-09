package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLegacyEmailRecoveryRequiresBoundFreshProof(t *testing.T) {
	user := &User{ID: 1, Email: "user@example.com", Status: UserStatusEnabled, PasswordChangedAt: 10, PasswordHash: "original"}
	uc := NewIdentityUsecase(&mockIdentityRepo{users: map[int64]*User{1: user}}, nil)
	now := time.Now()
	uc.now = func() time.Time { return now }
	for _, ctx := range []context.Context{
		context.Background(),
		WithVerifiedEmailRecovery(context.Background(), user.Email, now.Add(-2*time.Minute), 1, 10),
		WithVerifiedEmailRecovery(context.Background(), user.Email, now, 2, 10),
		WithVerifiedEmailRecovery(context.Background(), user.Email, now, 1, 9),
	} {
		require.Error(t, uc.ResetPasswordByEmail(ctx, user.Email, "new-password"))
		require.Equal(t, "original", user.PasswordHash)
	}
	proof := WithVerifiedEmailRecovery(context.Background(), user.Email, now, 1, 10)
	require.NoError(t, uc.ResetPasswordByEmail(proof, user.Email, "new-password"))
	require.NotEqual(t, "original", user.PasswordHash)
	require.Greater(t, user.PasswordChangedAt, int64(10))
	require.Error(t, uc.ResetPasswordByEmail(proof, user.Email, "another-password"))
}
