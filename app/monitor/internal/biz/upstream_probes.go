package biz

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"math/rand/v2"
	"micro-one-api/domain/execution"
	"micro-one-api/platform/metrics"
	"micro-one-api/platform/security/serviceidentity"
	"time"
)

type UpstreamProbeClient interface {
	Probe(context.Context, string, string) (execution.ProbeResult, error)
}
type ProbeBudgetRepo interface {
	ReserveProbe(context.Context, execution.ProbeTarget, string, time.Time) (bool, error)
	CompleteProbe(context.Context, execution.ProbeTarget, execution.ProbeResult) error
	RecordProbeCoverage(context.Context, string, string, int64, int64) error
}
type UpstreamProbeScheduler struct {
	repo     ProbeBudgetRepo
	client   UpstreamProbeClient
	targets  []execution.ProbeTarget
	interval time.Duration
	now      func() time.Time
	wait     func(context.Context, time.Duration) bool
}

func NewUpstreamProbeScheduler(r ProbeBudgetRepo, c UpstreamProbeClient, targets []execution.ProbeTarget, interval time.Duration) (*UpstreamProbeScheduler, error) {
	if interval < time.Minute || interval > 24*time.Hour {
		return nil, fmt.Errorf("probe interval outside bounds")
	}
	if r == nil || c == nil || len(targets) == 0 || len(targets) > 64 {
		return nil, fmt.Errorf("persistent repo, relay client and bounded targets required")
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if target.ID == "" || seen[target.ID] {
			return nil, fmt.Errorf("probe target identities must be unique")
		}
		seen[target.ID] = true
	}
	return &UpstreamProbeScheduler{repo: r, client: c, targets: targets, interval: interval, now: time.Now, wait: waitForProbe}, nil
}
func waitForProbe(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func (s *UpstreamProbeScheduler) Run(ctx context.Context, onError func(error)) {
	failures := map[string]int{}
	due := map[string]time.Time{}
	nextInterval := func() time.Duration {
		// #nosec G404 -- Scheduling jitter does not generate credentials, permits or security decisions.
		return s.interval - time.Duration(rand.Int64N(int64(s.interval/10)))
	}
	for _, target := range s.targets {
		due[target.ID] = s.now().Add(nextInterval())
	}
	for {
		// Sleep until the earliest target due time. A separate ticker that
		// fires just before next_due skips a healthy target for a whole extra
		// period, violating the configured detection coverage.
		next := due[s.targets[0].ID]
		for _, target := range s.targets[1:] {
			if due[target.ID].Before(next) {
				next = due[target.ID]
			}
		}
		if !s.wait(ctx, max(time.Duration(0), next.Sub(s.now()))) {
			return
		}
		for _, t := range s.targets {
			if ctx.Err() != nil {
				return
			}
			if next := due[t.ID]; s.now().Before(next) {
				continue
			}
			id := "canary_" + uuid.NewString()
			ok, err := s.repo.ReserveProbe(ctx, t, id, s.now())
			if err != nil {
				onError(err)
				due[t.ID] = s.now().Add(s.interval)
				continue
			}
			if !ok {
				due[t.ID] = s.now().Add(s.interval)
				metrics.UpstreamProbeCoverage.WithLabelValues("budget_or_lease").Inc()
				if err := s.repo.RecordProbeCoverage(ctx, t.ID, "budget_or_lease", s.now().Unix(), due[t.ID].Unix()); err != nil {
					onError(err)
				}
				continue
			}
			call, cancel := context.WithTimeout(ctx, 35*time.Second)
			r, err := s.client.Probe(call, t.ID, id)
			cancel()
			if err != nil {
				onError(err)
				r = execution.ProbeResult{AttemptID: id, TargetID: t.ID, Outcome: "probe_unavailable", ObservedAt: s.now().Unix()}
			}
			if r.Outcome == "transport_failure" || r.Outcome == "probe_unavailable" || r.Outcome == "upstream_rejected" {
				failures[t.ID]++
				if failures[t.ID] > 4 {
					failures[t.ID] = 4
				}
				due[t.ID] = s.now().Add(s.interval * time.Duration(1<<failures[t.ID]))
				metrics.UpstreamProbeCoverage.WithLabelValues("backoff").Inc()
			} else {
				failures[t.ID] = 0
				due[t.ID] = s.now().Add(nextInterval())
			}
			metrics.UpstreamProbeRuns.WithLabelValues(t.Protocol, r.Outcome).Inc()
			metrics.UpstreamProbeCoverage.WithLabelValues("executed").Inc()
			if err := s.repo.RecordProbeCoverage(ctx, t.ID, r.Outcome, s.now().Unix(), due[t.ID].Unix()); err != nil {
				onError(err)
			}
			if err = s.repo.CompleteProbe(ctx, t, r); err != nil {
				onError(err)
			}
		}
	}
}

func (uc *MonitorUsecase) ClaimProbePermit(ctx context.Context, target, attempt, digest string) (bool, error) {
	const method = "/api.monitor.v1.MonitorService/ClaimProbePermit"
	if !serviceidentity.HasSystemCapability(ctx, method) || target == "" || len(target) > 64 || attempt == "" || len(attempt) > 128 || len(digest) != 64 {
		return false, fmt.Errorf("invalid probe permit identity")
	}
	r, ok := uc.repo.(interface {
		ClaimProbePermit(context.Context, string, string, string) (bool, error)
	})
	if !ok {
		return false, fmt.Errorf("persistent budget permit unavailable")
	}
	return r.ClaimProbePermit(ctx, target, attempt, digest)
}
