package biz

import (
	"micro-one-api/platform/metrics"
	"sync"
)

// Shared per relay process: ten bootstrap retries, then one retry per ten primary calls.
type retryBudget struct {
	mu      sync.Mutex
	credits int
}

func newRetryBudget() *retryBudget { return &retryBudget{credits: 100} }

func (b *retryBudget) primary() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.credits = min(100, b.credits+1)
}

func (b *retryBudget) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.credits < 10 {
		metrics.RelayRetryBudgetRejected.Inc()
		return false
	}
	b.credits -= 10
	return true
}

func (uc *RelayUsecase) RecordPrimaryAttempt(ch *Channel) {
	if uc != nil && ch != nil && ch.UpstreamConfigurationRevision != "" {
		uc.retryBudget.primary()
	}
}

func (uc *RelayUsecase) AllowUpstreamRetry(ch *Channel) bool {
	return uc == nil || ch == nil || ch.UpstreamConfigurationRevision == "" || uc.retryBudget.allow()
}
