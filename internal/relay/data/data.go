package data

import (
	"context"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/internal/relay/biz"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Data aggregates downstream clients and provider adaptors for relay-gateway.
type Data struct {
	Identity biz.IdentityClient
	Channel  biz.ChannelClient
}

func NewData(identityEndpoint, channelEndpoint string) (*Data, error) {
	identityConn, err := grpc.NewClient(identityEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	channelConn, err := grpc.NewClient(channelEndpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = identityConn.Close()
		return nil, err
	}
	return &Data{
		Identity: &identityClient{
			client: identityv1.NewIdentityServiceClient(identityConn),
		},
		Channel: &channelClient{
			client: channelv1.NewChannelServiceClient(channelConn),
		},
	}, nil
}

type identityClient struct {
	client identityv1.IdentityServiceClient
}

func (c *identityClient) GetAuthSnapshot(ctx context.Context, token string) (*biz.AuthSnapshot, error) {
	resp, err := c.client.GetAuthSnapshot(ctx, &identityv1.GetAuthSnapshotRequest{
		Token: token,
	})
	if err != nil {
		return nil, err
	}
	return &biz.AuthSnapshot{
		UserID:        resp.UserId,
		TokenID:       resp.TokenId,
		TokenName:     resp.TokenName,
		Group:         resp.Group,
		AllowedModels: append([]string(nil), resp.AllowedModels...),
		UserEnabled:   resp.UserEnabled,
		TokenEnabled:  resp.TokenEnabled,
	}, nil
}

type channelClient struct {
	client channelv1.ChannelServiceClient
}

func (c *channelClient) SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*biz.Channel, error) {
	resp, err := c.client.SelectChannel(ctx, &channelv1.SelectChannelRequest{
		Group:                group,
		Model:                model,
		ExcludeFirstPriority: excludeFirstPriority,
	})
	if err != nil {
		return nil, err
	}
	info := resp.Channel
	return &biz.Channel{
		ID:       info.Id,
		Type:     info.Type,
		Name:     info.Name,
		Status:   info.Status,
		BaseURL:  info.BaseUrl,
		Group:    info.Group,
		Models:   splitCSV(info.Models),
		Priority: info.Priority,
		Key:      info.Key,
	}, nil
}

func splitCSV(input string) []string {
	raw := strings.Split(input, ",")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}
