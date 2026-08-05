package server

import (
	"context"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	relaybiz "micro-one-api/internal/biz"

	"google.golang.org/grpc"
)

// recordingChannelClient is a minimal channel-service fake that counts
// RecordChannelUsage calls so tests can assert whether subscription-sourced
// traffic skips the channel-stats path. Unlike rawChannelClient it uses a
// pointer receiver so the counter is observable.
type recordingChannelClient struct {
	channelv1.ChannelServiceClient
	channelUsageCalls int
}

func (c *recordingChannelClient) RecordChannelUsage(context.Context, *channelv1.RecordChannelUsageRequest, ...grpc.CallOption) (*channelv1.RecordChannelUsageResponse, error) {
	c.channelUsageCalls++
	return &channelv1.RecordChannelUsageResponse{Success: true, Message: "ok"}, nil
}

func (c *recordingChannelClient) RecordModelUsage(context.Context, *channelv1.RecordModelUsageRequest, ...grpc.CallOption) (*channelv1.RecordModelUsageResponse, error) {
	return &channelv1.RecordModelUsageResponse{Success: true, Message: "ok"}, nil
}

func (c *recordingChannelClient) RecordSubscriptionAccountQuotaUsage(context.Context, *channelv1.RecordSubscriptionAccountQuotaUsageRequest, ...grpc.CallOption) (*channelv1.RecordSubscriptionAccountQuotaUsageResponse, error) {
	return &channelv1.RecordSubscriptionAccountQuotaUsageResponse{Success: true, Message: "ok"}, nil
}

// TestCommitQuotaSkipsChannelUsageForSubscription verifies the fix for the
// recurring "failed to record channel usage ... channel not found" warning.
// Subscription-sourced traffic executes on a synthetic channel id derived from
// the subscription account id (e.g. id=4 in production logs), so recording it
// against the channel table always fails. The dedicated subscription billing
// path handles this traffic; recordChannelUsage must be skipped.
func TestCommitQuotaSkipsChannelUsageForSubscription(t *testing.T) {
	ch := &recordingChannelClient{}
	billing := &rawBillingClient{}
	srv := &HTTPServer{billingClient: billing, channelClient: ch}

	if err := srv.commitQuota(context.Background(), "res-1", 100, true, usageLogInput{
		ChannelID:             4, // synthetic id mirroring subscription account
		SourceKind:            relaybiz.UpstreamSourceSubscription,
		SubscriptionAccountID: 4,
		ModelName:             "glm-5.2",
		UserID:                1,
	}); err != nil {
		t.Fatalf("commitQuota error = %v", err)
	}

	if ch.channelUsageCalls != 0 {
		t.Fatalf("RecordChannelUsage calls = %d, want 0 for subscription-sourced traffic", ch.channelUsageCalls)
	}
}

// TestCommitQuotaRecordsChannelUsageForChannel ensures the fix does not
// regress the normal channel path: channel-sourced traffic must still record.
func TestCommitQuotaRecordsChannelUsageForChannel(t *testing.T) {
	ch := &recordingChannelClient{}
	billing := &rawBillingClient{}
	srv := &HTTPServer{billingClient: billing, channelClient: ch}

	if err := srv.commitQuota(context.Background(), "res-1", 100, true, usageLogInput{
		ChannelID:  7,
		SourceKind: relaybiz.UpstreamSourceChannel,
		ModelName:  "gpt-4o",
		UserID:     1,
	}); err != nil {
		t.Fatalf("commitQuota error = %v", err)
	}

	if ch.channelUsageCalls != 1 {
		t.Fatalf("RecordChannelUsage calls = %d, want 1 for channel-sourced traffic", ch.channelUsageCalls)
	}
}
