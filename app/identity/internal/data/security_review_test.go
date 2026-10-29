package data

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
)

type beforeSecurityWriteRunner struct {
	biz.IAMTxRunner
	before func()
}

func (r *beforeSecurityWriteRunner) RunIAMWrite(ctx context.Context, fn func(context.Context, biz.IAMTx) error) error {
	if r.before != nil {
		before := r.before
		r.before = nil
		before()
	}
	return r.IAMTxRunner.RunIAMWrite(ctx, fn)
}

func TestLegacyManagedEmailRechecksSecurityUnderWriteLock(t *testing.T) {
	for _, change := range []string{"expired proof", "revoked session"} {
		t.Run(change, func(t *testing.T) {
			f := newIAMA4Fixture(t, "sqlite")
			operator := f.register("security-operator")
			require.NoError(t, f.db.Table("users").Where("id = ?", operator.ID).Update("role", biz.RoleAdminUser).Error)
			target := f.register("security-target")
			srv := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
			t.Cleanup(func() { _ = rdb.Close() })
			d := &Data{db: f.db, redis: rdb}
			runner := &beforeSecurityWriteRunner{IAMTxRunner: NewIAMTxRunner(d)}
			uc := biz.NewIdentityUsecase(NewRepository(d), nil)
			uc.SetIAMRuntime(NewIAMRuntimeRepo(d), runner)
			challenges := NewChallengeRepo(d)
			uc.SetSecurity(NewSecurityRepo(d, []byte("01234567890123456789012345678901")), challenges, challenges.(biz.StepUpRepo))
			t.Setenv("IDENTITY_STEP_UP_ENABLED", "true")
			raw := f.token(operator.ID, "managed-email-operator", 0, time.Now().Add(time.Hour))
			require.NoError(t, uc.VerifyStepUp(f.ctx, raw, "password123", ""))
			runner.before = func() {
				if change == "expired proof" {
					srv.FastForward(5 * time.Minute)
				} else {
					require.NoError(t, f.db.Table("users").Where("id = ?", operator.ID).Update("password_changed_at", time.Now().UnixMilli()).Error)
				}
			}
			err := uc.UpdateUser(authorization.WithCredential(f.ctx, raw), target.ID, "", "takeover@example.test", "", biz.UserStatusEnabled)
			require.Error(t, err, "a proof checked before the write lock must be checked again inside it")
			stored, err := uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			require.Equal(t, target.Email, stored.Email)
		})
	}
}
