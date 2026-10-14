package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/execution"
)

type schedulerFixture struct {
	at       []time.Time
	outcome  string
	complete func()
}

func (f *schedulerFixture) ReserveProbe(_ context.Context, _ execution.ProbeTarget, _ string, now time.Time) (bool, error) {
	f.at = append(f.at, now)
	return true, nil
}
func (f *schedulerFixture) CompleteProbe(context.Context, execution.ProbeTarget, execution.ProbeResult) error {
	f.complete()
	return nil
}
func (f *schedulerFixture) RecordProbeCoverage(context.Context, string, string, int64, int64) error {
	return nil
}
func (f *schedulerFixture) Probe(_ context.Context, target, attempt string) (execution.ProbeResult, error) {
	return execution.ProbeResult{TargetID: target, AttemptID: attempt, Outcome: f.outcome}, nil
}

func TestSuccessfulProbeRepeatsWithinConfiguredPeriod(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := &schedulerFixture{outcome: "success"}
	f.complete = func() {
		if len(f.at) == 2 {
			cancel()
		}
	}
	s, err := NewUpstreamProbeScheduler(f, f, []execution.ProbeTarget{{ID: "target", Protocol: "openai_chat"}}, time.Minute)
	require.NoError(t, err)
	now := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	s.wait = func(ctx context.Context, delay time.Duration) bool {
		now = now.Add(delay)
		return ctx.Err() == nil
	}
	s.Run(ctx, func(err error) { require.NoError(t, err) })
	require.Len(t, f.at, 2)
	require.LessOrEqual(t, f.at[1].Sub(f.at[0]), time.Minute, "success must not skip the next ticker and silently double the configured period")
}
