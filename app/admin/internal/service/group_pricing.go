package service

import "context"

// ListGroups preserves the legacy /api/group contract. The records are pricing
// overrides; routing membership is owned by identity and channel resources.
func (s *AdminService) ListGroups(ctx context.Context) ([]GroupConfig, error) {
	ratios, err := s.systemOptsUc.ListRoutingGroupRatios(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]GroupConfig, 0, len(ratios))
	for _, ratio := range ratios {
		result = append(result, GroupConfig{Group: ratio.Group, Ratio: ratio.Ratio})
	}
	return result, nil
}

func (s *AdminService) UpsertGroup(ctx context.Context, group string, ratio float64) (*GroupConfig, error) {
	result, err := s.systemOptsUc.UpsertRoutingGroupRatio(ctx, group, ratio)
	if err != nil {
		return nil, err
	}
	return &GroupConfig{Group: result.Group, Ratio: result.Ratio}, nil
}

// DeleteGroup deletes only the pricing override. Existing users, channels,
// accounts, mappings and subscriptions retain their respective membership.
func (s *AdminService) DeleteGroup(ctx context.Context, group string) (*GroupConfig, error) {
	result, err := s.systemOptsUc.DeleteRoutingGroupRatio(ctx, group)
	if err != nil {
		return nil, err
	}
	return &GroupConfig{Group: result.Group, Ratio: result.Ratio}, nil
}
