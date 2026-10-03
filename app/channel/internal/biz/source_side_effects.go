package biz

import "context"

// Compatibility resource editors may project several distinct operations.
// The owner evaluates only effects that actually change under the row lock.
func (uc *ChannelUsecase) prepareSourceSideEffects(ctx context.Context, account bool) (context.Context, error) {
	var err error
	entries := []struct{ point, op string }{{"channel.model_mappings", "channel.model_mapping.create"}, {"channel.model_mappings", "channel.model_mapping.update"}, {"channel.model_mappings", "channel.model_mapping.delete"}, {"channel.models.create", "channel.model.create"}}
	if account {
		entries = append(entries, struct{ point, op string }{"channel.models.pricing", "billing.pricing.update"})
	}
	for _, entry := range entries {
		ctx, err = uc.authorizeOptional(ctx, entry.point, entry.op)
		if err != nil {
			return ctx, err
		}
	}
	return ctx, nil
}
