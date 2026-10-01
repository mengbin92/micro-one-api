package biz

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

type ManagedUserPatch struct {
	DisplayName, Email, Group, Password      string
	Status                                   int32
	Fields                                   []string
	ExpectedRevision, ExpectedPolicyRevision uint64
	Reason                                   string
}
type UserAuthorizationTxRepo interface {
	UserAuthorizationFactsTx(context.Context, IAMTx, int64, time.Time) (authorization.ObjectFacts, error)
}

func (v iamManagementView) permitUserResource(op string, facts authorization.ObjectFacts) error {
	q, err := v.resourceQuery(op)
	if err != nil {
		return err
	}
	o, ok := authorization.Lookup(op)
	if !ok {
		return ErrIAMInvalidRelation
	}
	if !q.Matches(facts, o.WholeObject) {
		return ErrIAMProtected
	}
	return nil
}

func (v iamManagementView) rootTarget(uid int64) bool {
	ids, err := iamFutureRoles(v.state, uid, v.now)
	if err != nil {
		return true
	}
	return slices.ContainsFunc(v.state.Roles, func(r IAMRole) bool { return r.Builtin && r.Code == "root" && slices.Contains(ids, r.ID) })
}

func (v iamManagementView) permitCredentialTarget(uid int64, operation string) error {
	if uid == v.actor.UserID || v.rootTarget(uid) {
		return ErrIAMProtected
	}
	if v.root {
		return nil
	}
	for _, d := range v.delegations {
		if d.Context != authorization.Platform() || !slices.Contains(v.active, d.ManagerRoleID) || !slices.Contains(d.Actions, operation) {
			continue
		}
		if IAMCheckCredentialTakeover(v.now, v.state, v.delegations, v.actor.UserID, uid, d) == nil {
			return nil
		}
	}
	return ErrIAMProtected
}

func (uc *IdentityUsecase) UpdateManagedUser(ctx context.Context, id int64, patch ManagedUserPatch) error {
	mode, err := uc.AuthorizationMode(ctx)
	if err != nil {
		return err
	}
	if mode == "legacy" {
		return uc.UpdateUser(ctx, id, patch.DisplayName, patch.Email, patch.Group, patch.Status)
	}
	if id <= 0 || len(patch.Fields) == 0 || patch.ExpectedRevision == 0 || patch.ExpectedPolicyRevision == 0 || strings.TrimSpace(patch.Reason) == "" {
		return ErrIAMInvalidRelation
	}
	seen := map[string]bool{}
	for _, field := range patch.Fields {
		if seen[field] || !slices.Contains([]string{"display_name", "email", "group", "status", "password"}, field) {
			return ErrIAMInvalidRelation
		}
		seen[field] = true
	}
	var hash string
	if seen["password"] {
		if len(patch.Password) < 8 {
			return ErrInvalidPassword
		}
		b, err := bcrypt.GenerateFromPassword([]byte(patch.Password), bcrypt.DefaultCost)
		if err != nil {
			return err
		}
		hash = string(b)
	}
	group := User{Group: patch.Group}
	if seen["group"] {
		if strings.TrimSpace(patch.Group) == "" || !RoutingV2Enabled() {
			return ErrRoutingDefaultInvalid
		}
		if err := uc.bindLegacyGroup(ctx, &group); err != nil {
			return err
		}
	}
	snapshot, err := uc.GetSessionAuthorization(ctx, authorization.Credential(ctx), authorization.Platform())
	if err != nil {
		return err
	}
	governance, err := uc.resourceGovernance()
	if err != nil {
		return err
	}
	factsRepo, ok := uc.iam.(UserAuthorizationTxRepo)
	if !ok {
		return ErrIAMDependencyUnavailable
	}
	return uc.runtimeWriteEvent(ctx, "identity.user.update", strconv.FormatInt(id, 10), patch.Reason, snapshot.Actor, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, id)
		if err != nil {
			return err
		}
		if rev != patch.ExpectedRevision || v.policy.PolicyRevision != patch.ExpectedPolicyRevision {
			return ErrIAMRevisionConflict
		}
		u, err := uc.iam.User(ctx, tx, id)
		if err != nil {
			return err
		}
		facts, err := factsRepo.UserAuthorizationFactsTx(ctx, tx, id, v.now)
		if err != nil {
			return err
		}
		before, _ := jsonx.Marshal(iamAccountAuditState(u))
		event.Before = string(before)
		credentialChanged := false
		for _, field := range patch.Fields {
			op := "identity.user.update"
			switch field {
			case "status":
				if patch.Status != UserStatusEnabled && patch.Status != UserStatusDisabled {
					return ErrIAMInvalidRelation
				}
				if v.rootTarget(id) || id == v.actor.UserID {
					return ErrIAMProtected
				}
				if patch.Status == UserStatusEnabled {
					op = "identity.user.enable"
				} else {
					op = "identity.user.disable"
				}
			case "email":
				op = "identity.user.email_binding.update"
			case "password":
				op = "identity.user.credential.update"
			}
			if err := v.permitUserResource(op, facts); err != nil {
				return err
			}
			if field == "email" || field == "password" {
				if err := v.permitCredentialTarget(id, op); err != nil {
					return err
				}
				credentialChanged = true
			}
		}
		if seen["display_name"] {
			u.DisplayName = patch.DisplayName
		}
		if seen["email"] {
			u.Email = patch.Email
		}
		if seen["password"] {
			u.PasswordHash = hash
		}
		if seen["status"] {
			u.Status = patch.Status
		}
		if seen["group"] && group.DefaultRoutingGroupID != u.DefaultRoutingGroupID {
			// The legacy projection changes default, removes its original grant,
			// and inserts the target grant. Authorize all three actual effects.
			all := facts
			all.RoutingGroupIDs = slices.Clone(facts.RoutingGroupIDs)
			all.RoutingGroupIDs = append(all.RoutingGroupIDs, group.DefaultRoutingGroupID)
			for _, op := range []string{"identity.routing_access.default.update", "identity.routing_access.grant", "identity.routing_access.revoke"} {
				if err := v.permitUserResource(op, all); err != nil {
					return err
				}
			}
			u.Group, u.DefaultRoutingGroupID = patch.Group, group.DefaultRoutingGroupID
		}
		fields := slices.Clone(patch.Fields)
		if credentialChanged {
			u.PasswordChangedAt = nextPasswordEpoch(u.PasswordChangedAt, v.now)
			fields = append(fields, "password_epoch")
		}
		if err := uc.iam.UpdateAccount(ctx, tx, u, fields); err != nil {
			return err
		}
		if err := uc.iam.AdvanceUser(ctx, tx, id, rev); err != nil {
			return err
		}
		if credentialChanged || seen["status"] {
			if err := uc.iam.RevokeUserSessions(ctx, tx, id, v.now); err != nil {
				return err
			}
		}
		after, _ := jsonx.Marshal(iamAccountAuditState(u))
		event.After = string(after)
		diff, _ := jsonx.Marshal(map[string]any{"fields": patch.Fields, "expected_target_revision": patch.ExpectedRevision, "committed_target_revision": patch.ExpectedRevision + 1})
		event.Diff = string(diff)
		return uc.iam.AdvancePolicy(ctx, tx, v.policy.PolicyRevision, false)
	})
}
