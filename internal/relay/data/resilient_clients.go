package data

import (
	"context"
	"time"

	"google.golang.org/grpc"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	logv1 "micro-one-api/api/log/v1"
	appgrpc "micro-one-api/internal/pkg/grpc"
)

func NewResilientIdentityClient(client identityv1.IdentityServiceClient, timeout time.Duration) identityv1.IdentityServiceClient {
	if client == nil {
		return nil
	}
	return &resilientIdentityClient{
		IdentityServiceClient: client,
		breaker:               appgrpc.NewResilientClient[identityv1.IdentityServiceClient](client, appgrpc.DefaultBreakerConfig("identity"), timeout, nil),
	}
}

type resilientIdentityClient struct {
	identityv1.IdentityServiceClient
	breaker *appgrpc.ResilientClient[identityv1.IdentityServiceClient]
}

func (c *resilientIdentityClient) GetAuthSnapshot(ctx context.Context, req *identityv1.GetAuthSnapshotRequest, opts ...grpc.CallOption) (*identityv1.GetAuthSnapshotReply, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client identityv1.IdentityServiceClient) (any, error) {
		return client.GetAuthSnapshot(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*identityv1.GetAuthSnapshotReply), nil
}

func NewResilientChannelClient(client channelv1.ChannelServiceClient, timeout time.Duration) channelv1.ChannelServiceClient {
	if client == nil {
		return nil
	}
	return &resilientChannelClient{
		ChannelServiceClient: client,
		breaker:              appgrpc.NewResilientClient[channelv1.ChannelServiceClient](client, appgrpc.DefaultBreakerConfig("channel"), timeout, nil),
	}
}

type resilientChannelClient struct {
	channelv1.ChannelServiceClient
	breaker *appgrpc.ResilientClient[channelv1.ChannelServiceClient]
}

func (c *resilientChannelClient) SelectChannel(ctx context.Context, req *channelv1.SelectChannelRequest, opts ...grpc.CallOption) (*channelv1.SelectChannelReply, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client channelv1.ChannelServiceClient) (any, error) {
		return client.SelectChannel(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*channelv1.SelectChannelReply), nil
}

func (c *resilientChannelClient) SelectSubscriptionAccount(ctx context.Context, req *channelv1.SelectSubscriptionAccountRequest, opts ...grpc.CallOption) (*channelv1.SelectSubscriptionAccountReply, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client channelv1.ChannelServiceClient) (any, error) {
		return client.SelectSubscriptionAccount(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*channelv1.SelectSubscriptionAccountReply), nil
}

func (c *resilientChannelClient) GetSubscriptionAccount(ctx context.Context, req *channelv1.GetSubscriptionAccountRequest, opts ...grpc.CallOption) (*channelv1.GetSubscriptionAccountReply, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client channelv1.ChannelServiceClient) (any, error) {
		return client.GetSubscriptionAccount(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*channelv1.GetSubscriptionAccountReply), nil
}

func (c *resilientChannelClient) ListAvailableModels(ctx context.Context, req *channelv1.ListAvailableModelsRequest, opts ...grpc.CallOption) (*channelv1.ListAvailableModelsReply, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client channelv1.ChannelServiceClient) (any, error) {
		return client.ListAvailableModels(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*channelv1.ListAvailableModelsReply), nil
}

func (c *resilientChannelClient) RecordChannelUsage(ctx context.Context, req *channelv1.RecordChannelUsageRequest, opts ...grpc.CallOption) (*channelv1.RecordChannelUsageResponse, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client channelv1.ChannelServiceClient) (any, error) {
		return client.RecordChannelUsage(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*channelv1.RecordChannelUsageResponse), nil
}

func (c *resilientChannelClient) RecordChannelHealth(ctx context.Context, req *channelv1.RecordChannelHealthRequest, opts ...grpc.CallOption) (*channelv1.RecordChannelHealthResponse, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client channelv1.ChannelServiceClient) (any, error) {
		return client.RecordChannelHealth(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*channelv1.RecordChannelHealthResponse), nil
}

func NewResilientBillingClient(client billingv1.BillingServiceClient, timeout time.Duration) billingv1.BillingServiceClient {
	if client == nil {
		return nil
	}
	return &resilientBillingClient{
		BillingServiceClient: client,
		breaker:              appgrpc.NewResilientClient[billingv1.BillingServiceClient](client, appgrpc.DefaultBreakerConfig("billing"), timeout, nil),
	}
}

type resilientBillingClient struct {
	billingv1.BillingServiceClient
	breaker *appgrpc.ResilientClient[billingv1.BillingServiceClient]
}

func (c *resilientBillingClient) ReserveQuota(ctx context.Context, req *billingv1.ReserveQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReserveQuotaResponse, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client billingv1.BillingServiceClient) (any, error) {
		return client.ReserveQuota(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*billingv1.ReserveQuotaResponse), nil
}

func (c *resilientBillingClient) CommitQuota(ctx context.Context, req *billingv1.CommitQuotaRequest, opts ...grpc.CallOption) (*billingv1.CommitQuotaResponse, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client billingv1.BillingServiceClient) (any, error) {
		return client.CommitQuota(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*billingv1.CommitQuotaResponse), nil
}

func (c *resilientBillingClient) ReleaseQuota(ctx context.Context, req *billingv1.ReleaseQuotaRequest, opts ...grpc.CallOption) (*billingv1.ReleaseQuotaResponse, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client billingv1.BillingServiceClient) (any, error) {
		return client.ReleaseQuota(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*billingv1.ReleaseQuotaResponse), nil
}

func (c *resilientBillingClient) GetAccountSnapshot(ctx context.Context, req *billingv1.GetAccountSnapshotRequest, opts ...grpc.CallOption) (*billingv1.GetAccountSnapshotResponse, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client billingv1.BillingServiceClient) (any, error) {
		return client.GetAccountSnapshot(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*billingv1.GetAccountSnapshotResponse), nil
}

func NewResilientLogClient(client logv1.LogServiceClient, timeout time.Duration) logv1.LogServiceClient {
	if client == nil {
		return nil
	}
	return &resilientLogClient{
		LogServiceClient: client,
		breaker:          appgrpc.NewResilientClient[logv1.LogServiceClient](client, appgrpc.DefaultBreakerConfig("log"), timeout, nil),
	}
}

type resilientLogClient struct {
	logv1.LogServiceClient
	breaker *appgrpc.ResilientClient[logv1.LogServiceClient]
}

func (c *resilientLogClient) IngestLog(ctx context.Context, req *logv1.IngestLogRequest, opts ...grpc.CallOption) (*logv1.IngestLogResponse, error) {
	resp, err := c.breaker.Execute(ctx, func(ctx context.Context, client logv1.LogServiceClient) (any, error) {
		return client.IngestLog(ctx, req, opts...)
	})
	if err != nil {
		return nil, err
	}
	return resp.(*logv1.IngestLogResponse), nil
}
