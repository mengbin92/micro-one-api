package data

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
)

func TestEmailBindingCreationAndUpdateAreUniqueAcrossDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			owner, err := f.uc.Register(f.ctx, "mail-owner", "password123", "Owner@Example.com", "default")
			require.NoError(t, err)
			other := f.register("mail-other")
			_, err = f.uc.Register(f.ctx, "mail-duplicate", "password123", " owner@example.COM ", "default")
			require.ErrorIs(t, err, biz.ErrUserExists)
			require.ErrorIs(t, f.uc.UpdateSelfEmail(f.ctx, other.ID, "OWNER@example.com"), biz.ErrUserExists)
			stored, err := f.uc.GetUser(f.ctx, other.ID)
			require.NoError(t, err)
			require.Equal(t, other.Email, stored.Email, "a rejected binding must leave the existing account intact")
			// A case-only update of one's own unique address remains compatible.
			require.NoError(t, f.uc.UpdateSelfEmail(f.ctx, owner.ID, "owner@example.COM"))
			for _, name := range []string{"no-email-one", "no-email-two"} {
				_, err = f.uc.Register(f.ctx, name, "password123", "", "default")
				require.NoError(t, err, "empty email is not an exclusive binding")
			}

			// Two independent registration attempts race through the usecase's
			// early reads. The policy-locked owner must still admit only one.
			var winners atomic.Int32
			var wait sync.WaitGroup
			for i := range 4 {
				wait.Go(func() {
					_, err := f.uc.Register(f.ctx, fmt.Sprintf("mail-race-%d", i), "password123", "Race@Example.com", "default")
					if err == nil {
						winners.Add(1)
					} else {
						require.ErrorIs(t, err, biz.ErrUserExists)
					}
				})
			}
			wait.Wait()
			require.Equal(t, int32(1), winners.Load())
			var count int64
			require.NoError(t, f.db.Table("users").Where("LOWER(TRIM(email)) = ?", "race@example.com").Count(&count).Error)
			require.Equal(t, int64(1), count)
		})
	}
}

func TestHistoricalDuplicateEmailRecoveryFailsClosedAcrossDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			first := f.register("historical-first")
			second := f.register("historical-second")
			// Emulate rows predating binding checks, including old whitespace and
			// case variants. No schema migration deletes or picks a winner.
			require.NoError(t, f.db.Table("users").Where("id = ?", second.ID).Update("email", " "+strings.ToUpper(first.Email)+" ").Error)
			repo := NewRepository(&Data{db: f.db})
			for _, email := range []string{first.Email, strings.ToUpper(first.Email), " " + first.Email + " "} {
				got, err := repo.FindUserByEmail(f.ctx, email)
				require.Nil(t, got)
				require.ErrorIs(t, err, biz.ErrInvalidToken)
				_, _, err = f.uc.PrepareEmailRecovery(f.ctx, email)
				require.ErrorIs(t, err, biz.ErrInvalidToken)
			}
			proof := biz.WithVerifiedEmailRecovery(f.ctx, first.Email, time.Now(), first.ID, first.PasswordChangedAt)
			require.ErrorIs(t, f.uc.ResetPasswordByEmail(proof, first.Email, "unexpected-password"), biz.ErrInvalidToken)
			for _, user := range []*biz.User{first, second} {
				_, _, err := f.uc.Login(f.ctx, user.Username, "password123", "127.0.0.1")
				require.NoError(t, err, "recovery must not mutate either ambiguous account")
			}
		})
	}
}

func TestCaseInsensitiveEmailRecoveryRevokesSessionsAcrossDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		for _, mode := range []string{"legacy", "iam"} {
			t.Run(driver+"/"+mode, func(t *testing.T) {
				f := newIAMA4Fixture(t, driver)
				user, err := f.uc.Register(f.ctx, "case-recovery", "password123", "Case.Recovery@Example.com", "default")
				require.NoError(t, err)
				if mode == "iam" {
					f.mode("iam", "complete")
				}
				_, oldSession, err := f.uc.Login(f.ctx, user.Username, "password123", "127.0.0.1")
				require.NoError(t, err)
				email := strings.ToLower(user.Email)
				id, epoch, err := f.uc.PrepareEmailRecovery(f.ctx, email)
				require.NoError(t, err)
				require.Equal(t, user.ID, id)
				proof := biz.WithVerifiedEmailRecovery(f.ctx, email, time.Now(), id, epoch)
				require.NoError(t, f.uc.ResetPasswordByEmail(proof, email, "recovered-password"))
				_, err = f.uc.ValidateSessionToken(f.ctx, oldSession)
				require.ErrorIs(t, err, biz.ErrSessionRevoked)
				_, _, err = f.uc.Login(f.ctx, user.Username, "recovered-password", "127.0.0.1")
				require.NoError(t, err)
				require.ErrorIs(t, f.uc.ResetPasswordByEmail(proof, email, "replay-password"), biz.ErrIAMRevisionConflict)
			})
		}
	}
}

func TestMemoryEmailBindingsRejectConflictsAndAmbiguousRecovery(t *testing.T) {
	ctx := context.Background()
	repo := newTestRepo()
	first := &biz.User{Username: "first", Email: "Owner@Example.com"}
	second := &biz.User{Username: "second", Email: "other@example.com"}
	require.NoError(t, repo.CreateUser(ctx, first))
	require.NoError(t, repo.CreateUser(ctx, second))
	require.ErrorIs(t, repo.CreateUser(ctx, &biz.User{Username: "duplicate", Email: " owner@example.COM "}), biz.ErrUserExists)
	change, err := repo.FindUserByID(ctx, second.ID)
	require.NoError(t, err)
	change.Email = "OWNER@example.com"
	require.ErrorIs(t, repo.UpdateUser(ctx, change), biz.ErrUserExists)
	// Seed a legacy duplicate directly, bypassing the new write guard.
	repo.usersByID[second.ID].Email = " owner@example.com "
	got, err := repo.FindUserByEmail(ctx, "OWNER@example.com")
	require.Nil(t, got)
	require.ErrorIs(t, err, biz.ErrInvalidToken)
	uc := biz.NewIdentityUsecase(repo, nil)
	_, _, err = uc.PrepareEmailRecovery(ctx, "owner@example.com")
	require.ErrorIs(t, err, biz.ErrInvalidToken)
}
