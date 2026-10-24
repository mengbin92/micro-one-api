package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/execution"
	"micro-one-api/platform/security/serviceidentity"
	"testing"
	"time"
)

type probeRepoFixture struct{ calls int }

func (r *probeRepoFixture) Probe(_ context.Context, t execution.ProbeTarget, id string) (execution.ProbeResult, error) {
	r.calls++
	return execution.ProbeResult{TargetID: t.ID, AttemptID: id}, nil
}
func TestProbeRequiresDedicatedMonitorAndExactTarget(t *testing.T) {
	r := &probeRepoFixture{}
	u := NewUpstreamProbeUsecase(r, []execution.ProbeTarget{{ID: "authorized"}})
	method := "/api.relay.v1.RelayService/ProbeUpstream"
	for _, p := range []serviceidentity.Principal{{Name: "legacy-shared"}, {Name: "admin", Dedicated: true}, {Name: "monitor"}} {
		ctx := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(context.Background(), p), method)
		_, err := u.Probe(ctx, "authorized", "attempt")
		require.Error(t, err)
	}
	require.Zero(t, r.calls)
	u.SetProbePermitClient(probePermitFixture(true))
	trusted := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: "monitor", Dedicated: true}), method)
	_, err := u.Probe(trusted, "unlisted", "attempt")
	require.Error(t, err)
	_, err = u.Probe(trusted, "authorized", "attempt")
	require.NoError(t, err)
	require.Equal(t, 1, r.calls)
}

type probePermitFixture bool

func (p probePermitFixture) ClaimProbePermit(context.Context, string, string, string) (bool, error) {
	return bool(p), nil
}

type countingProbePermit struct{ calls int }

func (p *countingProbePermit) ClaimProbePermit(context.Context, string, string, string) (bool, error) {
	p.calls++
	return true, nil
}

func TestExpiredProbeStopsBeforePermitAndSupplier(t *testing.T) {
	r := &probeRepoFixture{}
	p := &countingProbePermit{}
	u := NewUpstreamProbeUsecase(r, []execution.ProbeTarget{{ID: "expired", ExpiresAt: time.Now().Add(-time.Second).Unix()}})
	u.SetProbePermitClient(p)
	method := "/api.relay.v1.RelayService/ProbeUpstream"
	ctx := serviceidentity.WithRPCMethod(serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: "monitor", Dedicated: true}), method)
	_, err := u.Probe(ctx, "expired", "attempt")
	require.ErrorContains(t, err, "expired")
	require.Zero(t, p.calls, "an expired target cannot consume a one-shot permit")
	require.Zero(t, r.calls, "an expired target cannot call the supplier")
}
