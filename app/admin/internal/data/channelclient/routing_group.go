package channelclient

import (
	"context"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/routing"
)

type routingGroupReader struct {
	client channelv1.ChannelServiceClient
}

func NewRoutingGroupReader(client channelv1.ChannelServiceClient) biz.RoutingGroupReader {
	return &routingGroupReader{client: client}
}
func groupFromRPC(g *channelv1.RoutingGroup) *routing.Group {
	return &routing.Group{ID: g.Id, Key: g.Key, DisplayName: g.DisplayName, Description: g.Description, Status: g.Status, AccessMode: g.AccessMode, ModelAccessMode: g.ModelAccessMode, SortOrder: g.SortOrder, Revision: g.Revision, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}
func (r *routingGroupReader) List(ctx context.Context, q routing.GroupListRequest) (*routing.GroupListResult, error) {
	if r.client == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	reply, err := r.client.ListRoutingGroups(ctx, &channelv1.ListRoutingGroupsRequest{PageSize: q.PageSize, PageToken: q.PageToken, Filter: q.Filter, OrderBy: q.OrderBy})
	if err != nil {
		return nil, err
	}
	if reply == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	result := &routing.GroupListResult{Groups: []*routing.Group{}, NextPageToken: reply.NextPageToken}
	for _, g := range reply.Groups {
		if g == nil {
			return nil, biz.ErrRoutingGroupUnavailable
		}
		result.Groups = append(result.Groups, groupFromRPC(g))
	}
	return result, nil
}
func (r *routingGroupReader) Get(ctx context.Context, id int64) (*routing.GroupDetail, error) {
	if r.client == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	reply, err := r.client.GetRoutingGroup(ctx, &channelv1.GetRoutingGroupRequest{Id: id})
	if err != nil {
		return nil, err
	}
	if reply == nil || reply.Group == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	result := &routing.GroupDetail{Group: groupFromRPC(reply.Group), Resources: []routing.GroupResource{}, ModelGrants: []routing.GroupModelGrant{}}
	for _, v := range reply.Resources {
		if v == nil {
			return nil, biz.ErrRoutingGroupUnavailable
		}
		result.Resources = append(result.Resources, routing.GroupResource{Source: routing.Source{Kind: v.SourceKind, ID: v.SourceId}, Priority: v.Priority, Weight: v.Weight})
	}
	for _, v := range reply.ModelGrants {
		if v == nil {
			return nil, biz.ErrRoutingGroupUnavailable
		}
		result.ModelGrants = append(result.ModelGrants, routing.GroupModelGrant{MappingID: v.MappingId, AccountID: v.AccountId, Model: v.Model, UpstreamModelID: v.UpstreamModelId, Enabled: v.Enabled, Priority: v.Priority, ExtraAuthorization: v.ExtraAuthorization})
	}
	return result, nil
}
