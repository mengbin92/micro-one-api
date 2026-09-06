package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"micro-one-api/domain/routing"
)

// routeChannelAbilities is the authorization seam shared by selection,
// exclusions, catalogue verification and exact-source authorization. Preserve
// the repository's registry/legacy precedence and its no-match error: a
// registry denial must not widen into the unrestricted fallback.
func (uc *ChannelUsecase) routeChannelAbilities(ctx context.Context, group, model string) ([]Ability, error) {
	if group == "" || model == "" {
		return nil, ErrChannelNotFound
	}
	return uc.repo.ListAbilitiesByGroupAndModel(ctx, group, model)
}

func (uc *ChannelUsecase) routeSubscriptionAbilities(ctx context.Context, group, model, platform string) ([]SubscriptionAccountAbility, error) {
	if group == "" || model == "" {
		return nil, ErrSubscriptionAccountNotFound
	}
	var routed []*ModelRouting
	if uc.routingRepo != nil {
		rows, err := uc.routingRepo.ListModelRoutingsForSelect(ctx, group, model, platform)
		if err != nil {
			return nil, fmt.Errorf("load model routing policy: %w", err)
		}
		routed = RoutingMatchForSelect(rows, model)
	}
	abilities, err := uc.repo.ListSubscriptionAccountAbilities(ctx, group, model, platform)
	if err != nil {
		return nil, err
	}
	before := len(abilities)
	abilities = filterAbilitiesByRouted(abilities, routed)
	if len(abilities) == 0 {
		if len(routed) > 0 && before > 0 {
			return nil, fmt.Errorf("model routing matched %d account(s) for %q but none are schedulable: %w", len(routed), model, ErrSubscriptionAccountNotFound)
		}
		return nil, ErrSubscriptionAccountNotFound
	}
	return abilities, nil
}

// CanRoute resolves current model-scoped authorization without selecting a
// random account. Health, quotas and concurrency remain scheduling concerns.
// In particular, registry subscription mappings can grant one model outside
// the account CSV; checking only that CSV would incorrectly revoke the grant.
func (uc *ChannelUsecase) CanRoute(ctx context.Context, group, model string, source routing.Source) (routing.Permission, error) {
	deny := routing.Permission{}
	if uc == nil || uc.repo == nil || source.ID <= 0 || group == "" || strings.TrimSpace(model) == "" {
		return deny, nil
	}
	switch source.Kind {
	case routing.Channel:
		abilities, err := uc.routeChannelAbilities(ctx, group, model)
		if errors.Is(err, ErrChannelNotFound) {
			return deny, nil
		}
		if err != nil {
			return deny, err
		}
		for _, ability := range abilities {
			if ability.ChannelID == source.ID && ability.Enabled {
				channel, err := uc.repo.FindByID(ctx, source.ID)
				if errors.Is(err, ErrChannelNotFound) {
					return deny, nil
				}
				if err != nil {
					return deny, err
				}
				return routing.Permission{Allowed: channel != nil && channel.Status == ChannelStatusEnabled, UpstreamModelID: ability.UpstreamModelID}, nil
			}
		}
		if len(abilities) == 0 {
			channels, err := uc.repo.ListUnrestrictedChannelsByGroup(ctx, group)
			if err != nil {
				return deny, err
			}
			for _, channel := range channels {
				if channel.ID == source.ID && channel.Status == ChannelStatusEnabled {
					return routing.Permission{Allowed: true}, nil
				}
			}
		}
	case routing.Subscription:
		account, err := uc.repo.FindSubscriptionAccountByID(ctx, source.ID)
		if errors.Is(err, ErrSubscriptionAccountNotFound) {
			return deny, nil
		}
		if err != nil {
			return deny, err
		}
		if account == nil || account.Status != ChannelStatusEnabled {
			return deny, nil
		}
		abilities, err := uc.routeSubscriptionAbilities(ctx, group, model, account.Platform)
		if errors.Is(err, ErrSubscriptionAccountNotFound) {
			return deny, nil
		}
		if err != nil {
			return deny, err
		}
		for _, ability := range abilities {
			if ability.AccountID == source.ID && ability.Enabled {
				return routing.Permission{Allowed: true, UpstreamModelID: ability.UpstreamModelID}, nil
			}
		}
	}
	return deny, nil
}

// hasAuthorizedModel filters the catalogue by the same model-scoped grants
// used for selection. Public/discovery filtering remains owned by the repo.
// Temporary quota/health changes do not remove a model from the catalogue.
func (uc *ChannelUsecase) hasAuthorizedModel(ctx context.Context, group, model string) (bool, error) {
	channels, err := uc.routeChannelAbilities(ctx, group, model)
	if err != nil && !errors.Is(err, ErrChannelNotFound) {
		return false, err
	}
	for _, ability := range channels {
		permission, err := uc.CanRoute(ctx, group, model, routing.Source{Kind: routing.Channel, ID: ability.ChannelID})
		if err != nil {
			return false, err
		}
		if permission.Allowed {
			return true, nil
		}
	}
	accounts, err := uc.repo.ListSubscriptionAccountAbilities(ctx, group, model, "")
	if errors.Is(err, ErrSubscriptionAccountNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, ability := range accounts {
		permission, err := uc.CanRoute(ctx, group, model, routing.Source{Kind: routing.Subscription, ID: ability.AccountID})
		if err != nil {
			return false, err
		}
		if permission.Allowed {
			return true, nil
		}
	}
	return false, nil
}
