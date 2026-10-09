package biz

import (
	"context"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

type emailRecoveryKey struct{}
type emailRecoveryProof struct {
	Email         string
	VerifiedAt    time.Time
	UserID        int64
	PasswordEpoch int64
}

// Only the identity transport that consumed a one-use, purpose-specific mail
// challenge supplies this proof. No RPC/request field can manufacture it.
func WithVerifiedEmailRecovery(ctx context.Context, email string, verifiedAt time.Time, userID, passwordEpoch int64) context.Context {
	return context.WithValue(ctx, emailRecoveryKey{}, emailRecoveryProof{email, verifiedAt, userID, passwordEpoch})
}

func (uc *IdentityUsecase) resetIAMPasswordByEmail(ctx context.Context, email, password string) error {
	proof, ok := ctx.Value(emailRecoveryKey{}).(emailRecoveryProof)
	now := uc.now()
	if !ok || proof.UserID <= 0 || email == "" || proof.Email != email || proof.VerifiedAt.After(now.Add(time.Second)) || now.Sub(proof.VerifiedAt) > time.Minute {
		return ErrInvalidToken
	}
	if len(password) < 8 || len(password) > 72 {
		return ErrInvalidPassword
	}
	target, err := uc.repo.FindUserByEmail(ctx, email)
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return uc.runtimeWriteEvent(ctx, "credential.email.recovery", strconv.FormatInt(target.ID, 10), "verified single-use email recovery", authorization.Actor{ServiceID: "identity-email-recovery"}, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.CheckWrite(authorization.IAMManagementWrite, false) != nil {
			return ErrIAMCutoverBlocked
		}
		user, err := uc.iam.User(ctx, tx, target.ID)
		if err != nil {
			return err
		}
		if user.Status != UserStatusEnabled {
			return ErrUserDisabled
		}
		// A challenge sent before the address/account changed cannot reset the new
		// binding, including a change away and back while bcrypt was running.
		if user.ID != proof.UserID || user.Email != email || user.PasswordChangedAt != proof.PasswordEpoch {
			return ErrIAMRevisionConflict
		}
		state, err := uc.iam.ConstraintState(ctx, tx, authorization.Platform())
		if err != nil {
			return err
		}
		view := iamManagementView{state: state, now: uc.now()}
		if view.rootTarget(user.ID) {
			return ErrIAMProtected
		}
		before, err := jsonx.Marshal(iamAccountAuditState(user))
		if err != nil {
			return err
		}
		event.Before = string(before)
		user.PasswordHash = string(hash)
		user.PasswordChangedAt = nextPasswordEpoch(user.PasswordChangedAt, uc.now())
		if err := uc.iam.UpdateAccount(ctx, tx, user, []string{"password"}); err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, user.ID)
		if err != nil {
			return err
		}
		if err := uc.iam.AdvanceUser(ctx, tx, user.ID, rev); err != nil {
			return err
		}
		if err := uc.iam.RevokeUserSessions(ctx, tx, user.ID, uc.now()); err != nil {
			return err
		}
		after, err := jsonx.Marshal(iamAccountAuditState(user))
		if err != nil {
			return err
		}
		event.After = string(after)
		event.Diff = `{"fields":["password","password_epoch"]}`
		return uc.iam.AdvancePolicy(ctx, tx, p.PolicyRevision, false)
	})
}

// PrepareEmailRecovery captures the existing email binding for the mail
// transport's single-use record; no password hash crosses this boundary.
func (uc *IdentityUsecase) PrepareEmailRecovery(ctx context.Context, email string) (int64, int64, error) {
	user, err := uc.repo.FindUserByEmail(ctx, email)
	if err != nil {
		return 0, 0, err
	}
	if user.Status != UserStatusEnabled {
		return 0, 0, ErrUserDisabled
	}
	return user.ID, user.PasswordChangedAt, nil
}
