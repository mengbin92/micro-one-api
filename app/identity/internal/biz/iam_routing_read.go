package biz

import (
	"context"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
)

type UserRoutingTxRepo interface {
	UserRoutingFactsTx(context.Context, IAMTx, int64) (*routing.SubjectFacts, error)
}

func (uc *IdentityUsecase) ManagedUserRoutingFacts(ctx context.Context, id int64) (*routing.SubjectFacts, error) {
	snapshot, err := uc.GetSessionAuthorization(ctx, authorization.Credential(ctx), authorization.Platform())
	if err != nil {
		return nil, err
	}
	governance, err := uc.resourceGovernance()
	if err != nil {
		return nil, err
	}
	factsRepo, ok := uc.iam.(UserAuthorizationTxRepo)
	if !ok {
		return nil, ErrIAMDependencyUnavailable
	}
	repo, ok := uc.iam.(UserRoutingTxRepo)
	if !ok {
		return nil, ErrIAMDependencyUnavailable
	}
	var out *routing.SubjectFacts
	err = uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		if snapshot.Actor.UserID != id {
			facts, err := factsRepo.UserAuthorizationFactsTx(ctx, tx, id, v.now)
			if err != nil {
				return err
			}
			if err := v.permitUserResource("identity.routing_access.read", facts); err != nil {
				return err
			}
		}
		out, err = repo.UserRoutingFactsTx(ctx, tx, id)
		if err != nil {
			return err
		}
		out.AuthorizationRevision, err = uc.iam.UserRevision(ctx, tx, id)
		if err != nil {
			return err
		}
		out.AuthorizationPolicyRevision = v.policy.PolicyRevision
		return nil
	})
	return out, err
}
