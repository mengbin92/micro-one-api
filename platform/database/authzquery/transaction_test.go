package authzquery

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/domain/authorization"
	authztest "micro-one-api/domain/authorization/testutil"
)

type transactionPolicy struct {
	db       *gorm.DB
	queries  int
	revokeAt int
	mode     string
	actor    int64
}

func (r *transactionPolicy) Query(ctx context.Context, _, _, _ string) (authorization.ResourceAuthorization, error) {
	r.queries++
	// One connection makes this time out if called inside the transaction.
	// A policy call must finish before the replayable owner callback begins.
	var rows int64
	if err := r.db.WithContext(ctx).Table("effects").Count(&rows).Error; err != nil {
		return authorization.ResourceAuthorization{}, err
	}
	if r.revokeAt > 0 && r.queries >= r.revokeAt {
		return authorization.ResourceAuthorization{}, authorization.ErrDenied
	}
	q := authztest.All()
	q.Versions.Policy = uint64(r.queries)
	if r.actor > 0 {
		q.ActorID = r.actor
	}
	mode := r.mode
	if mode == "" {
		mode = "iam"
	}
	return authorization.ResourceAuthorization{Mode: mode, Query: q}, nil
}
func transactionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "refresh.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec("CREATE TABLE effects (id INTEGER PRIMARY KEY)").Error)
	return db
}
func TestIAMTransactionRetryRechecksRevocationBeforeReplay(t *testing.T) {
	db := transactionDB(t)
	policy := &transactionPolicy{db: db, revokeAt: 3}
	ctx, cancel := context.WithTimeout(authztest.Context(), 2*time.Second)
	defer cancel()
	ctx, err := authorization.Prepare(ctx, policy, "channel.channels.update", "channel.channel.update")
	require.NoError(t, err)
	writes := 0
	err = RunInTx(ctx, db, 3, func(current context.Context, tx *gorm.DB) error {
		writes++
		q, ok := authorization.QueryScopeFromContext(current, "channel.channel.update")
		require.True(t, ok)
		require.EqualValues(t, 2, q.Versions.Policy)
		require.NoError(t, tx.Exec("INSERT INTO effects (id) VALUES (1)").Error)
		return errors.New("database is locked")
	})
	require.ErrorIs(t, err, authorization.ErrDenied)
	require.Equal(t, 3, policy.queries, "preflight, first attempt, fresh retry decision")
	require.Equal(t, 1, writes, "revoked retry never starts its owner callback")
	var effects int64
	require.NoError(t, db.Table("effects").Count(&effects).Error)
	require.Zero(t, effects, "failed first attempt was rolled back")
}
func TestIAMTransactionRejectsChangedActorOrMode(t *testing.T) {
	for _, scenario := range []string{"actor", "mode", "revocation"} {
		t.Run(scenario, func(t *testing.T) {
			db := transactionDB(t)
			policy := &transactionPolicy{db: db}
			ctx, err := authorization.Prepare(authztest.Context(), policy, "channel.channels.update", "channel.channel.update")
			require.NoError(t, err)
			switch scenario {
			case "actor":
				policy.actor = 2
			case "mode":
				policy.mode = "legacy"
			case "revocation":
				policy.revokeAt = 2
			}
			wrote := false
			err = RunInTx(ctx, db, 3, func(context.Context, *gorm.DB) error { wrote = true; return nil })
			require.ErrorIs(t, err, authorization.ErrDenied)
			require.False(t, wrote)
		})
	}
}

type selfTransactionPolicy struct {
	transactionPolicy
	sessions int
	revoked  bool
}

func (r *selfTransactionPolicy) ResolveActor(context.Context, string, string) (authorization.Actor, string, error) {
	r.sessions++
	if r.revoked {
		return authorization.Actor{}, "iam", authorization.ErrDenied
	}
	return authorization.Actor{UserID: 1, ExpiresAt: time.Now().Add(time.Hour)}, "iam", nil
}
func TestIAMTransactionRechecksSelfSession(t *testing.T) {
	db := transactionDB(t)
	policy := &selfTransactionPolicy{transactionPolicy: transactionPolicy{db: db}}
	ctx, err := authorization.PrepareSelf(authztest.Context(), policy, "billing.self", "billing.payment.read")
	require.NoError(t, err)
	policy.revoked = true
	wrote := false
	err = RunInTx(ctx, db, 3, func(context.Context, *gorm.DB) error { wrote = true; return nil })
	require.ErrorIs(t, err, authorization.ErrDenied)
	require.False(t, wrote)
	require.Equal(t, 2, policy.sessions)
	require.Zero(t, policy.queries, "self path verifies the session rather than requesting an administrative grant")
}
