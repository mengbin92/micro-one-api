package service

import (
	"context"
	"strconv"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
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
		reply.Resources = append(reply.Resources, &channelv1.RoutingGroupResource{SourceKind: r.Source.Kind, SourceId: r.Source.ID, Priority: r.Priority, Weight: r.Weight})
	}
	for _, m := range detail.ModelGrants {
		reply.ModelGrants = append(reply.ModelGrants, &channelv1.RoutingGroupModelGrant{MappingId: m.MappingID, AccountId: m.AccountID, Model: m.Model, UpstreamModelId: m.UpstreamModelID, Enabled: m.Enabled, Priority: m.Priority, ExtraAuthorization: m.ExtraAuthorization})
	}
	return reply, nil
}
