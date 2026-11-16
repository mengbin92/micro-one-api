package writeauditclient

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	configv1 "micro-one-api/api/config/v1"
	logv1 "micro-one-api/api/log/v1"
	monitorv1 "micro-one-api/api/monitor/v1"
	notifyv1 "micro-one-api/api/notify/v1"
	"micro-one-api/app/admin/internal/biz"
)

// capturingChannelClient records the metadata the adapter actually sent.
type capturingChannelClient struct {
	channelv1.ChannelServiceClient
	md    metadata.MD
	reply *channelv1.ListWriteAuditsResponse
	err   error
}

func (c *capturingChannelClient) ListWriteAudits(ctx context.Context, _ *channelv1.ListWriteAuditsRequest, _ ...grpc.CallOption) (*channelv1.ListWriteAuditsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	return c.reply, c.err
}

type capturingBillingClient struct {
	billingv1.BillingServiceClient
	md    metadata.MD
	reply *billingv1.ListWriteAuditsResponse
	err   error
}

func (c *capturingBillingClient) ListWriteAudits(ctx context.Context, _ *billingv1.ListWriteAuditsRequest, _ ...grpc.CallOption) (*billingv1.ListWriteAuditsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	return c.reply, c.err
}

func outgoingOperatorContext() context.Context {
	md := metadata.NewOutgoingContext(context.Background(), metadata.Pairs(
		"x-operator-authorization", "Bearer operator-jwt",
		"x-authorization-reason", "ops",
	))
	return md
}

func TestChannelSourceStripsOperatorMetadata(t *testing.T) {
	client := &capturingChannelClient{reply: &channelv1.ListWriteAuditsResponse{
		Total: 1, StoreId: "channel-store",
		Items: []*channelv1.WriteAuditItem{{EventId: "ev-1", ActorUserId: 7, Operation: "channel.channel.update", OccurredAt: 9, Result: "success"}},
	}}
	source := NewChannelSource(client)
	batch, err := source.List(outgoingOperatorContext(), biz.OperationAuditFilter{Operation: "channel.channel", Limit: 100})
	require.NoError(t, err)
	require.EqualValues(t, 1, batch.Total)
	require.Len(t, batch.Items, 1)
	require.Equal(t, "channel.channel.update", batch.Items[0].Operation)
	require.Equal(t, "channel-store", batch.StoreID)
	require.Empty(t, client.md.Get("x-operator-authorization"), "owner capability check requires no forwarded operator credential")
	require.Empty(t, client.md.Get("x-authorization-reason"))
}

func TestBillingSourceMapsRows(t *testing.T) {
	client := &capturingBillingClient{reply: &billingv1.ListWriteAuditsResponse{
		Total: 2, StoreId: "billing-store",
		Items: []*billingv1.WriteAuditItem{
			{EventId: "ev-2", ActorUserId: 8, Operation: "billing.reconcile.run", ResourceId: "4", DecisionVersions: `[1]`, Reason: "ops", OccurredAt: 10, Result: "failure"},
			nil,
		},
	}}
	source := NewBillingSource(client)
	_, err := source.List(outgoingOperatorContext(), biz.OperationAuditFilter{Limit: 100})
	require.ErrorIs(t, err, biz.ErrOperationAuditUnavailable, "malformed owner replies must not surface partial rows")
}

func TestNilClientsReportUnavailable(t *testing.T) {
	_, notifyErr := NewNotifySource(nil).List(context.Background(), biz.OperationAuditFilter{Limit: 20})
	require.ErrorIs(t, notifyErr, biz.ErrOperationAuditUnavailable)
	_, monitorErr := NewMonitorSource(nil).List(context.Background(), biz.OperationAuditFilter{Limit: 20})
	require.ErrorIs(t, monitorErr, biz.ErrOperationAuditUnavailable)
	_, logErr := NewLogSource(nil).List(context.Background(), biz.OperationAuditFilter{Limit: 20})
	require.ErrorIs(t, logErr, biz.ErrOperationAuditUnavailable)
	_, configErr := NewConfigSource(nil).List(context.Background(), biz.OperationAuditFilter{Limit: 20})
	require.ErrorIs(t, configErr, biz.ErrOperationAuditUnavailable)
	_, err := NewChannelSource(nil).List(context.Background(), biz.OperationAuditFilter{})
	require.ErrorIs(t, err, biz.ErrOperationAuditUnavailable)
	_, err = NewBillingSource(nil).List(context.Background(), biz.OperationAuditFilter{})
	require.ErrorIs(t, err, biz.ErrOperationAuditUnavailable)
}

type capturingConfigClient struct {
	configv1.ConfigServiceClient
	md  metadata.MD
	req *configv1.ListWriteAuditsRequest
}

func (c *capturingConfigClient) ListWriteAudits(ctx context.Context, req *configv1.ListWriteAuditsRequest, _ ...grpc.CallOption) (*configv1.ListWriteAuditsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	c.req = req
	return &configv1.ListWriteAuditsResponse{Total: 1, StoreId: "config-store", Items: []*configv1.WriteAuditItem{{EventId: "ev-config", ActorUserId: 7, Operation: "config.update", ResourceId: "9", DecisionVersions: "{}", Reason: "ops", OccurredAt: 100, Result: "success"}}}, nil
}
func TestConfigSourcePassesFilterAndStoreIdentity(t *testing.T) {
	client := &capturingConfigClient{}
	batch, err := NewConfigSource(client).List(outgoingOperatorContext(), biz.OperationAuditFilter{ActorUserID: 7, Operation: "config.", ResourceID: "9", Result: "success", StartTimeMS: 20, EndTimeMS: 200, Limit: 40})
	require.NoError(t, err)
	require.Equal(t, "config-store", batch.StoreID)
	require.EqualValues(t, 1, batch.Total)
	require.Len(t, batch.Items, 1)
	require.Equal(t, "ev-config", batch.Items[0].EventID)
	require.EqualValues(t, 7, client.req.ActorUserId)
	require.Equal(t, "config.", client.req.Operation)
	require.Equal(t, "9", client.req.ResourceId)
	require.Equal(t, "success", client.req.Result)
	require.EqualValues(t, 20, client.req.StartTimeMs)
	require.EqualValues(t, 200, client.req.EndTimeMs)
	require.Zero(t, client.req.Page)
	require.EqualValues(t, 40, client.req.PageSize)
	require.Empty(t, client.md.Get("x-operator-authorization"))
	require.Empty(t, client.md.Get("x-authorization-reason"))
}

type capturingLogClient struct {
	logv1.LogServiceClient
	md  metadata.MD
	req *logv1.ListWriteAuditsRequest
}

func (c *capturingLogClient) ListWriteAudits(ctx context.Context, req *logv1.ListWriteAuditsRequest, _ ...grpc.CallOption) (*logv1.ListWriteAuditsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	c.req = req
	return &logv1.ListWriteAuditsResponse{Total: 1, StoreId: "log-store", Items: []*logv1.WriteAuditItem{{EventId: "ev-log", ActorUserId: 7, Operation: "log.update", ResourceId: "9", DecisionVersions: "{}", Reason: "ops", OccurredAt: 100, Result: "success"}}}, nil
}
func TestLogSourcePassesFilterAndStoreIdentity(t *testing.T) {
	client := &capturingLogClient{}
	batch, err := NewLogSource(client).List(outgoingOperatorContext(), biz.OperationAuditFilter{ActorUserID: 7, Operation: "log.", ResourceID: "9", Result: "success", StartTimeMS: 20, EndTimeMS: 200, Limit: 40})
	require.NoError(t, err)
	require.Equal(t, "log-store", batch.StoreID)
	require.EqualValues(t, 1, batch.Total)
	require.Len(t, batch.Items, 1)
	require.Equal(t, "ev-log", batch.Items[0].EventID)
	require.EqualValues(t, 7, client.req.ActorUserId)
	require.Equal(t, "log.", client.req.Operation)
	require.Equal(t, "9", client.req.ResourceId)
	require.Equal(t, "success", client.req.Result)
	require.EqualValues(t, 20, client.req.StartTimeMs)
	require.EqualValues(t, 200, client.req.EndTimeMs)
	require.Zero(t, client.req.Page)
	require.EqualValues(t, 40, client.req.PageSize)
	require.Empty(t, client.md.Get("x-operator-authorization"))
	require.Empty(t, client.md.Get("x-authorization-reason"))
}

type capturingMonitorClient struct {
	monitorv1.MonitorServiceClient
	md  metadata.MD
	req *monitorv1.ListWriteAuditsRequest
}

func (c *capturingMonitorClient) ListWriteAudits(ctx context.Context, req *monitorv1.ListWriteAuditsRequest, _ ...grpc.CallOption) (*monitorv1.ListWriteAuditsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	c.req = req
	return &monitorv1.ListWriteAuditsResponse{Total: 1, StoreId: "monitor-store", Items: []*monitorv1.WriteAuditItem{{EventId: "ev-monitor", ActorUserId: 7, Operation: "monitor.update", ResourceId: "9", DecisionVersions: "{}", Reason: "ops", OccurredAt: 100, Result: "success"}}}, nil
}
func TestMonitorSourcePassesFilterAndStoreIdentity(t *testing.T) {
	client := &capturingMonitorClient{}
	batch, err := NewMonitorSource(client).List(outgoingOperatorContext(), biz.OperationAuditFilter{ActorUserID: 7, Operation: "monitor.", ResourceID: "9", Result: "success", StartTimeMS: 20, EndTimeMS: 200, Limit: 40})
	require.NoError(t, err)
	require.Equal(t, "monitor-store", batch.StoreID)
	require.EqualValues(t, 1, batch.Total)
	require.Len(t, batch.Items, 1)
	require.Equal(t, "ev-monitor", batch.Items[0].EventID)
	require.EqualValues(t, 7, client.req.ActorUserId)
	require.Equal(t, "monitor.", client.req.Operation)
	require.Equal(t, "9", client.req.ResourceId)
	require.Equal(t, "success", client.req.Result)
	require.EqualValues(t, 20, client.req.StartTimeMs)
	require.EqualValues(t, 200, client.req.EndTimeMs)
	require.Zero(t, client.req.Page)
	require.EqualValues(t, 40, client.req.PageSize)
	require.Empty(t, client.md.Get("x-operator-authorization"))
	require.Empty(t, client.md.Get("x-authorization-reason"))
}

type capturingNotifyClient struct {
	notifyv1.NotifyServiceClient
	md  metadata.MD
	req *notifyv1.ListWriteAuditsRequest
}

func (c *capturingNotifyClient) ListWriteAudits(ctx context.Context, req *notifyv1.ListWriteAuditsRequest, _ ...grpc.CallOption) (*notifyv1.ListWriteAuditsResponse, error) {
	c.md, _ = metadata.FromOutgoingContext(ctx)
	c.req = req
	return &notifyv1.ListWriteAuditsResponse{Total: 1, StoreId: "notify-store", Items: []*notifyv1.WriteAuditItem{{EventId: "ev-notify", ActorUserId: 7, Operation: "notify.update", ResourceId: "9", DecisionVersions: "{}", Reason: "ops", OccurredAt: 100, Result: "success"}}}, nil
}
func TestNotifySourcePassesFilterAndStoreIdentity(t *testing.T) {
	client := &capturingNotifyClient{}
	batch, err := NewNotifySource(client).List(outgoingOperatorContext(), biz.OperationAuditFilter{ActorUserID: 7, Operation: "notify.", ResourceID: "9", Result: "success", StartTimeMS: 20, EndTimeMS: 200, Limit: 40})
	require.NoError(t, err)
	require.Equal(t, "notify-store", batch.StoreID)
	require.EqualValues(t, 1, batch.Total)
	require.Len(t, batch.Items, 1)
	require.Equal(t, "ev-notify", batch.Items[0].EventID)
	require.EqualValues(t, 7, client.req.ActorUserId)
	require.Equal(t, "notify.", client.req.Operation)
	require.Equal(t, "9", client.req.ResourceId)
	require.Equal(t, "success", client.req.Result)
	require.EqualValues(t, 20, client.req.StartTimeMs)
	require.EqualValues(t, 200, client.req.EndTimeMs)
	require.Zero(t, client.req.Page)
	require.EqualValues(t, 40, client.req.PageSize)
	require.Empty(t, client.md.Get("x-operator-authorization"))
	require.Empty(t, client.md.Get("x-authorization-reason"))
}

func TestChannelSourceFailsClosedForMissingIdentityAndRPCFailure(t *testing.T) {
	failure := errors.New("owner failed")
	for _, tt := range []struct {
		name  string
		reply *channelv1.ListWriteAuditsResponse
		err   error
	}{
		{name: "nil reply"},
		{name: "empty store identity", reply: &channelv1.ListWriteAuditsResponse{}},
		{name: "negative total", reply: &channelv1.ListWriteAuditsResponse{StoreId: "store", Total: -1}},
		{name: "RPC failure", err: failure},
	} {
		t.Run(tt.name, func(t *testing.T) {
			batch, err := NewChannelSource(&capturingChannelClient{reply: tt.reply, err: tt.err}).List(t.Context(), biz.OperationAuditFilter{Limit: 20})
			require.Error(t, err)
			require.Empty(t, batch.Items)
			require.Empty(t, batch.StoreID)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			}
		})
	}
}
func TestChannelSourceRejectsUnsupportedFetchWindow(t *testing.T) {
	source := NewChannelSource(&capturingChannelClient{})
	for _, filter := range []biz.OperationAuditFilter{{Offset: 1, Limit: 20}, {Limit: 5001}, {Limit: 0}} {
		_, err := source.List(t.Context(), filter)
		require.ErrorIs(t, err, biz.ErrInvalidOperationAuditQuery)
	}
}
