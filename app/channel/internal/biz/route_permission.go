package biz

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"micro-one-api/domain/routing"
)

// routeChannelCandidates is the authorization seam shared by selection,
// exclusions, catalogue verification and exact-source authorization. A missing
// registry mapping surfaces as ErrChannelNotFound and is never widened.
func (uc *ChannelUsecase) routeChannelCandidates(ctx context.Context, group, model string) ([]RoutingCandidate, error) {
	if group == "" || model == "" {
		return nil, ErrChannelNotFound
	}
	return uc.repo.ListChannelCandidates(ctx, group, model)
}

func (uc *ChannelUsecase) routeSubscriptionCandidates(ctx context.Context, group, model, platform string) ([]SubscriptionRoutingCandidate, error) {
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
	candidates, err := uc.repo.ListSubscriptionCandidates(ctx, group, model, platform)
	if err != nil {
		return nil, err
	}
	before := len(candidates)
	candidates = filterCandidatesByRouted(candidates, routed)
	if len(candidates) == 0 {
		if len(routed) > 0 && before > 0 {
			return nil, fmt.Errorf("model routing matched %d account(s) for %q but none are schedulable: %w", len(routed), model, ErrSubscriptionAccountNotFound)
		}
		return nil, ErrSubscriptionAccountNotFound
	}
	return candidates, nil
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
		candidates, err := uc.routeChannelCandidates(ctx, group, model)
		if errors.Is(err, ErrChannelNotFound) {
			return deny, nil
		}
		if err != nil {
			return deny, err
		}
		for _, candidate := range candidates {
			if candidate.ChannelID == source.ID && candidate.Enabled {
				channel, err := uc.repo.FindByID(ctx, source.ID)
				if errors.Is(err, ErrChannelNotFound) {
					return deny, nil
				}
				if err != nil {
					return deny, err
				}
				return routing.Permission{Allowed: channel != nil && channel.Status == ChannelStatusEnabled, UpstreamModelID: candidate.UpstreamModelID}, nil
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
		candidates, err := uc.routeSubscriptionCandidates(ctx, group, model, account.Platform)
		if errors.Is(err, ErrSubscriptionAccountNotFound) {
			return deny, nil
		}
		if err != nil {
			return deny, err
		}
		for _, candidate := range candidates {
			if candidate.AccountID == source.ID && candidate.Enabled {
				return routing.Permission{Allowed: true, UpstreamModelID: candidate.UpstreamModelID}, nil
			}
		}
	}
	return deny, nil
}

// hasAuthorizedModel filters the catalogue by the same model-scoped grants
// used for selection. Public/discovery filtering remains owned by the repo.
// Temporary quota/health changes do not remove a model from the catalogue.
func (uc *ChannelUsecase) hasAuthorizedModel(ctx context.Context, group, model string) (bool, error) {
	candidates, err := uc.routeChannelCandidates(ctx, group, model)
	if err != nil && !errors.Is(err, ErrChannelNotFound) {
		return false, err
	}
	for _, candidate := range candidates {
		permission, err := uc.CanRoute(ctx, group, model, routing.Source{Kind: routing.Channel, ID: candidate.ChannelID})
		if err != nil {
			return false, err
		}
		if permission.Allowed {
			return true, nil
		}
	}
	accounts, err := uc.repo.ListSubscriptionCandidates(ctx, group, model, "")
	if errors.Is(err, ErrSubscriptionAccountNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, candidate := range accounts {
		permission, err := uc.CanRoute(ctx, group, model, routing.Source{Kind: routing.Subscription, ID: candidate.AccountID})
		if err != nil {
			return false, err
		}
		if permission.Allowed {
			return true, nil
		}
	}
	return false, nil
}

// HasRoutingCandidates is the read-only probe behind ordered (auto) routing:
// true when the enabled group currently has any schedulable upstream for the
// model, using the same ability + schedulability filters as the serving path.
// It never selects, so weighted-scheduler state is never advanced.
func (uc *ChannelUsecase) HasRoutingCandidates(ctx context.Context, group *routing.Group, model string) (bool, error) {
	if uc == nil || uc.repo == nil || group == nil || group.Status != "enabled" || strings.TrimSpace(model) == "" {
		return false, nil
	}
	now := uc.now()
	candidates, err := uc.routeChannelCandidates(ctx, group.Key, model)
	if err != nil && !errors.Is(err, ErrChannelNotFound) {
		return false, err
	}
	for _, candidate := range candidates {
		if !candidate.Enabled {
			continue
		}
		channel, err := uc.repo.FindByID(ctx, candidate.ChannelID)
		if err != nil {
			if errors.Is(err, ErrChannelNotFound) {
				continue
			}
			return false, err
		}
		if channel.SelectableAt(now) && !uc.IsUsageSemanticBlocked(ctx, UsageSemanticSourceKindChannel, channel.ID, candidate.UpstreamModelID) {
			return true, nil
		}
	}
	accounts, err := uc.routeSubscriptionCandidates(ctx, group.Key, model, "")
	if err != nil && !errors.Is(err, ErrSubscriptionAccountNotFound) {
		return false, err
	}
	for _, candidate := range accounts {
		if !candidate.Enabled {
			continue
		}
		if uc.IsUsageSemanticBlocked(ctx, UsageSemanticSourceKindSubscription, candidate.AccountID, candidate.UpstreamModelID) {
			continue
		}
		account, err := uc.repo.FindSubscriptionAccountByID(ctx, candidate.AccountID)
		if err != nil {
			if errors.Is(err, ErrSubscriptionAccountNotFound) {
				continue
			}
			return false, err
		}
		if account.Status == ChannelStatusEnabled && account.IsSchedulableAt(now) {
			return true, nil
		}
	}
	return false, nil
}
