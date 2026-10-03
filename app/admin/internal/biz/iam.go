package biz

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

var ErrIAMResourceUnavailable = errors.ServiceUnavailable("AUTHORIZATION_DEPENDENCY_UNAVAILABLE", "resource authorization unavailable")

// IAMRepo delegates authority to identity; admin never owns IAM persistence.
type IAMRepo interface {
	Execute(context.Context, string, string, m.Request) (m.Response, error)
}

func (uc *IAMUsecase) ResourceAuthorization(ctx context.Context, credential string, req authorization.ResourceRequest) (authorization.ResourceAuthorization, error) {
	repo, ok := uc.repo.(interface {
		ResourceAuthorization(context.Context, string, authorization.ResourceRequest) (authorization.ResourceAuthorization, error)
	})
	if !ok {
		return authorization.ResourceAuthorization{}, ErrIAMResourceUnavailable
	}
	return repo.ResourceAuthorization(ctx, credential, req)
}

type IAMUsecase struct{ repo IAMRepo }

func NewIAMUsecase(repo IAMRepo) *IAMUsecase { return &IAMUsecase{repo: repo} }
func (uc *IAMUsecase) Execute(ctx context.Context, credential, method string, req m.Request) (m.Response, error) {
	return uc.repo.Execute(ctx, credential, method, req)
}

func (uc *IAMUsecase) ResourceMode(ctx context.Context, point string) (string, error) {
	repo, ok := uc.repo.(interface {
		ResourceMode(context.Context, string) (string, error)
	})
	if !ok {
		return "", ErrIAMResourceUnavailable
	}
	return repo.ResourceMode(ctx, point)
}
