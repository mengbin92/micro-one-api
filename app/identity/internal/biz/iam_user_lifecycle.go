package biz

import (
	"context"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

// ManagedUserCreate admits only the fixed member defaults. Role governance and
// financial balances are separate owner operations, never fields of creation.
type ManagedUserCreate struct {
	Username, DisplayName, Email, Password, Group, Reason string
	ExpectedPolicyRevision                                uint64
}

func (uc *IdentityUsecase) CreateManagedUser(ctx context.Context, in ManagedUserCreate) (*User, error) {
	mode, err := uc.AuthorizationMode(ctx)
	if err != nil {
		return nil, err
	}
	if mode == "legacy" {
		return uc.CreateUser(ctx, in.Username, in.DisplayName, in.Email, in.Password, in.Group, 0)
	}
	if strings.TrimSpace(in.Username) == "" || len(in.Password) < 8 || strings.TrimSpace(in.Reason) == "" || in.ExpectedPolicyRevision == 0 {
		return nil, ErrIAMInvalidRelation
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	input := User{Username: in.Username, DisplayName: in.DisplayName, Email: in.Email, PasswordHash: string(hash), Group: registrationGroup(in.Group), Role: RoleCommonUser, Status: UserStatusEnabled}
	// An explicitly selected group is never replaced by the registration default.
	if in.Group != "" {
		input.Group = in.Group
	}
	if err := uc.bindLegacyGroup(ctx, &input); err != nil {
		return nil, err
	}
	snapshot, err := uc.GetSessionAuthorization(ctx, authorization.Credential(ctx), authorization.Platform())
	if err != nil {
		return nil, err
	}
	governance, err := uc.resourceGovernance()
	if err != nil {
		return nil, err
	}
	saved, _, err := uc.createIAMAccountChecked(ctx, input, false, true, snapshot.Actor, in.Reason, func(ctx context.Context, tx IAMTx, p authorization.PolicyState) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		if p.PolicyRevision != in.ExpectedPolicyRevision {
			return ErrIAMRevisionConflict
		}
		facts := authorization.ObjectFacts{Context: authorization.Platform()}
		if input.DefaultRoutingGroupID > 0 {
			facts.RoutingGroupIDs = []int64{input.DefaultRoutingGroupID}
		}
		if err := v.permitUserResource("identity.user.create", facts); err != nil {
			return err
		}
		// A management create sets a login password and may bind a recovery
		// email. Creation authority does not include those sensitive operations.
		for _, op := range managedCreateCredentialOperations(in) {
			if err := v.permitUserResource(op, facts); err != nil {
				return err
			}
		}
		if input.DefaultRoutingGroupID > 0 {
			for _, op := range []string{"identity.routing_access.default.update", "identity.routing_access.grant"} {
				if err := v.permitUserResource(op, facts); err != nil {
					return err
				}
			}
		}
		return nil
	}, func(ctx context.Context, tx IAMTx, saved User) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		for _, op := range managedCreateCredentialOperations(in) {
			if err := v.permitCredentialTarget(saved.ID, op); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func managedCreateCredentialOperations(in ManagedUserCreate) []string {
	ops := []string{"identity.user.credential.update"}
	if in.Email != "" {
		ops = append(ops, "identity.user.email_binding.update")
	}
	return ops
}

func (uc *IdentityUsecase) DeleteManagedUser(ctx context.Context, id int64, revision, policy uint64, reason string) error {
	mode, err := uc.AuthorizationMode(ctx)
	if err != nil {
		return err
	}
	if mode == "legacy" {
		return uc.DeleteUser(ctx, id)
	}
	if id <= 0 || revision == 0 || policy == 0 || strings.TrimSpace(reason) == "" {
		return ErrIAMInvalidRelation
	}
	snapshot, err := uc.GetSessionAuthorization(ctx, authorization.Credential(ctx), authorization.Platform())
	if err != nil {
		return err
	}
	governance, err := uc.resourceGovernance()
	if err != nil {
		return err
	}
	repo, ok := uc.iam.(UserAuthorizationTxRepo)
	if !ok {
		return ErrIAMDependencyUnavailable
	}
	return uc.runtimeWriteEvent(ctx, "identity.user.delete", strconv.FormatInt(id, 10), reason, snapshot.Actor, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, id)
		if err != nil {
			return err
		}
		if rev != revision || policy != v.policy.PolicyRevision {
			return ErrIAMRevisionConflict
		}
		if id == v.actor.UserID || v.rootTarget(id) {
			return ErrIAMProtected
		}
		facts, err := repo.UserAuthorizationFactsTx(ctx, tx, id, v.now)
		if err != nil {
			return err
		}
		if err := v.permitUserResource("identity.user.delete", facts); err != nil {
			return err
		}
		user, err := uc.iam.User(ctx, tx, id)
		if err != nil {
			return err
		}
		before, err := jsonx.Marshal(iamAccountAuditState(user))
		if err != nil {
			return err
		}
		event.Before = string(before)
		diff, err := jsonx.Marshal(map[string]any{"expected_target_revision": revision})
		if err != nil {
			return err
		}
		event.Diff = string(diff)
		if err := uc.iam.DeleteAccount(ctx, tx, id); err != nil {
			return err
		}
		return uc.iam.AdvancePolicy(ctx, tx, v.policy.PolicyRevision, false)
	})
}

func (uc *IdentityUsecase) DeleteSelf(ctx context.Context, id int64) error {
	mode, err := uc.AuthorizationMode(ctx)
	if err != nil {
		return err
	}
	if mode == "legacy" {
		return uc.DeleteUser(ctx, id)
	}
	snapshot, err := uc.GetSessionAuthorization(ctx, authorization.Credential(ctx), authorization.Platform())
	if err != nil {
		return err
	}
	if snapshot.Actor.UserID != id {
		return ErrIAMProtected
	}
	governance, err := uc.resourceGovernance()
	if err != nil {
		return err
	}
	return uc.runtimeWriteEvent(ctx, "account.self.delete", strconv.FormatInt(id, 10), "verified self deletion", snapshot.Actor, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		if v.rootTarget(id) {
			return ErrIAMProtected
		}
		user, err := uc.iam.User(ctx, tx, id)
		if err != nil {
			return err
		}
		before, err := jsonx.Marshal(iamAccountAuditState(user))
		if err != nil {
			return err
		}
		event.Before = string(before)
		if err := uc.iam.DeleteAccount(ctx, tx, id); err != nil {
			return err
		}
		return uc.iam.AdvancePolicy(ctx, tx, v.policy.PolicyRevision, false)
	})
}
