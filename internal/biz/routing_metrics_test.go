package biz

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"micro-one-api/platform/metrics"
)

func TestRoutingRejectionMetrics(t *testing.T) {
	t.Setenv("RELAY_ROUTING_CONTEXT_V2", "true")
	t.Setenv("SUBSCRIPTION_ENTITLEMENTS_V2", "false")
	t.Setenv("RELAY_ROUTING_ORDERED", "false")
	identity := &fixedIdentityFake{}
	uc := NewRelayUsecase(identity, &fixedChannelFake{}, nil, nil)
	capability := metrics.RoutingAdmissionRejected.WithLabelValues("resolve", "identity_capability")
	before := testutil.ToFloat64(capability)
	require.Error(t, uc.ResolveRoutingContext(context.Background(), nil, RoutingResolveOptions{}))
	require.Equal(t, before+1, testutil.ToFloat64(capability))
	auth, err := identity.GetAuthSnapshot(context.Background(), "key-a", "")
	require.NoError(t, err)
	denied := metrics.RoutingAdmissionRejected.WithLabelValues("resolve", "policy_denied")
	before = testutil.ToFloat64(denied)
	require.NoError(t, uc.ResolveRoutingContext(context.Background(), auth, RoutingResolveOptions{}))
	require.Equal(t, before, testutil.ToFloat64(denied))
	auth.RoutingFacts.Grants = nil
	require.Error(t, uc.ResolveRoutingContext(context.Background(), auth, RoutingResolveOptions{}))
	require.Equal(t, before+1, testutil.ToFloat64(denied))
	ordered := metrics.RoutingAdmissionRejected.WithLabelValues("ordered", "relay_capability")
	before = testutil.ToFloat64(ordered)
	auth.RoutingFacts.TokenMode = "ordered"
	require.Error(t, uc.ResolveRoutingContext(context.Background(), auth, RoutingResolveOptions{}))
	require.Equal(t, before+1, testutil.ToFloat64(ordered))
}
