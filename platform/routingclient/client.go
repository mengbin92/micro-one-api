// Package routingclient is the internal channel RPC adapter for group facts.
package routingclient

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
	grpcauth "micro-one-api/platform/grpc"
)

type Client struct {
	client channelv1.ChannelServiceClient
}

func New(client channelv1.ChannelServiceClient) *Client { return &Client{client: client} }

func Dial(endpoint string) (*Client, func(), error) {
	if endpoint == "" {
		return nil, nil, fmt.Errorf("CHANNEL_GRPC_ENDPOINT is required for routing v2")
	}
	conn, err := grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(os.Getenv("SERVICE_TOKEN"))))
	if err != nil {
		return nil, nil, err
	}
	return New(channelv1.NewChannelServiceClient(conn)), func() { _ = conn.Close() }, nil
}

func groupFromProto(g *channelv1.RoutingGroup) *routing.Group {
	if g == nil {
		return nil
	}
	return &routing.Group{ID: g.Id, Key: g.Key, Status: g.Status, AccessMode: g.AccessMode, ModelAccessMode: g.ModelAccessMode, Revision: g.Revision}
}

func (c *Client) GetRoutingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	r, err := c.client.GetRoutingGroup(ctx, &channelv1.GetRoutingGroupRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return groupFromProto(r.GetGroup()), nil
}

func (c *Client) FindRoutingGroup(ctx context.Context, key string) (*routing.Group, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	quoted, _ := jsonx.Marshal(key)
	r, err := c.client.ListRoutingGroups(ctx, &channelv1.ListRoutingGroupsRequest{Filter: "key = " + string(quoted), PageSize: 2})
	if err != nil {
		return nil, err
	}
	if len(r.Groups) != 1 || r.Groups[0].Key != key {
		return nil, fmt.Errorf("routing group not found")
	}
	return groupFromProto(r.Groups[0]), nil
}

func (c *Client) ListRoutingGroups(ctx context.Context) ([]*routing.Group, error) {
	var groups []*routing.Group
	token := ""
	for {
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		r, err := c.client.ListRoutingGroups(callCtx, &channelv1.ListRoutingGroupsRequest{PageSize: 200, PageToken: token})
		cancel()
		if err != nil {
			return nil, err
		}
		for _, g := range r.Groups {
			groups = append(groups, groupFromProto(g))
		}
		if r.NextPageToken == "" {
			return groups, nil
		}
		if token == r.NextPageToken {
			return nil, fmt.Errorf("repeated routing group page")
		}
		token = r.NextPageToken
	}
}
