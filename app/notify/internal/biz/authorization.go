package biz

import (
	"context"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

func (uc *NotifyUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }
func (uc *NotifyUsecase) authorize(ctx context.Context, point, op string) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.Prepare(ctx, uc.authorization, point, op)
}
func (uc *NotifyUsecase) authorizeSystem(ctx context.Context, method, point, op string) error {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, method) {
		return nil
	}
	prepared, err := authorization.Prepare(ctx, uc.authorization, point, op)
	if err != nil {
		return err
	}
	if _, iam := authorization.QueryScopeFromContext(prepared, op); iam {
		return authorization.ErrDenied
	}
	return nil
}
