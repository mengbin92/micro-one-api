package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/execution"
	"micro-one-api/platform/security/serviceidentity"
	"testing"
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
