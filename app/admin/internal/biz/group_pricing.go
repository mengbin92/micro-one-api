package biz

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
)

// RoutingGroupRatio is a pricing override keyed by routing group. It is not
// a routing-group registry: removing an override does not revoke membership.
type RoutingGroupRatio struct {
	Group string
	Ratio float64
}

const groupRatioOption = "GroupRatio"

func (uc *SystemOptionsUsecase) ListRoutingGroupRatios(ctx context.Context) ([]RoutingGroupRatio, error) {
	ratios, err := uc.routingGroupRatios(ctx)
	if err != nil {
		return nil, err
	}
	ensureDefaultGroupRatio(ratios)
	keys := make([]string, 0, len(ratios))
	for group := range ratios {
		keys = append(keys, group)
	}
	sort.Strings(keys)
	result := make([]RoutingGroupRatio, 0, len(keys))
	for _, group := range keys {
		result = append(result, RoutingGroupRatio{Group: group, Ratio: ratios[group]})
	}
	return result, nil
}

func (uc *SystemOptionsUsecase) UpsertRoutingGroupRatio(ctx context.Context, group string, ratio float64) (*RoutingGroupRatio, error) {
	group = strings.TrimSpace(group)
	if group == "" || strings.Contains(group, ",") {
		return nil, fmt.Errorf("one routing group is required")
	}
	if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return nil, fmt.Errorf("ratio must be finite and greater than 0")
	}
	ratios, err := uc.routingGroupRatios(ctx)
	if err != nil {
		return nil, err
	}
	ratios[group] = ratio
	if err := uc.saveRoutingGroupRatios(ctx, ratios); err != nil {
		return nil, err
	}
	return &RoutingGroupRatio{Group: group, Ratio: ratio}, nil
}

func (uc *SystemOptionsUsecase) DeleteRoutingGroupRatio(ctx context.Context, group string) (*RoutingGroupRatio, error) {
	group = strings.TrimSpace(group)
	if group == "" {
		return nil, fmt.Errorf("routing group is required")
	}
	if group == routing.DefaultGroup {
		return nil, fmt.Errorf("default group cannot be deleted")
	}
	ratios, err := uc.routingGroupRatios(ctx)
	if err != nil {
		return nil, err
	}
	ratio := ratios[group]
	delete(ratios, group)
	ensureDefaultGroupRatio(ratios)
	if err := uc.saveRoutingGroupRatios(ctx, ratios); err != nil {
		return nil, err
	}
	return &RoutingGroupRatio{Group: group, Ratio: ratio}, nil
}

func ensureDefaultGroupRatio(ratios map[string]float64) {
	if _, ok := ratios[routing.DefaultGroup]; !ok {
		ratios[routing.DefaultGroup] = 1
	}
}

func (uc *SystemOptionsUsecase) routingGroupRatios(ctx context.Context) (map[string]float64, error) {
	raw, err := uc.Get(ctx, groupRatioOption)
	if err != nil {
		return nil, err
	}
	ratios := map[string]float64{}
	if strings.TrimSpace(raw) != "" {
		if err := jsonx.Unmarshal([]byte(raw), &ratios); err != nil {
			return nil, fmt.Errorf("invalid GroupRatio option: %w", err)
		}
	}
	if ratios == nil {
		ratios = map[string]float64{}
	}
	return ratios, nil
}

func (uc *SystemOptionsUsecase) saveRoutingGroupRatios(ctx context.Context, ratios map[string]float64) error {
	if uc == nil || uc.repo == nil {
		return fmt.Errorf("system options storage not configured")
	}
	payload, err := jsonx.Marshal(ratios)
	if err != nil {
		return err
	}
	return uc.Set(ctx, groupRatioOption, string(payload))
}
