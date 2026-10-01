package biz

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

func (uc *IdentityUsecase) updateIAMRoutingAccess(ctx context.Context, c RoutingAccessChange) error {
	if c.ExpectedUserRevision == 0 || c.ExpectedPolicyRevision == 0 || strings.TrimSpace(c.Reason) == "" {
		return ErrIAMInvalidRelation
	}
	operation := map[string]string{"grant": "identity.routing_access.grant", "revoke": "identity.routing_access.revoke", "default": "identity.routing_access.default.update", "public_access": "identity.routing_access.public_access.update"}[c.Operation]
	if operation == "" {
		return ErrIAMInvalidRelation
	}
	if c.Operation == "grant" || c.Operation == "default" {
		group := User{Group: c.GroupKey}
		if err := uc.bindLegacyGroup(ctx, &group); err != nil {
			return err
		}
		if group.DefaultRoutingGroupID != c.GroupID {
			return ErrRoutingDefaultInvalid
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
	repo, ok := uc.iam.(UserAuthorizationTxRepo)
	if !ok {
		return ErrIAMDependencyUnavailable
	}
	return uc.runtimeWriteEvent(ctx, operation, strconv.FormatInt(c.UserID, 10), c.Reason, snapshot.Actor, func(ctx context.Context, tx IAMTx, event *IAMAuditEvent) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		rev, err := uc.iam.UserRevision(ctx, tx, c.UserID)
		if err != nil {
			return err
		}
		if rev != c.ExpectedUserRevision || v.policy.PolicyRevision != c.ExpectedPolicyRevision {
			return ErrIAMRevisionConflict
		}
		facts, err := repo.UserAuthorizationFactsTx(ctx, tx, c.UserID, v.now)
		if err != nil {
			return err
		}
		affected := facts
		affected.RoutingGroupIDs = slices.Clone(facts.RoutingGroupIDs)
		if c.GroupID > 0 && !slices.Contains(affected.RoutingGroupIDs, c.GroupID) {
			affected.RoutingGroupIDs = append(affected.RoutingGroupIDs, c.GroupID)
		}
		if c.Operation == "default" && c.UserID == v.actor.UserID {
			// Intrinsic default selection can only choose an already effective grant.
			if !slices.Contains(facts.RoutingGroupIDs, c.GroupID) {
				return ErrIAMProtected
			}
		} else {
			if err := v.permitUserResource(operation, affected); err != nil {
				return err
			}
			if c.Operation == "public_access" {
				q, err := v.resourceQuery(operation)
				if err != nil {
					return err
				}
				if !q.Global() {
					return ErrIAMProtected
				}
			}
		}
		before, err := uc.iam.User(ctx, tx, c.UserID)
		if err != nil {
			return err
		}
		if before.RoutingAccessRevision != c.ExpectedRevision {
			return ErrRoutingAccessConflict
		}
		payload, err := jsonx.Marshal(iamAccountAuditState(before))
		if err != nil {
			return err
		}
		event.Before = string(payload)
		if err := uc.iam.UpdateRoutingAccessTx(ctx, tx, c); err != nil {
			return err
		}
		after, err := uc.iam.User(ctx, tx, c.UserID)
		if err != nil {
			return err
		}
		payload, err = jsonx.Marshal(iamAccountAuditState(after))
		if err != nil {
			return err
		}
		event.After = string(payload)
		payload, err = jsonx.Marshal(map[string]any{"operation": c.Operation, "routing_group_id": c.GroupID})
		if err != nil {
			return err
		}
		event.Diff = string(payload)
		if before.RoutingAccessRevision == after.RoutingAccessRevision {
			return nil
		}
		if err := uc.iam.AdvanceUser(ctx, tx, c.UserID, rev); err != nil {
			return err
		}
		return uc.iam.AdvancePolicy(ctx, tx, v.policy.PolicyRevision, false)
	})
}
