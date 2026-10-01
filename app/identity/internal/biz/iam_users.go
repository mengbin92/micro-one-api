package biz

import (
	"context"
	"errors"
	"time"

	"micro-one-api/domain/authorization"
)

type UserAuthorizationRepo interface {
	UserAuthorizationFacts(context.Context, int64, time.Time) (authorization.ObjectFacts, error)
}

func (uc *IdentityUsecase) managedUserScope(ctx context.Context, point, operation string) (context.Context, *authorization.QueryScope, error) {
	out, err := uc.GetResourceAuthorization(ctx, authorization.Credential(ctx), authorization.ResourceRequest{ExecutionPoint: point, Operation: operation})
	if err != nil {
		return ctx, nil, err
	}
	if out.Mode == "legacy" {
		return ctx, nil, nil
	}
	return authorization.WithQueryScope(ctx, operation, out.Query), &out.Query, nil
}

func (uc *IdentityUsecase) userAuthorizationFacts(ctx context.Context, id int64) (authorization.ObjectFacts, error) {
	repo, ok := uc.repo.(UserAuthorizationRepo)
	if !ok {
		return authorization.ObjectFacts{}, ErrIAMDependencyUnavailable
	}
	return repo.UserAuthorizationFacts(ctx, id, uc.now())
}

func (uc *IdentityUsecase) redactManagedContact(ctx context.Context, point string, users []*User) error {
	_, scope, err := uc.managedUserScope(ctx, point, "identity.user.contact.read")
	if err != nil {
		if !errors.Is(err, ErrIAMProtected) {
			return err
		}
		for _, u := range users {
			u.Email = ""
		}
		return nil
	}
	if scope == nil {
		return nil
	}
	for _, u := range users {
		facts, err := uc.userAuthorizationFacts(ctx, u.ID)
		if err != nil {
			return err
		}
		if !scope.Matches(facts, false) {
			u.Email = ""
		}
		u.PasswordHash = ""
	}
	return nil
}

func (uc *IdentityUsecase) managedUserRevisions(ctx context.Context, users []*User) error {
	if uc.iam == nil {
		return nil
	}
	return uc.iamRunner.ReadIAMSnapshot(ctx, func(ctx context.Context, tx IAMTx) error {
		p, err := uc.iam.Policy(ctx, tx)
		if err != nil {
			return err
		}
		if p.Mode == "legacy" {
			return nil
		}
		if p.Cutover != "complete" {
			return ErrIAMCutoverBlocked
		}
		for _, u := range users {
			rev, err := uc.iam.UserRevision(ctx, tx, u.ID)
			if err != nil {
				return err
			}
			u.AuthorizationRevision, u.AuthorizationPolicyRevision = rev, p.PolicyRevision
		}
		return nil
	})
}

func (uc *IdentityUsecase) ListManagedUsers(ctx context.Context, page, size int32, keyword, group string, status int32) ([]*User, int64, error) {
	ctx, _, err := uc.managedUserScope(ctx, "identity.users.list", "identity.user.list")
	if err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	users, total, err := uc.repo.ListUsers(ctx, page, size, keyword, group, status)
	if err != nil {
		return nil, 0, err
	}
	if err := uc.redactManagedContact(ctx, "identity.users.list", users); err != nil {
		return nil, 0, err
	}
	if err := uc.managedUserRevisions(ctx, users); err != nil {
		return nil, 0, err
	}
	return users, total, nil
}

func (uc *IdentityUsecase) GetManagedUser(ctx context.Context, id int64) (*User, error) {
	ctx, scope, err := uc.managedUserScope(ctx, "identity.users.read", "identity.user.read")
	if err != nil {
		return nil, err
	}
	if scope != nil {
		facts, err := uc.userAuthorizationFacts(ctx, id)
		if err != nil {
			return nil, err
		}
		if !scope.Matches(facts, false) {
			return nil, ErrIAMProtected
		}
	}
	u, err := uc.repo.FindUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if err = uc.redactManagedContact(ctx, "identity.users.read", []*User{u}); err != nil {
		return nil, err
	}
	if err = uc.managedUserRevisions(ctx, []*User{u}); err != nil {
		return nil, err
	}
	return u, nil
}
