package biz

import (
	"context"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"slices"
	"strconv"
)

// Intrinsic self paths authenticate the exact JTI and re-read the user under
// the policy lock. They never accept a transport-supplied user ID as identity.
func (uc *IdentityUsecase) mutateIAMSelfAccount(ctx context.Context, id int64, action string, fields []string, change func(*User) error) error {
	if !slices.Contains([]string{"account.self.update", "account.self.email", "account.self.logout.all", "account.self.aff_code"}, action) {
		return ErrIAMProtected
	}
	snapshot, err := uc.GetSessionAuthorization(ctx, authorization.Credential(ctx), authorization.Platform())
	if err != nil {
		return err
	}
	if snapshot.Actor.UserID != id {
		return ErrIAMProtected
	}
	return uc.runtimeWriteEvent(ctx, action, strconv.FormatInt(id, 10), "verified self account mutation", snapshot.Actor, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.CheckWrite(authorization.IAMManagementWrite, false) != nil {
			return ErrIAMCutoverBlocked
		}
		u, err := uc.iam.User(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := checkIAMIdentity(snapshot.Actor, u, uc.now()); err != nil {
			return err
		}
		s, err := uc.iam.Session(ctx, tx, snapshot.Actor.SessionID, authorization.Platform())
		if err != nil {
			return err
		}
		if err := checkIAMSession(snapshot.Actor, s, uc.now()); err != nil {
			return err
		}
		before, _ := jsonx.Marshal(iamAccountAuditState(u))
		event.Before = string(before)
		old := u
		if err := change(&u); err != nil {
			return err
		}
		writeFields := slices.Clone(fields)
		if old.Email != u.Email {
			u.PasswordChangedAt = nextPasswordEpoch(u.PasswordChangedAt, uc.now())
			writeFields = append(writeFields, "password_epoch")
		}
		if err := uc.iam.UpdateAccount(ctx, tx, u, writeFields); err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := uc.iam.AdvanceUser(ctx, tx, id, rev); err != nil {
			return err
		}
		if old.PasswordChangedAt != u.PasswordChangedAt {
			if err := uc.iam.RevokeUserSessions(ctx, tx, id, uc.now()); err != nil {
				return err
			}
		}
		after, _ := jsonx.Marshal(iamAccountAuditState(u))
		event.After = string(after)
		diff, _ := jsonx.Marshal(map[string]any{"fields": writeFields})
		event.Diff = string(diff)
		return uc.iam.AdvancePolicy(ctx, tx, p.PolicyRevision, false)
	})
}
