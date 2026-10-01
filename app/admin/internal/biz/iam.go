package biz

import (
	"context"
	m "micro-one-api/domain/authorization/management"
)

// IAMRepo delegates authority to identity; admin never owns IAM persistence.
type IAMRepo interface {
	Execute(context.Context, string, string, m.Request) (m.Response, error)
}
type IAMUsecase struct{ repo IAMRepo }

func NewIAMUsecase(repo IAMRepo) *IAMUsecase { return &IAMUsecase{repo: repo} }
func (uc *IAMUsecase) Execute(ctx context.Context, credential, method string, req m.Request) (m.Response, error) {
	return uc.repo.Execute(ctx, credential, method, req)
}
