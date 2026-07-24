package data

import (
	"context"
	"errors"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relaycredential "micro-one-api/domain/upstream/credential"
	relaybiz "micro-one-api/internal/biz"
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

func (a *ChannelAdapter) Resolve(ctx context.Context, channelID int64) (*relaycredential.SubscriptionAccountMetadata, error) {
	return NewChannelSubscriptionAccountStore(a.client).Resolve(ctx, channelID)
}

func (a *ChannelAdapter) SelectSubscriptionAccount(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*relaybiz.SubscriptionAccount, error) {
	reply, err := a.client.SelectSubscriptionAccount(ctx, &channelv1.SelectSubscriptionAccountRequest{
		Group:                group,
		Model:                model,
		Platform:             platform,
		ExcludeFirstPriority: excludeFirstPriority,
	})
	if err != nil {
		return nil, err
	}
	return subscriptionAccountInfoToBiz(reply.GetAccount()), nil
}

// GetSubscriptionAccountByID materializes a single subscription account (with
// secrets) by id for session-stickiness reuse. Returns (nil, nil) when the id
// is unknown. It reuses the WithSecrets-preferred by-id RPC shared with the
// credential/resolver path (see ChannelSubscriptionAccountStore).
func (a *ChannelAdapter) GetSubscriptionAccountByID(ctx context.Context, accountID int64) (*relaybiz.SubscriptionAccount, error) {
	reply, err := NewChannelSubscriptionAccountStore(a.client).getSubscriptionAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return subscriptionAccountInfoToBiz(reply.GetAccount()), nil
}

// subscriptionAccountInfoToBiz maps the shared proto account message into the
// relay biz account. Returns nil for a nil info so callers surface a clean miss.
func subscriptionAccountInfoToBiz(account *commonv1.SubscriptionAccountInfo) *relaybiz.SubscriptionAccount {
	if account == nil {
		return nil
	}
	return &relaybiz.SubscriptionAccount{
		ID:                    account.GetId(),
		Name:                  account.GetName(),
		Platform:              account.GetPlatform(),
		AccountType:           account.GetAccountType(),
		Status:                account.GetStatus(),
		BaseURL:               account.GetBaseUrl(),
		Group:                 account.GetGroup(),
		Models:                splitModels(account.GetModels()),
		Priority:              account.GetPriority(),
		AccessToken:           account.GetAccessToken(),
		AccountID:             account.GetAccountId(),
		Fingerprint:           account.GetFingerprint(),
		Concurrency:           account.GetConcurrency(),
		RPMLimit:              account.GetRpmLimit(),
		SessionWindowLimitUSD: account.GetSessionWindowLimitUsd(),
		ModelMapping:          account.GetModelMapping(),
	}
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
		ID:             ch.Id,
		Type:           ch.Type,
		Name:           ch.Name,
		Status:         ch.Status,
		BaseURL:        ch.BaseUrl,
		Group:          ch.Group,
		Models:         splitModels(ch.Models),
		Priority:       ch.Priority,
		Key:            ch.Key,
		ModelMapping:   ch.GetModelMapping(),
		RestrictModels: ch.GetRestrictModels(),
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
