package service

import (
	"context"
	"fmt"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
)

type channelSettlementReporter struct {
	client channelv1.ChannelServiceClient
}

func NewChannelSettlementReporter(client channelv1.ChannelServiceClient) *channelSettlementReporter {
	if client == nil {
		return nil
	}
	return &channelSettlementReporter{client: client}
}

func (r *channelSettlementReporter) RecordSubscriptionAccountQuotaUsage(ctx context.Context, accountID int64, reservationID string, costUSD float64, occurredAt time.Time) error {
	request := &channelv1.RecordSubscriptionAccountQuotaUsageRequest{AccountId: accountID, ReservationId: reservationID, CostUsd: costUSD, CostSource: "billing_commit"}
	if !occurredAt.IsZero() {
		request.OccurredAt = occurredAt.Unix()
	}
	resp, err := r.client.RecordSubscriptionAccountQuotaUsage(ctx, request)
	if err != nil {
		return err
	}
	if resp == nil || !resp.GetSuccess() {
		if resp == nil {
			return fmt.Errorf("empty subscription account quota response")
		}
		return fmt.Errorf("subscription account quota rejected: %s", resp.GetMessage())
	}
	return nil
}
