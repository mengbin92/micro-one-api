package data

import (
	"context"
	"errors"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relaybiz "micro-one-api/internal/relay/biz"
)

// IdentityAdapter wraps a gRPC IdentityServiceClient to implement biz.IdentityClient.
type IdentityAdapter struct {
	client identityv1.IdentityServiceClient
}

// NewIdentityAdapter creates a new IdentityAdapter.
func NewIdentityAdapter(client identityv1.IdentityServiceClient) *IdentityAdapter {
	return &IdentityAdapter{client: client}
}

func (a *IdentityAdapter) GetAuthSnapshot(ctx context.Context, token string) (*relaybiz.AuthSnapshot, error) {
	reply, err := a.client.GetAuthSnapshot(ctx, &identityv1.GetAuthSnapshotRequest{Token: token})
	if err != nil {
		return nil, err
	}
	return &relaybiz.AuthSnapshot{
		UserID:        reply.UserId,
		TokenID:       reply.TokenId,
		TokenName:     reply.TokenName,
		Group:         reply.Group,
		AllowedModels: reply.AllowedModels,
		UserEnabled:   reply.UserEnabled,
		TokenEnabled:  reply.TokenEnabled,
	}, nil
}

// splitModels splits a comma-separated model string into a slice.
func splitModels(models string) []string {
	if models == "" {
		return nil
	}
	parts := strings.Split(models, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ChannelAdapter wraps a gRPC ChannelServiceClient to implement biz.ChannelClient.
type ChannelAdapter struct {
	client channelv1.ChannelServiceClient
}

// NewChannelAdapter creates a new ChannelAdapter.
func NewChannelAdapter(client channelv1.ChannelServiceClient) *ChannelAdapter {
	return &ChannelAdapter{client: client}
}

func (a *ChannelAdapter) SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*relaybiz.Channel, error) {
	reply, err := a.client.SelectChannel(ctx, &channelv1.SelectChannelRequest{
		Group:                group,
		Model:                model,
		ExcludeFirstPriority: excludeFirstPriority,
	})
	if err != nil {
		return nil, err
	}
	ch := reply.Channel
	if ch == nil {
		return nil, nil
	}
	relayChannel := &relaybiz.Channel{
		ID:       ch.Id,
		Type:     ch.Type,
		Name:     ch.Name,
		Status:   ch.Status,
		BaseURL:  ch.BaseUrl,
		Group:    ch.Group,
		Models:   splitModels(ch.Models),
		Priority: ch.Priority,
		Key:      ch.Key,
	}
	if ch.Config != nil {
		relayChannel.Config.APIVersion = ch.Config.ApiVersion
	}
	return relayChannel, nil
}

func (a *ChannelAdapter) RecordChannelHealth(ctx context.Context, channelID int64, success bool, message string, responseTime int64) error {
	reply, err := a.client.RecordChannelHealth(ctx, &channelv1.RecordChannelHealthRequest{
		ChannelId:    channelID,
		Success:      success,
		Error:        message,
		ResponseTime: responseTime,
	})
	if err != nil {
		return err
	}
	if reply != nil && !reply.GetSuccess() {
		return errors.New(reply.GetMessage())
	}
	return nil
}
