package server

import (
	"context"
	"math"
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
	channelUsageCalls      int
	lastReservationID      string
	subscriptionCostUSD    float64
	subscriptionUsageCalls int
}

func (c *recordingChannelClient) RecordChannelUsage(_ context.Context, req *channelv1.RecordChannelUsageRequest, _ ...grpc.CallOption) (*channelv1.RecordChannelUsageResponse, error) {
	c.channelUsageCalls++
	c.lastReservationID = req.GetReservationId()
	return &channelv1.RecordChannelUsageResponse{Success: true, Message: "ok"}, nil
}

func (c *recordingChannelClient) RecordModelUsage(context.Context, *channelv1.RecordModelUsageRequest, ...grpc.CallOption) (*channelv1.RecordModelUsageResponse, error) {
	return &channelv1.RecordModelUsageResponse{Success: true, Message: "ok"}, nil
}

func (c *recordingChannelClient) RecordSubscriptionAccountQuotaUsage(_ context.Context, req *channelv1.RecordSubscriptionAccountQuotaUsageRequest, _ ...grpc.CallOption) (*channelv1.RecordSubscriptionAccountQuotaUsageResponse, error) {
	c.subscriptionUsageCalls++
	c.subscriptionCostUSD = req.CostUsd
	return &channelv1.RecordSubscriptionAccountQuotaUsageResponse{Success: true, Message: "ok"}, nil
}

// TestApplyPlanInputsSourceKindAndUpstreamModelID (CR 2026-08-05) is a
// regression test for a field-assignment reversal in applyPlanInputs: the
// helper wrote upstreamCostKeyInputsFromPlan's two return values into
// (UpstreamModelID, SourceKind) in the wrong order, so SourceKind was always
// empty and subscription-sourced traffic was never skipped by
// recordChannelUsageFromDetail. The reversed values also corrupted the
// canonical billing cost key (UpstreamModelID was "subscription").
func TestApplyPlanInputsSourceKindAndUpstreamModelID(t *testing.T) {
	plan := &relaybiz.RelayPlan{
		Account: &relaybiz.SubscriptionAccount{ID: 4},
		Channel: &relaybiz.Channel{ID: 4},
	}
	in := usageLogInput{ChannelID: 4}
	in.applyPlanInputs(plan)
	if in.SourceKind != relaybiz.UpstreamSourceSubscription {
		t.Fatalf("SourceKind = %q, want %q (subscription-sourced traffic must be marked)", in.SourceKind, relaybiz.UpstreamSourceSubscription)
	}
	if in.UpstreamModelID != "" {
		t.Fatalf("UpstreamModelID = %q, want empty (source kind leaked into it)", in.UpstreamModelID)
	}
}

// TestApplyPlanInputsChannelSource (CR 2026-08-05) guards the channel branch
// of applyPlanInputs: channel-sourced plans must mark SourceKind=channel and
// keep the real upstream model id.
func TestApplyPlanInputsChannelSource(t *testing.T) {
	plan := &relaybiz.RelayPlan{
		Channel: &relaybiz.Channel{ID: 7, UpstreamModelID: "glm-5.2"},
	}
	in := usageLogInput{ChannelID: 7}
	in.applyPlanInputs(plan)
	if in.SourceKind != relaybiz.UpstreamSourceChannel {
		t.Fatalf("SourceKind = %q, want %q", in.SourceKind, relaybiz.UpstreamSourceChannel)
	}
	if in.UpstreamModelID != "glm-5.2" {
		t.Fatalf("UpstreamModelID = %q, want glm-5.2", in.UpstreamModelID)
	}
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
	if ch.subscriptionUsageCalls != 1 {
		t.Fatalf("RecordSubscriptionAccountQuotaUsage calls = %d, want 1", ch.subscriptionUsageCalls)
	}
	if math.Abs(ch.subscriptionCostUSD-0.01) > 1e-12 {
		t.Fatalf("subscription cost_usd = %.12f, want 0.01 for 100 fixed-point amount units", ch.subscriptionCostUSD)
	}

	// Explicitly assert the SourceKind-driven skip seam: driving
	// recordChannelUsageFromDetail directly with SourceKind="subscription"
	// must also short-circuit (channelID/quota/channelClient are all valid
	// here, so the only way channelUsageCalls stays 0 is the subscription
	// skip). Guards against a future re-reversal of applyPlanInputs silently
	// passing an empty SourceKind through commitQuota.
	before := ch.channelUsageCalls
	srv.recordChannelUsageFromDetail(context.Background(), usageLogInput{
		ChannelID:  4,
		SourceKind: relaybiz.UpstreamSourceSubscription,
	}, 50)
	if ch.channelUsageCalls != before {
		t.Fatalf("recordChannelUsageFromDetail did not skip: channelUsageCalls went %d -> %d, want unchanged for SourceKind=subscription", before, ch.channelUsageCalls)
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
	if ch.lastReservationID != "res-1" {
		t.Fatalf("reservation id = %q, want res-1", ch.lastReservationID)
	}
}

func TestCommitQuotaSkipsUsageCountersForRefundedAttempt(t *testing.T) {
	ch := &recordingChannelClient{}
	srv := &HTTPServer{billingClient: &rawBillingClient{}, channelClient: ch}

	if err := srv.commitQuota(context.Background(), "res-failed", 100, false, usageLogInput{
		ChannelID:  7,
		SourceKind: relaybiz.UpstreamSourceChannel,
		ModelName:  "gpt-4o",
		UserID:     1,
	}); err != nil {
		t.Fatalf("commitQuota error = %v", err)
	}
	if ch.channelUsageCalls != 0 {
		t.Fatalf("RecordChannelUsage calls = %d, want 0 for refunded attempt", ch.channelUsageCalls)
	}
}
