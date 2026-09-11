package data

import (
	"context"

	billingv1 "micro-one-api/api/billing/v1"
	relaybiz "micro-one-api/internal/biz"
)

// BillingSettlementAdapter adapts the billing gRPC client to the relay
// ordered-routing settlement probe.
type BillingSettlementAdapter struct {
	client billingv1.BillingServiceClient
}

func NewBillingSettlementAdapter(client billingv1.BillingServiceClient) *BillingSettlementAdapter {
	return &BillingSettlementAdapter{client: client}
}

var _ relaybiz.RoutingSettlementClient = (*BillingSettlementAdapter)(nil)

func (a *BillingSettlementAdapter) CheckRoutingSettlement(ctx context.Context, userID, groupID int64) (bool, string, error) {
	reply, err := a.client.CheckRoutingSettlement(ctx, &billingv1.CheckRoutingSettlementRequest{UserId: userID, RoutingGroupId: groupID})
	if err != nil {
		return false, "", err
	}
	return reply.GetAllowed(), reply.GetReason(), nil
}
