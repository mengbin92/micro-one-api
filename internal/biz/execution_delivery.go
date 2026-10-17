package biz

import (
	"context"
	"errors"
	"fmt"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/metrics"
	"os"
	"strings"
	"time"
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
type ExecutionDelivery struct{ repo ExecutionOutboxRepo }

func NewExecutionDelivery(r ExecutionOutboxRepo) *ExecutionDelivery {
	return &ExecutionDelivery{repo: r}
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
func (u *ExecutionDelivery) Run(ctx context.Context, onError func(error)) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			count, oldest, statErr := u.repo.Stats(ctx)
			if statErr != nil {
				onError(statErr)
				continue
			}
			metrics.ExecutionOutboxPending.Set(float64(count))
			age := int64(0)
			if oldest > 0 {
				age = time.Now().Unix() - oldest
			}
			metrics.ExecutionOutboxOldestAge.Set(float64(age))
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
