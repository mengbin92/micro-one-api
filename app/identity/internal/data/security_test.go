package data

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/totp"
)

func securityReplica(t *testing.T, f *iamA4Fixture, srv *miniredis.Miniredis) *biz.IdentityUsecase {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	d := &Data{db: f.db, redis: rdb}
	uc := biz.NewIdentityUsecase(NewRepository(d), nil)
	uc.SetIAMRuntime(NewIAMRuntimeRepo(d), NewIAMTxRunner(d))
	challenges := NewChallengeRepo(d)
	uc.SetSecurity(NewSecurityRepo(d, []byte("01234567890123456789012345678901")), challenges, challenges.(biz.StepUpRepo))
	return uc
}

func loginChallenge(t *testing.T, uc *biz.IdentityUsecase) string {
	t.Helper()
	_, raw, err := uc.Login(context.Background(), "security-user", "password123", "")
	var mfa *biz.MFARequired
	require.ErrorAs(t, err, &mfa)
	require.Empty(t, raw)
	return mfa.Challenge
}

func TestSecurityMFADialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			user := f.register("security-user")
			srv := miniredis.RunT(t)
			a, b := securityReplica(t, f, srv), securityReplica(t, f, srv)
			t.Setenv("IDENTITY_STEP_UP_ENABLED", "true")
			raw := f.token(user.ID, "before-enrollment", 0, time.Now().Add(time.Hour))
			other := f.token(user.ID, "other-session", 0, time.Now().Add(time.Hour))
			require.ErrorIs(t, a.RequireStepUp(f.ctx, raw, false), biz.ErrStepUpRequired)
			require.NoError(t, a.VerifyStepUp(f.ctx, raw, "password123", ""))
			require.NoError(t, b.RequireStepUp(f.ctx, raw, false))
			require.ErrorIs(t, b.RequireStepUp(f.ctx, other, false), biz.ErrStepUpRequired)
			secret, uri, _, err := b.ManageMFA(f.ctx, raw, "setup", "")
			require.NoError(t, err)
			require.Contains(t, uri, "otpauth://totp/")
			code, err := totp.Code(secret, time.Now().Unix()/30-1, 6)
			require.NoError(t, err)
			_, _, _, err = a.ManageMFA(f.ctx, other, "confirm", code)
			require.ErrorIs(t, err, biz.ErrMFAInvalid)
			_, _, codes, err := a.ManageMFA(f.ctx, raw, "confirm", code)
			require.NoError(t, err)
			require.Len(t, codes, 10)
			_, err = a.ValidateSessionToken(f.ctx, raw)
			require.Error(t, err, "enrollment invalidates old sessions")
			var stored mfaModel
			require.NoError(t, f.db.First(&stored, "user_id = ?", user.ID).Error)
			require.NotContains(t, stored.Secret, secret)
			require.NotContains(t, stored.RecoveryHashes, codes[0])
			require.Empty(t, stored.PendingSecret)

			// Password and OAuth share the same policy, and challenges are not JWT sessions.
			challenge := loginChallenge(t, a)
			_, err = b.ValidateSessionToken(f.ctx, challenge)
			require.Error(t, err)
			_, err = f.uc.BindOAuthIdentity(f.ctx, user.ID, "github", "security-subject")
			require.NoError(t, err)
			_, oauthToken, _, err := b.OAuthLogin(f.ctx, "github", "security-subject", "ignored", "", "")
			var needed *biz.MFARequired
			require.ErrorAs(t, err, &needed)
			require.Empty(t, oauthToken)

			// The same TOTP step used to enroll cannot authenticate again.
			_, _, err = b.CompleteMFALogin(f.ctx, challenge, code)
			require.ErrorIs(t, err, biz.ErrMFAInvalid)
			current, _ := totp.Code(secret, time.Now().Unix()/30, 6)
			_, session, err := b.CompleteMFALogin(f.ctx, challenge, current)
			require.NoError(t, err)
			_, _, err = a.CompleteMFALogin(f.ctx, challenge, codes[0])
			require.Error(t, err, "challenge remains consumed with a different valid factor")
			require.ErrorIs(t, a.VerifyStepUp(f.ctx, session, "", current), biz.ErrMFAInvalid)

			// Distinct challenges cannot consume the same TOTP step twice.
			nextCode, _ := totp.Code(secret, time.Now().Unix()/30+1, 6)
			challenges := []string{loginChallenge(t, a), loginChallenge(t, b)}
			var totpSuccess atomic.Int32
			var totpWG sync.WaitGroup
			for i := range challenges {
				totpWG.Add(1)
				go func(i int) {
					defer totpWG.Done()
					_, _, e := b.CompleteMFALogin(f.ctx, challenges[i], nextCode)
					if e == nil {
						totpSuccess.Add(1)
					}
				}(i)
			}
			totpWG.Wait()
			require.EqualValues(t, 1, totpSuccess.Load())
			// Both replicas race on one challenge and one recovery code.
			challenge = loginChallenge(t, a)
			var success atomic.Int32
			var wg sync.WaitGroup
			for i := 0; i < 4; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					uc := a
					if i%2 == 1 {
						uc = b
					}
					_, _, e := uc.CompleteMFALogin(f.ctx, challenge, codes[0])
					if e == nil {
						success.Add(1)
					}
				}(i)
			}
			wg.Wait()
			require.EqualValues(t, 1, success.Load())
			_, _, err = b.CompleteMFALogin(f.ctx, loginChallenge(t, b), codes[0])
			require.Error(t, err, "recovery code stays consumed across fresh challenges")

			require.NoError(t, b.VerifyStepUp(f.ctx, session, "", codes[1]))
			require.NoError(t, a.RequireStepUp(f.ctx, session, false))
			srv.FastForward(5 * time.Minute)
			require.ErrorIs(t, a.RequireStepUp(f.ctx, session, false), biz.ErrStepUpRequired)
			require.NoError(t, b.VerifyStepUp(f.ctx, session, "", codes[2]))
			_, _, rotated, err := a.ManageMFA(f.ctx, session, "recovery", "")
			require.NoError(t, err)
			require.Len(t, rotated, 10)
			require.Error(t, a.RequireStepUp(f.ctx, session, false))
			_, _, err = b.CompleteMFALogin(f.ctx, loginChallenge(t, b), codes[3])
			require.Error(t, err, "rotation invalidates old recovery hashes")
			_, recovered, err := a.CompleteMFALogin(f.ctx, loginChallenge(t, a), rotated[0])
			require.NoError(t, err)
			require.NoError(t, b.VerifyStepUp(f.ctx, recovered, "", rotated[1]))
			_, _, _, err = a.ManageMFA(f.ctx, recovered, "disable", "")
			require.NoError(t, err)
			_, plain, err := b.Login(f.ctx, "security-user", "password123", "")
			require.NoError(t, err)
			require.NotEmpty(t, plain)
			t.Setenv("IDENTITY_MFA_REQUIRE_ADMIN", "true")
			require.ErrorIs(t, a.RequireStepUp(f.ctx, plain, true), biz.ErrMFAEnrollmentRequired)
		})
	}
}

func TestSecurityProofRevocationAndFailures(t *testing.T) {
	f := newIAMA4Fixture(t, "sqlite")
	_, err := f.uc.EnsureRootAdmin(f.ctx)
	require.NoError(t, err)
	u := f.register("security-user")
	f.mode("iam", "complete")
	srv := miniredis.RunT(t)
	a, b := securityReplica(t, f, srv), securityReplica(t, f, srv)
	t.Setenv("IDENTITY_STEP_UP_ENABLED", "true")
	raw := f.token(u.ID, "revocation-test", 0, time.Now().Add(time.Hour))
	for i := 0; i < 5; i++ {
		err = a.VerifyStepUp(f.ctx, raw, "wrong", "")
		require.Error(t, err)
	}
	require.ErrorIs(t, b.VerifyStepUp(f.ctx, raw, "password123", ""), biz.ErrMFALocked)
	require.NoError(t, f.db.Table("identity_mfa").Where("user_id = ?", u.ID).Updates(map[string]any{"locked_until": time.Now().Add(-time.Minute).UnixMilli()}).Error)
	require.NoError(t, b.VerifyStepUp(f.ctx, raw, "password123", ""))
	require.NoError(t, a.RequireStepUp(f.ctx, raw, false))
	snapshot, err := a.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
	require.NoError(t, err)
	require.NoError(t, b.RevokeOwnSession(f.ctx, raw, authorization.Platform(), true, snapshot.Session.SessionRevision, "security test"))
	require.Error(t, a.RequireStepUp(f.ctx, raw, false))
	// A missing Redis dependency cannot create a successful proof.
	other := f.token(u.ID, "unavailable", 0, time.Now().Add(time.Hour))
	srv.Close()
	err = b.VerifyStepUp(f.ctx, other, "password123", "")
	require.True(t, errors.Is(err, biz.ErrMFAUnavailable))
}

func TestOAuthOnlyEnrollmentAndEncryptionFailures(t *testing.T) {
	f := newIAMA4Fixture(t, "sqlite")
	srv := miniredis.RunT(t)
	a, b := securityReplica(t, f, srv), securityReplica(t, f, srv)
	u, raw, _, err := a.OAuthLogin(f.ctx, "github", "oauth-only-subject", "oauth-only", "", "")
	require.NoError(t, err)
	require.Empty(t, u.PasswordHash)
	status, err := b.GetSecurityStatus(f.ctx, raw)
	require.NoError(t, err)
	require.False(t, status.PasswordAvailable)
	secret, _, _, err := b.ManageMFA(f.ctx, raw, "setup", "")
	require.NoError(t, err)
	otp, _ := totp.Code(secret, time.Now().Unix()/30-1, 6)
	_, _, _, err = b.ManageMFA(f.ctx, raw, "confirm", otp)
	require.NoError(t, err)
	wrongData := &Data{db: f.db}
	b.SetIAMRuntime(NewIAMRuntimeRepo(wrongData), NewIAMTxRunner(wrongData))
	otherKey := NewSecurityRepo(wrongData, []byte("abcdef0123456789abcdef0123456789"))
	rdb := redis.NewClient(&redis.Options{Addr: srv.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	challenges := NewChallengeRepo(&Data{redis: rdb})
	b.SetSecurity(otherKey, challenges, challenges.(biz.StepUpRepo))
	_, token, _, err := b.OAuthLogin(f.ctx, "github", "oauth-only-subject", "oauth-only", "", "")
	require.ErrorIs(t, err, biz.ErrMFAUnavailable)
	require.Empty(t, token)
}

func TestSecurityReenrollmentUsesFreshTOTPWatermark(t *testing.T) {
	f := newIAMA4Fixture(t, "sqlite")
	f.register("security-user")
	srv := miniredis.RunT(t)
	a := securityReplica(t, f, srv)
	_, raw, err := a.Login(f.ctx, "security-user", "password123", "")
	require.NoError(t, err)
	require.NoError(t, a.VerifyStepUp(f.ctx, raw, "password123", ""))
	secret, _, _, err := a.ManageMFA(f.ctx, raw, "setup", "")
	require.NoError(t, err)
	code, err := totp.Code(secret, time.Now().Unix()/30-1, 6)
	require.NoError(t, err)
	_, _, codes, err := a.ManageMFA(f.ctx, raw, "confirm", code)
	require.NoError(t, err)
	_, raw, err = a.CompleteMFALogin(f.ctx, loginChallenge(t, a), codes[0])
	require.NoError(t, err)
	// An authenticator one step ahead is valid for the old secret.
	code, err = totp.Code(secret, time.Now().Unix()/30+1, 6)
	require.NoError(t, err)
	require.NoError(t, a.VerifyStepUp(f.ctx, raw, "", code))
	_, _, _, err = a.ManageMFA(f.ctx, raw, "disable", "")
	require.NoError(t, err)
	_, raw, err = a.Login(f.ctx, "security-user", "password123", "")
	require.NoError(t, err)
	require.NoError(t, a.VerifyStepUp(f.ctx, raw, "password123", ""))
	secret, _, _, err = a.ManageMFA(f.ctx, raw, "setup", "")
	require.NoError(t, err)
	var state mfaModel
	require.NoError(t, f.db.First(&state).Error)
	require.EqualValues(t, -1, state.LastStep)
	code, err = totp.Code(secret, time.Now().Unix()/30, 6)
	require.NoError(t, err)
	_, _, codes, err = a.ManageMFA(f.ctx, raw, "confirm", code)
	require.NoError(t, err, "a new secret must not inherit the old secret's consumed time steps")
	require.Len(t, codes, 10)
}

func TestSecurityConsumedChallengeCannotLockAccount(t *testing.T) {
	f := newIAMA4Fixture(t, "sqlite")
	f.register("security-user")
	srv := miniredis.RunT(t)
	a := securityReplica(t, f, srv)
	_, raw, err := a.Login(f.ctx, "security-user", "password123", "")
	require.NoError(t, err)
	require.NoError(t, a.VerifyStepUp(f.ctx, raw, "password123", ""))
	secret, _, _, err := a.ManageMFA(f.ctx, raw, "setup", "")
	require.NoError(t, err)
	code, err := totp.Code(secret, time.Now().Unix()/30-1, 6)
	require.NoError(t, err)
	_, _, recovery, err := a.ManageMFA(f.ctx, raw, "confirm", code)
	require.NoError(t, err)
	challenge := loginChallenge(t, a)
	_, _, err = a.CompleteMFALogin(f.ctx, challenge, recovery[0])
	require.NoError(t, err)
	// A stolen or retried consumed challenge no longer grants any factor attempts.
	for i := 0; i < 5; i++ {
		_, _, err = a.CompleteMFALogin(f.ctx, challenge, "invalid")
		require.Error(t, err)
	}
	var state mfaModel
	require.NoError(t, f.db.First(&state).Error)
	require.Zero(t, state.Failures)
	require.Zero(t, state.LockedUntil)
	_, _, err = a.CompleteMFALogin(f.ctx, loginChallenge(t, a), recovery[1])
	require.NoError(t, err)
}
