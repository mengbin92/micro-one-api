package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
)

type pricingGroupReader struct{}

func (pricingGroupReader) GetRoutingGroup(context.Context, int64) (*routing.Group, error) {
	return &routing.Group{ID: 10, Key: "default", Status: "enabled", Revision: 1}, nil
}

func TestRoutingGroupPriceWithoutSubscription(t *testing.T) {
	t.Setenv("BILLING_REQUEST_SNAPSHOT_V2", "true")
	uc := NewBillingUsecaseWithPricing(nil, nil, nil, nil, PricingConfig{})
	uc.SetRoutingGroupReader(pricingGroupReader{})
	uc.subscription = &mockSubscriptionPrimitives{}
	quote, err := uc.RoutingGroupPrice(context.Background(), 10, 1)
	require.NoError(t, err)
	require.False(t, quote.SubscriptionCovered)
	require.Positive(t, quote.PriceRatio)
}
