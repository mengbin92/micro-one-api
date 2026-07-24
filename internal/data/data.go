package data

import (
	"context"
	"errors"
	"strings"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relaycredential "micro-one-api/domain/upstream/credential"
	"micro-one-api/internal/biz"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Data aggregates downstream clients and provider adaptors for relay-gateway.
type Data struct {
	Identity biz.IdentityClient
	Channel  biz.ChannelClient
	Accounts relaycredential.SubscriptionAccountResolver
}

// dataClientTimeout is the per-call timeout applied to the circuit-breaker
// wrappers when constructing clients via NewData.
const dataClientTimeout = 30 * time.Second

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
	// Wrap the raw gRPC clients with the same resilient (circuit-breaker +
	// timeout) wrappers the wired path uses, and construct the channel client
	// once so all consumers share breaker state.
	identitySvc := NewResilientIdentityClient(identityv1.NewIdentityServiceClient(identityConn), dataClientTimeout)
	channelSvc := NewResilientChannelClient(channelv1.NewChannelServiceClient(channelConn), dataClientTimeout)
	return &Data{
		Identity: &identityClient{
			client: identitySvc,
		},
		Channel: &channelClient{
			client: channelSvc,
		},
		Accounts: NewChannelSubscriptionAccountStore(channelSvc),
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

func (c *channelClient) SelectSubscriptionAccount(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*biz.SubscriptionAccount, error) {
	resp, err := c.client.SelectSubscriptionAccount(ctx, &channelv1.SelectSubscriptionAccountRequest{
		Group:                group,
		Model:                model,
		Platform:             platform,
		ExcludeFirstPriority: excludeFirstPriority,
	})
	if err != nil {
		return nil, err
	}
	info := resp.GetAccount()
	if info == nil {
		return nil, nil
	}
	return &biz.SubscriptionAccount{
		ID:                    info.GetId(),
		Name:                  info.GetName(),
		Platform:              info.GetPlatform(),
		AccountType:           info.GetAccountType(),
		Status:                info.GetStatus(),
		BaseURL:               info.GetBaseUrl(),
		Group:                 info.GetGroup(),
		Models:                splitCSV(info.GetModels()),
		Priority:              info.GetPriority(),
		AccessToken:           info.GetAccessToken(),
		AccountID:             info.GetAccountId(),
		Fingerprint:           info.GetFingerprint(),
		Concurrency:           info.GetConcurrency(),
		RPMLimit:              info.GetRpmLimit(),
		SessionWindowLimitUSD: info.GetSessionWindowLimitUsd(),
		ModelMapping:          info.GetModelMapping(),
	}, nil
}

func (c *channelClient) GetSubscriptionAccountByID(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error) {
	reply, err := NewChannelSubscriptionAccountStore(c.client).getSubscriptionAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return subscriptionAccountInfoToBiz(reply.GetAccount()), nil
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
		ID:             info.Id,
		Type:           info.Type,
		Name:           info.Name,
		Status:         info.Status,
		BaseURL:        info.BaseUrl,
		Group:          info.Group,
		Models:         splitCSV(info.Models),
		Priority:       info.Priority,
		Key:            info.Key,
		ModelMapping:   info.GetModelMapping(),
		RestrictModels: info.GetRestrictModels(),
	}, nil
}

func (c *channelClient) RecordChannelHealth(ctx context.Context, channelID int64, success bool, message string, responseTime int64) error {
	resp, err := c.client.RecordChannelHealth(ctx, &channelv1.RecordChannelHealthRequest{
		ChannelId:    channelID,
		Success:      success,
		Error:        message,
		ResponseTime: responseTime,
	})
	if err != nil {
		return err
	}
	if resp != nil && !resp.GetSuccess() {
		return errors.New(resp.GetMessage())
	}
	return nil
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
