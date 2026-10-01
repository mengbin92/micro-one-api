package biz

import (
	"context"
	"crypto/subtle"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"micro-one-api/domain/authorization"
)

// RescueRootCredential is an independent, deployment-enabled recovery action.
// A6 exposes it only through a dedicated service-authenticated rescue adapter;
// ordinary IAM management can neither enable rescue nor set its credential.
// It cannot change role, email, username or status, or revive a disabled account.
func (uc *IdentityUsecase) RescueRootCredential(ctx context.Context, credential string, target int64, password, reason string, expectedUser, expectedPolicy uint64) error {
	expected := os.Getenv("ADMIN_TOKEN")
	if !strings.EqualFold(os.Getenv("IAM_RESCUE_ENABLED"), "true") || expected == "" || subtle.ConstantTimeCompare([]byte(credential), []byte(expected)) != 1 {
		return ErrIAMProtected
	}
	if target <= 0 || len(password) < 8 || len(password) > 72 || strings.TrimSpace(reason) == "" || expectedUser == 0 || expectedPolicy == 0 {
		return ErrIAMInvalidRelation
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return uc.runtimeWrite(ctx, "credential.rescue", strconv.FormatInt(target, 10), reason, authorization.Actor{ServiceID: "system/admin-token"}, func(ctx context.Context, tx IAMTx) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.CheckWrite(authorization.IAMManagementWrite, false) != nil {
			return ErrIAMCutoverBlocked
		}
		if p.PolicyRevision != expectedPolicy {
			return ErrIAMRevisionConflict
		}
		user, err := uc.iam.User(ctx, tx, target)
		if err != nil {
			return err
		}
		if user.Status != UserStatusEnabled {
			return ErrIAMProtected
		}
		state, err := uc.iam.ConstraintState(ctx, tx, authorization.Platform())
		if err != nil {
			return err
		}
		authorized, err := iamAuthorizedRoles(uc.now(), state, target)
		if err != nil {
			return err
		}
		root := false
		for _, r := range state.Roles {
			if r.Code == "root" && r.Builtin {
				for _, id := range authorized {
					if r.ID == id {
						root = true
					}
				}
			}
		}
		if !root {
			return ErrIAMProtected
		}
		rev, err := uc.iam.UserRevision(ctx, tx, target)
		if err != nil {
			return err
		}
		if rev != expectedUser {
			return ErrIAMRevisionConflict
		}
		user.PasswordHash = string(hash)
		user.PasswordChangedAt = nextPasswordEpoch(user.PasswordChangedAt, uc.now())
		if err = uc.iam.UpdateAccount(ctx, tx, user, []string{"password"}); err != nil {
			return err
		}
		if err = uc.iam.RevokeUserSessions(ctx, tx, target, uc.now()); err != nil {
			return err
		}
		if err = uc.iam.AdvanceUser(ctx, tx, target, rev); err != nil {
			return err
		}
		return uc.iam.AdvancePolicy(ctx, tx, p.PolicyRevision, false)
	})
}
