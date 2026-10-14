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
}

func NewUpstreamProbeScheduler(r ProbeBudgetRepo, c UpstreamProbeClient, targets []execution.ProbeTarget, interval time.Duration) (*UpstreamProbeScheduler, error) {
	if interval < time.Minute || interval > 24*time.Hour {
		return nil, fmt.Errorf("probe interval outside bounds")
	}
	return &UpstreamProbeScheduler{r, c, targets, interval}, nil
}
func (s *UpstreamProbeScheduler) Run(ctx context.Context, onError func(error)) {
	failures := map[string]int{}
	due := map[string]time.Time{}
	for {
		// #nosec G404 -- Scheduling jitter does not generate credentials, permits or security decisions.
		wait := s.interval - time.Duration(rand.Int64N(int64(s.interval/10)))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		for _, t := range s.targets {
			if ctx.Err() != nil {
				return
			}
			if next := due[t.ID]; time.Now().Before(next) {
				metrics.UpstreamProbeCoverage.WithLabelValues("backoff").Inc()
				if err := s.repo.RecordProbeCoverage(ctx, t.ID, "backoff", time.Now().Unix(), next.Unix()); err != nil {
					onError(err)
				}
				continue
			}
			id := "canary_" + uuid.NewString()
			ok, err := s.repo.ReserveProbe(ctx, t, id, time.Now())
			if err != nil {
				onError(err)
				continue
			}
			if !ok {
				metrics.UpstreamProbeCoverage.WithLabelValues("budget_or_lease").Inc()
				if err := s.repo.RecordProbeCoverage(ctx, t.ID, "budget_or_lease", time.Now().Unix(), time.Now().Add(s.interval).Unix()); err != nil {
					onError(err)
				}
				continue
			}
			call, cancel := context.WithTimeout(ctx, 35*time.Second)
			r, err := s.client.Probe(call, t.ID, id)
			cancel()
			if err != nil {
				onError(err)
				r = execution.ProbeResult{AttemptID: id, TargetID: t.ID, Outcome: "probe_unavailable", ObservedAt: time.Now().Unix()}
			}
			if r.Outcome == "transport_failure" || r.Outcome == "probe_unavailable" || r.Outcome == "upstream_rejected" {
				failures[t.ID]++
				if failures[t.ID] > 4 {
					failures[t.ID] = 4
				}
				due[t.ID] = time.Now().Add(s.interval * time.Duration(1<<failures[t.ID]))
			} else {
				failures[t.ID] = 0
				due[t.ID] = time.Now().Add(s.interval)
			}
			metrics.UpstreamProbeRuns.WithLabelValues(t.Protocol, r.Outcome).Inc()
			metrics.UpstreamProbeCoverage.WithLabelValues("executed").Inc()
			if err := s.repo.RecordProbeCoverage(ctx, t.ID, r.Outcome, time.Now().Unix(), due[t.ID].Unix()); err != nil {
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
