package biz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"micro-one-api/domain/execution"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/metrics"
)

var ErrExecutionReceipt = errors.New("durable execution receipt mismatch")
var ErrExecutionAlreadyAdmitted = errors.New("execution attempt already admitted")

type ExecutionSender interface {
	Send(context.Context, execution.Event) (execution.Receipt, error)
}

type PendingExecution struct {
	Telemetry requesttrace.Carrier
	Event     execution.Event
	Digest    string
}
type ExecutionOutboxRepo interface {
	Stats(context.Context) (int64, int64, error)
	Admit(context.Context, execution.Event) error
	Put(context.Context, execution.Event) error
	Pending(context.Context, int) ([]PendingExecution, error)
	Acknowledge(context.Context, string, string) error
	Defer(context.Context, string, time.Time) error
	Send(context.Context, execution.Event) (execution.Receipt, error)
}
type ExecutionDelivery struct {
	repo          ExecutionOutboxRepo
	deliveryEvery time.Duration
	statsEvery    time.Duration
	statsBudget   time.Duration
}

func NewExecutionDelivery(r ExecutionOutboxRepo) *ExecutionDelivery {
	return &ExecutionDelivery{repo: r, deliveryEvery: time.Second, statsEvery: 15 * time.Second, statsBudget: 2 * time.Second}
}
func (u *ExecutionDelivery) Put(ctx context.Context, e execution.Event) error {
	return u.repo.Put(ctx, e)
}
func (u *ExecutionDelivery) Deliver(ctx context.Context, e execution.Event) (execution.Receipt, error) {
	if err := u.repo.Put(ctx, e); err != nil {
		return execution.Receipt{}, err
	}
	r, err := u.repo.Send(ctx, e)
	if err != nil {
		return r, err
	}
	_, d, err := e.Encode()
	if err != nil {
		return r, err
	}
	if !r.Received || r.Digest != d {
		return r, ErrExecutionReceipt
	}
	return r, u.repo.Acknowledge(ctx, e.ID, d)
}

// Run delivers pending financial evidence while statistics refresh on an
// independent, cancellable goroutine. A blocked Send can no longer starve the
// stats loop and fake a stale/frozen queue.
func (u *ExecutionDelivery) Run(ctx context.Context, onError func(error)) {
	metrics.ExecutionOutboxWorkerExpected.Set(1)
	defer func() {
		if ctx.Err() != nil {
			metrics.ExecutionOutboxWorkerExpected.Set(0)
		}
	}()
	defer recoverLoop("execution delivery", onError)
	statsCtx, stopStats := context.WithCancel(ctx)
	var stats sync.WaitGroup
	stats.Add(1)
	go func() {
		defer stats.Done()
		defer recoverLoop("execution stats", onError)
		u.statsLoop(statsCtx, onError)
	}()
	defer func() {
		stopStats()
		stats.Wait()
	}()

	u.deliveryLoop(ctx, onError)
}

func (u *ExecutionDelivery) statsLoop(ctx context.Context, onError func(error)) {
	stats := time.NewTicker(u.statsEvery)
	defer stats.Stop()
	u.refreshStats(ctx, onError)
	for {
		select {
		case <-ctx.Done():
			return
		case <-stats.C:
			u.refreshStats(ctx, onError)
		}
	}
}

func (u *ExecutionDelivery) deliveryLoop(ctx context.Context, onError func(error)) {
	delivery := time.NewTicker(u.deliveryEvery)
	defer delivery.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-delivery.C:
			rows, err := u.repo.Pending(ctx, 64)
			if err != nil {
				onError(err)
				continue
			}
			for _, row := range rows {
				call, cancel := context.WithTimeout(ctx, 15*time.Second)
				call, endTask := requesttrace.StartTask(call, row.Telemetry, "execution.outbox.worker")
				_, err := u.Deliver(call, row.Event)
				endTask(err)
				cancel()
				if err != nil {
					onError(err)
					if err := u.repo.Defer(ctx, row.Event.ID, time.Now().Add(30*time.Second)); err != nil {
						onError(err)
					}
				}
			}
		}
	}
}

func (u *ExecutionDelivery) Admit(ctx context.Context, e execution.Event) error {
	return u.repo.Admit(ctx, e)
}

func NativeUsageV2Enabled(sourceKind string, sourceID int64, model string) bool {
	if os.Getenv("RELAY_EXECUTION_DELIVERY_V1") != "true" {
		return false
	}
	key := fmt.Sprintf("%s:%d:%s", sourceKind, sourceID, model)
	for _, allowed := range strings.Split(os.Getenv("RELAY_NATIVE_USAGE_V2_ALLOWLIST"), ",") {
		if strings.TrimSpace(allowed) == key {
			return true
		}
	}
	return false
}

// refreshStats reads the financial outbox depth under a two second budget. It
// never zeroes a gauge on failure: the freshness timestamp is the failure
// signal, so a stuck reader cannot look like an empty queue.
func (u *ExecutionDelivery) refreshStats(parent context.Context, onError func(error)) {
	ctx, cancel := context.WithTimeout(parent, u.statsBudget)
	defer cancel()
	count, oldest, err := u.repo.Stats(ctx)
	if err != nil {
		onError(fmt.Errorf("execution outbox stats: %w", err))
		return
	}
	metrics.ExecutionOutboxPending.Set(float64(count))
	age := int64(0)
	if oldest > 0 {
		age = time.Now().Unix() - oldest
		if age < 0 {
			age = 0
		}
	}
	metrics.ExecutionOutboxOldestAge.Set(float64(age))
	metrics.ExecutionOutboxLastStats.Set(float64(time.Now().Unix()))
}
