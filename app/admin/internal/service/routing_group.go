package service

import (
	"context"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/filtering"
	"micro-one-api/pkg/ordering"
)

type routingGroupUsecase interface {
	List(context.Context, routing.GroupListRequest) (*routing.GroupListResult, error)
	Get(context.Context, int64) (*routing.GroupDetail, error)
}

func (s *AdminService) SetRoutingGroupUsecase(uc routingGroupUsecase) { s.routingGroupUc = uc }

// These JSON DTOs belong to the admin transport; remote PO/DTOs never reach biz.
type RoutingGroup struct {
	ID              int64  `json:"id"`
	Key             string `json:"key"`
	DisplayName     string `json:"display_name"`
	Description     string `json:"description"`
	Status          string `json:"status"`
	AccessMode      string `json:"access_mode"`
	ModelAccessMode string `json:"model_access_mode"`
	SortOrder       int32  `json:"sort_order"`
	Revision        int64  `json:"revision"`
}
type RoutingGroupList struct {
	Groups        []RoutingGroup `json:"groups"`
	NextPageToken string         `json:"next_page_token"`
}
type RoutingGroupResource struct {
	SourceKind       string `json:"source_kind"`
	SourceID         int64  `json:"source_id"`
	Priority         int64  `json:"priority"`
	Weight           int64  `json:"weight"`
	PriorityOverride *int64 `json:"priority_override,omitempty"`
	WeightOverride   *int64 `json:"weight_override,omitempty"`
}
type RoutingGroupModelGrant struct {
	MappingID          int64  `json:"mapping_id"`
	AccountID          int64  `json:"account_id"`
	Model              string `json:"model"`
	UpstreamModelID    string `json:"upstream_model_id"`
	Enabled            bool   `json:"enabled"`
	Priority           int32  `json:"priority"`
	ExtraAuthorization bool   `json:"extra_authorization"`
}
type RoutingGroupDetail struct {
	Group       RoutingGroup             `json:"group"`
	Resources   []RoutingGroupResource   `json:"resources"`
	ModelGrants []RoutingGroupModelGrant `json:"model_grants"`
}

func (s *AdminService) ListRoutingGroups(ctx context.Context, size int32, token, filter, order string) (*RoutingGroupList, error) {
	if s.routingGroupUc == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	if size < 0 || size > 200 || len(token) > 512 {
		return nil, biz.ErrRoutingGroupInvalid
	}
	if _, err := filtering.Equalities(filter, "key", "status", "access_mode"); err != nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	if _, err := ordering.Parse(order, "id", "key", "sort_order"); err != nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	result, err := s.routingGroupUc.List(ctx, routing.GroupListRequest{PageSize: size, PageToken: token, Filter: filter, OrderBy: order})
	if err != nil {
		return nil, err
	}
	reply := &RoutingGroupList{Groups: []RoutingGroup{}, NextPageToken: result.NextPageToken}
	for _, g := range result.Groups {
		reply.Groups = append(reply.Groups, RoutingGroup{ID: g.ID, Key: g.Key, DisplayName: g.DisplayName, Description: g.Description, Status: g.Status, AccessMode: g.AccessMode, ModelAccessMode: g.ModelAccessMode, SortOrder: g.SortOrder, Revision: g.Revision})
	}
	return reply, nil
}
func (s *AdminService) GetRoutingGroup(ctx context.Context, id int64) (*RoutingGroupDetail, error) {
	if s.routingGroupUc == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	if id <= 0 {
		return nil, biz.ErrRoutingGroupInvalid
	}
	result, err := s.routingGroupUc.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	g := result.Group
	reply := &RoutingGroupDetail{Group: RoutingGroup{ID: g.ID, Key: g.Key, DisplayName: g.DisplayName, Description: g.Description, Status: g.Status, AccessMode: g.AccessMode, ModelAccessMode: g.ModelAccessMode, SortOrder: g.SortOrder, Revision: g.Revision}, Resources: []RoutingGroupResource{}, ModelGrants: []RoutingGroupModelGrant{}}
	for _, r := range result.Resources {
		reply.Resources = append(reply.Resources, RoutingGroupResource{SourceKind: r.Source.Kind, SourceID: r.Source.ID, Priority: r.Priority, Weight: r.Weight, PriorityOverride: r.PriorityOverride, WeightOverride: r.WeightOverride})
	}
	for _, m := range result.ModelGrants {
		reply.ModelGrants = append(reply.ModelGrants, RoutingGroupModelGrant{MappingID: m.MappingID, AccountID: m.AccountID, Model: m.Model, UpstreamModelID: m.UpstreamModelID, Enabled: m.Enabled, Priority: m.Priority, ExtraAuthorization: m.ExtraAuthorization})
	}
	return reply, nil
}
