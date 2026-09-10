package data

import (
	"context"

	"google.golang.org/grpc"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/routing"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/routingclient"
)

func checkRoute(ctx context.Context, client channelv1.ChannelServiceClient, group, model string, source routing.Source) (routing.Permission, error) {
	var groupID int64
	if relaybiz.RoutingContextV2Enabled() {
		g, err := routingclient.New(client).FindRoutingGroup(ctx, group)
		if err != nil {
			return routing.Permission{}, err
		}
		groupID = g.ID
	}
	reply, err := client.CheckRoute(ctx, &channelv1.CheckRouteRequest{Group: group, Model: model, SourceKind: source.Kind, SourceId: source.ID, RoutingGroupId: groupID})
	if err != nil {
		return routing.Permission{}, err
	}
	return routing.Permission{Allowed: reply.GetAllowed(), UpstreamModelID: reply.GetUpstreamModelId()}, nil
}

func (a *ChannelAdapter) CanRoute(ctx context.Context, group, model string, source routing.Source) (routing.Permission, error) {
	return checkRoute(ctx, a.client, group, model, source)
}

func (c *channelClient) CanRoute(ctx context.Context, group, model string, source routing.Source) (routing.Permission, error) {
	return checkRoute(ctx, c.client, group, model, source)
}

func (c *resilientChannelClient) CheckRoute(ctx context.Context, req *channelv1.CheckRouteRequest, opts ...grpc.CallOption) (*channelv1.CheckRouteReply, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client channelv1.ChannelServiceClient) (any, error) {
		return client.CheckRoute(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*channelv1.CheckRouteReply), nil
}
