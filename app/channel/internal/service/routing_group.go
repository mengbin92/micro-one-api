package service

import (
	"context"
	"strconv"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/filtering"
	"micro-one-api/pkg/ordering"
	"micro-one-api/pkg/pagination"
)

type routingGroupUsecase interface {
	List(context.Context, biz.RoutingGroupListOptions) ([]*biz.RoutingGroup, error)
	Get(context.Context, int64) (*biz.RoutingGroupDetail, error)
}

func (s *ChannelService) SetRoutingGroupUsecase(uc routingGroupUsecase) { s.routingGroupUC = uc }

func (s *ChannelService) validateRoutingGroupPair(ctx context.Context, id int64, key string) error {
	if id == 0 {
		return nil
	}
	if id < 0 || s.routingGroupUC == nil {
		return biz.ErrRoutingGroupInvalid
	}
	detail, err := s.routingGroupUC.Get(ctx, id)
	if err != nil {
		return err
	}
	if detail == nil || detail.Group == nil || detail.Group.Key != key || detail.Group.Status != "enabled" {
		return biz.ErrRoutingGroupInvalid
	}
	return nil
}
func (s *ChannelService) ListRoutingGroups(ctx context.Context, req *channelv1.ListRoutingGroupsRequest) (*channelv1.ListRoutingGroupsReply, error) {
	if s.routingGroupUC == nil {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	if req == nil || req.PageSize < 0 || req.PageSize > 200 {
		return nil, biz.ErrRoutingGroupInvalid
	}
	limit := int(req.PageSize)
	if limit == 0 {
		limit = 50
	}
	filter, err := filtering.Equalities(req.Filter, "key", "status", "access_mode")
	if err != nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	order, err := ordering.Parse(req.OrderBy, "id", "key", "sort_order")
	if err != nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	query := req.Filter + "\x00" + req.OrderBy + "\x00" + strconv.Itoa(limit)
	offset, err := pagination.Offset(req.PageToken, query)
	if err != nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	groups, err := s.routingGroupUC.List(ctx, biz.RoutingGroupListOptions{Filter: filter, OrderBy: order, Offset: offset, Limit: limit + 1})
	if err != nil {
		return nil, err
	}
	next := ""
	if len(groups) > limit {
		groups = groups[:limit]
		next = pagination.Token(offset+limit, query)
	}
	reply := &channelv1.ListRoutingGroupsReply{Groups: []*channelv1.RoutingGroup{}, NextPageToken: next}
	for _, g := range groups {
		reply.Groups = append(reply.Groups, &channelv1.RoutingGroup{Id: g.ID, Key: g.Key, DisplayName: g.DisplayName, Description: g.Description, Status: g.Status, AccessMode: g.AccessMode, ModelAccessMode: g.ModelAccessMode, SortOrder: g.SortOrder, Revision: g.Revision, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt})
	}
	return reply, nil
}
func (s *ChannelService) GetRoutingGroup(ctx context.Context, req *channelv1.GetRoutingGroupRequest) (*channelv1.RoutingGroupDetail, error) {
	if s.routingGroupUC == nil {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	if req == nil || req.Id <= 0 {
		return nil, biz.ErrRoutingGroupInvalid
	}
	detail, err := s.routingGroupUC.Get(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	g := detail.Group
	reply := &channelv1.RoutingGroupDetail{Group: &channelv1.RoutingGroup{Id: g.ID, Key: g.Key, DisplayName: g.DisplayName, Description: g.Description, Status: g.Status, AccessMode: g.AccessMode, ModelAccessMode: g.ModelAccessMode, SortOrder: g.SortOrder, Revision: g.Revision, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}, Resources: []*channelv1.RoutingGroupResource{}, ModelGrants: []*channelv1.RoutingGroupModelGrant{}}
	for _, r := range detail.Resources {
		reply.Resources = append(reply.Resources, &channelv1.RoutingGroupResource{SourceKind: r.Source.Kind, SourceId: r.Source.ID, Priority: r.Priority, Weight: r.Weight, PriorityOverride: r.PriorityOverride, WeightOverride: r.WeightOverride})
	}
	for _, m := range detail.ModelGrants {
		reply.ModelGrants = append(reply.ModelGrants, &channelv1.RoutingGroupModelGrant{MappingId: m.MappingID, AccountId: m.AccountID, Model: m.Model, UpstreamModelId: m.UpstreamModelID, Enabled: m.Enabled, Priority: m.Priority, ExtraAuthorization: m.ExtraAuthorization})
	}
	return reply, nil
}

func (s *ChannelService) SetRoutingGroupState(ctx context.Context, req *channelv1.SetRoutingGroupStateRequest) (*channelv1.RoutingGroupDetail, error) {
	uc, ok := s.routingGroupUC.(interface {
		SetState(context.Context, int64, int64, string, string) (*biz.RoutingGroupDetail, error)
	})
	if !ok {
		return nil, biz.ErrRoutingGroupStorage
	}
	if _, err := uc.SetState(ctx, req.Id, req.ExpectedRevision, req.Status, req.AccessMode); err != nil {
		return nil, err
	}
	return s.GetRoutingGroup(ctx, &channelv1.GetRoutingGroupRequest{Id: req.Id})
}

func (s *ChannelService) SetRoutingGroupResourceOverrides(ctx context.Context, req *channelv1.SetRoutingGroupResourceOverridesRequest) (*channelv1.SetRoutingGroupResourceOverridesReply, error) {
	uc, ok := s.routingGroupUC.(interface {
		SetResourceOverrides(context.Context, int64, routing.Source, *int64, *int64) (*biz.RoutingGroupDetail, error)
	})
	if !ok {
		return nil, biz.ErrRoutingGroupStorage
	}
	if req == nil || req.RoutingGroupId <= 0 || req.SourceId <= 0 || (req.SourceKind != "channel" && req.SourceKind != "subscription") {
		return nil, biz.ErrRoutingGroupInvalid
	}
	if _, err := uc.SetResourceOverrides(ctx, req.RoutingGroupId, routing.Source{Kind: req.SourceKind, ID: req.SourceId}, req.PriorityOverride, req.WeightOverride); err != nil {
		return nil, err
	}
	return &channelv1.SetRoutingGroupResourceOverridesReply{}, nil
}

// HasRoutingCandidates is the ordered-routing probe. It is read-only and never
// advances weighted-scheduler state.
func (s *ChannelService) HasRoutingCandidates(ctx context.Context, req *channelv1.HasRoutingCandidatesRequest) (*channelv1.HasRoutingCandidatesReply, error) {
	if s.routingGroupUC == nil || s.uc == nil {
		return nil, biz.ErrRoutingGroupMigrationRequired
	}
	if req == nil || req.RoutingGroupId <= 0 || req.Model == "" {
		return nil, biz.ErrRoutingGroupInvalid
	}
	detail, err := s.routingGroupUC.Get(ctx, req.RoutingGroupId)
	if err != nil {
		return nil, err
	}
	if detail == nil || detail.Group == nil {
		return nil, biz.ErrRoutingGroupNotFound
	}
	has, err := s.uc.HasRoutingCandidates(ctx, detail.Group, req.Model)
	if err != nil {
		return nil, err
	}
	return &channelv1.HasRoutingCandidatesReply{HasCandidates: has}, nil
}
