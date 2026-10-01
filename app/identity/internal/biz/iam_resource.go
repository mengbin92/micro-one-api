package biz

import (
	"context"
	"slices"

	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

func (uc *IdentityUsecase) AuthorizationMode(ctx context.Context) (string, error) {
	if uc.iam == nil {
		return "legacy", nil
	}
	if uc.iamRunner == nil {
		return "", ErrIAMDependencyUnavailable
	}
	var mode string
	err := uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.Validate() != nil || (p.Cutover != "idle" && p.Cutover != "complete") {
			return ErrIAMCutoverBlocked
		}
		mode = p.Mode
		return nil
	})
	return mode, err
}

func (uc *IdentityUsecase) resourceGovernance() (*IAMGovernanceUsecase, error) {
	repo, ok := uc.iam.(IAMManagementRepo)
	if !ok || uc.iamRunner == nil {
		return nil, ErrIAMDependencyUnavailable
	}
	return NewIAMGovernanceUsecase(repo, uc.iamRunner, uc), nil
}

func (v iamManagementView) resourceQuery(op string) (authorization.QueryScope, error) {
	o, ok := authorization.Lookup(op)
	if !ok || !authorization.ResourceBound(op) {
		return authorization.QueryScope{}, ErrIAMInvalidRelation
	}
	return v.buildResourceQuery(o)
}

func (v iamManagementView) buildResourceQuery(o authorization.Operation) (authorization.QueryScope, error) {
	if !slices.ContainsFunc(v.permissions, func(p m.Permission) bool { return p.Code == o.Code && p.Status == "enabled" }) {
		return authorization.QueryScope{}, ErrIAMProtected
	}
	if v.session.ActivationState != "active" {
		return authorization.QueryScope{}, ErrIAMProtected
	}
	versions := authorization.Versions{User: v.userRevision, Policy: v.policy.PolicyRevision, Catalog: v.policy.CatalogRevision, Session: v.session.SessionRevision, SessionContext: v.session.Revision}
	q, err := authorization.QueryFromSources(v.actor, o, v.sources, v.now, versions)
	if err != nil {
		return q, ErrIAMInvalidRelation
	}
	if v.root {
		q.Allow = []authorization.Scope{{Clauses: []authorization.Clause{{All: true}}}}
	}
	if len(q.Allow) == 0 {
		return q, ErrIAMProtected
	}
	return q, nil
}

func (uc *IdentityUsecase) GetResourceAuthorization(ctx context.Context, raw string, req authorization.ResourceRequest) (authorization.ResourceAuthorization, error) {
	e, ok := authorization.Execution(req.ExecutionPoint)
	if !ok || !slices.Contains(e.Operations, req.Operation) {
		return authorization.ResourceAuthorization{}, ErrIAMInvalidRelation
	}
	if req.Object != nil {
		if req.Object.Context.RequirePlatform() != nil || req.Object.ResourceID < 0 || req.Object.OwnerUserID < 0 {
			return authorization.ResourceAuthorization{}, ErrIAMInvalidRelation
		}
		for _, id := range req.Object.RoutingGroupIDs {
			if id <= 0 {
				return authorization.ResourceAuthorization{}, ErrIAMInvalidRelation
			}
		}
	}
	mode, err := uc.AuthorizationMode(ctx)
	if err != nil {
		return authorization.ResourceAuthorization{}, err
	}
	if mode == "legacy" {
		return authorization.ResourceAuthorization{Mode: mode}, nil
	}
	snapshot, err := uc.GetSessionAuthorization(ctx, raw, authorization.Platform())
	if err != nil {
		return authorization.ResourceAuthorization{}, err
	}
	governance, err := uc.resourceGovernance()
	if err != nil {
		return authorization.ResourceAuthorization{}, err
	}
	out := authorization.ResourceAuthorization{Mode: mode}
	err = uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		v, err := governance.view(ctx, tx, snapshot.Actor, authorization.Platform())
		if err != nil {
			return err
		}
		out.Query, err = v.resourceQuery(req.Operation)
		if err != nil {
			return err
		}
		if req.Object != nil {
			op, _ := authorization.Lookup(req.Operation)
			allowed := out.Query.Matches(*req.Object, op.WholeObject)
			reason := "ALLOWED"
			if !allowed {
				reason = "SCOPE_DENIED"
			}
			out.Decision = &authorization.Decision{Allowed: allowed, Reason: reason, Context: authorization.Platform(), Operation: req.Operation, Versions: out.Query.Versions, ValidUntil: &out.Query.ValidUntil}
		}
		return nil
	})
	return out, err
}
