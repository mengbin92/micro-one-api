package biz

import (
	"context"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

func (uc *ModelUsecase) SetAuthorization(r authorization.Resolver) {
	if uc != nil {
		uc.authorization = r
	}
}
func (uc *ModelUsecase) authorize(ctx context.Context, point, operation string) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	if uc == nil {
		return ctx, authorization.ErrDenied
	}
	return authorization.Prepare(ctx, uc.authorization, point, operation)
}
func (uc *ModelUsecase) authorizeOptional(ctx context.Context, point, operation string) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	if uc == nil {
		return ctx, authorization.ErrDenied
	}
	return authorization.PrepareOptional(ctx, uc.authorization, point, operation)
}
func modelFacts(id int64) authorization.ObjectFacts {
	return authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}
}
func modelPriceView(ctx context.Context, m *Model) *Model {
	if m == nil {
		return nil
	}
	out := *m
	out.PriceFieldsVisible = authorization.Require(ctx, "billing.pricing.read", modelFacts(m.ID)) == nil
	if q, scoped := authorization.QueryScopeFromContext(ctx, "channel.model_mapping.read"); scoped {
		out.MappingsVisible = len(q.Allow) > 0
	} else {
		out.MappingsVisible = true
	}
	if !out.PriceFieldsVisible {
		out.PricingInput, out.PricingOutput, out.PricingCacheRead = 0, 0, 0
	}
	return &out
}

// Upsert is classified by the locked stored row; possession of update cannot
// create, and possession of create cannot overwrite an existing mapping.
func (uc *ModelUsecase) authorizeMappingWrite(ctx context.Context) (context.Context, error) {
	var err error
	for _, op := range []string{"channel.model_mapping.create", "channel.model_mapping.update"} {
		ctx, err = uc.authorizeOptional(ctx, "channel.model_mappings", op)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}

func (uc *ModelUsecase) prepareModelDeletion(ctx context.Context) (context.Context, error) {
	var err error
	for _, entry := range []struct{ point, op string }{{"channel.model_aliases", "channel.model_alias.delete"}, {"channel.model_mappings", "channel.model_mapping.delete"}, {"channel.models.pricing", "billing.pricing.update"}} {
		ctx, err = uc.authorizeOptional(ctx, entry.point, entry.op)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}
