package service

import (
	"context"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/domain/authorization"

	"github.com/stretchr/testify/require"
)

func TestPaymentMutationsRequireSystemCapability(t *testing.T) {
	svc := &BillingService{}
	ctx := authorization.WithExternal(context.Background())
	_, err := svc.MarkPaymentOrderPaid(ctx, &billingv1.MarkPaymentOrderPaidRequest{})
	require.Error(t, err)
	_, err = svc.MarkPaymentOrderAssetIssued(ctx, &billingv1.MarkPaymentOrderAssetIssuedRequest{})
	require.Error(t, err)
	_, err = svc.UnmarkPaymentOrderAssetIssued(ctx, &billingv1.UnmarkPaymentOrderAssetIssuedRequest{})
	require.Error(t, err)
}
