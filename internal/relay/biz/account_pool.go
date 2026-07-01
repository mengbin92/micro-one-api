package biz

import (
	"context"
	"sync/atomic"
	"time"

	"micro-one-api/internal/pkg/metrics"
)

type AccountPoolMetrics struct {
	Checked        int64
	RuntimeBlocked int64
	Allowed        int64
}

// AccountPool applies relay-gateway runtime schedulability checks to
// subscription accounts selected by channel-service. Persistent status,
// ability and priority checks remain owned by channel-service.
type AccountPool struct {
	blocker RuntimeBlocker

	checked        atomic.Int64
	runtimeBlocked atomic.Int64
	allowed        atomic.Int64
}

func NewAccountPool(blocker RuntimeBlocker) *AccountPool {
	if blocker == nil {
		blocker = NoopRuntimeBlocker{}
	}
	return &AccountPool{blocker: blocker}
}

func (p *AccountPool) IsSchedulable(ctx context.Context, account *SubscriptionAccount, now time.Time) bool {
	if p == nil || account == nil || account.ID <= 0 {
		return false
	}
	p.checked.Add(1)
	if now.IsZero() {
		now = time.Now()
	}
	if p.blocker != nil {
		if _, blocked := p.blocker.IsBlocked(ctx, account.ID, now); blocked {
			p.runtimeBlocked.Add(1)
			metrics.RelayAccountPoolChecksTotal.WithLabelValues("runtime_blocked").Inc()
			return false
		}
	}
	p.allowed.Add(1)
	metrics.RelayAccountPoolChecksTotal.WithLabelValues("allowed").Inc()
	return true
}

func (p *AccountPool) Metrics() AccountPoolMetrics {
	if p == nil {
		return AccountPoolMetrics{}
	}
	return AccountPoolMetrics{
		Checked:        p.checked.Load(),
		RuntimeBlocked: p.runtimeBlocked.Load(),
		Allowed:        p.allowed.Load(),
	}
}
