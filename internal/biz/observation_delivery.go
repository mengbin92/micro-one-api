package biz

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"go.uber.org/zap"

	"micro-one-api/domain/requesttrace"
	"micro-one-api/domain/upstream/evidence"
	applogger "micro-one-api/platform/logging"
	"micro-one-api/platform/metrics"
)

var (
	ErrObservationReceipt  = errors.New("durable observation receipt mismatch")
	ErrObservationConflict = errors.New("observation event id reused with different payload")
	// ErrObservationCapacity is a local queue capacity refusal. It is degraded
	// observation, never a business failure.
	ErrObservationCapacity = errors.New("observation outbox capacity exhausted")
	// ErrObservationStorageUnavailable means the consumer cannot durably store
	// evidence (for example an in-memory log repository). It must never be
	// acknowledged, or the relay would delete its only durable copy.
	ErrObservationStorageUnavailable = errors.New("observation storage is not durable")
)

// ObservationOutboxStats is a bounded read of the local observation queue.
type ObservationOutboxStats struct {
	Pending    int64
	OldestUnix int64
	Bytes      int64
	Signals    []ObservationSignal
}

// ObservationSignal is bounded alert metadata; it contains no response payload.
type ObservationSignal struct {
	At                                int64
	Protocol, SourceKind, Status, Gap string
}

func ObservationSignalFor(e evidence.SendEvent) ObservationSignal {
	if e.Category != "terminal" && e.Category != "unmatched" {
		return ObservationSignal{}
	}
	status := "unobserved"
	if e.Record != nil {
		status = evidence.NormalizeStatus(e.Record.Observation.Status)
	}
	signal := ObservationSignal{At: e.ObservedAt / 1000}
	if e.Category == "terminal" && e.Send.Purpose == evidence.PurposeUser && e.Capture == evidence.CaptureComplete && status != "verified" {
		signal.Protocol, signal.SourceKind, signal.Status = protocolLabel(e.Send.Protocol), boundedSourceKind(e.Send.SourceKind), status
	}
	if e.Category == "unmatched" || e.Capture == evidence.CaptureLimited || e.Capture == evidence.CaptureUnknown || status == "unobserved" {
		signal.Gap = gapKind(e, e.Capture, status)
	}
	if signal.Status == "" && signal.Gap == "" {
		return ObservationSignal{}
	}
	return signal
}

type PendingObservation struct {
	Telemetry requesttrace.Carrier
	Event     evidence.SendEvent
	Digest    string
}

// UsageObservationRepo is the local relay observation outbox. It is separate
// from the financial execution outbox so a full financial queue cannot stop
// user evidence persistence.
type UsageObservationRepo interface {
	Stats(ctx context.Context) (ObservationOutboxStats, error)
	Put(ctx context.Context, e evidence.SendEvent) error
	Pending(ctx context.Context, limit int) ([]PendingObservation, error)
	Acknowledge(ctx context.Context, id, digest string) error
	Defer(ctx context.Context, id string, t time.Time) error
}

// UsageObservationSender delivers one event to the Log owner.
type UsageObservationSender interface {
	Send(ctx context.Context, e evidence.SendEvent) (evidence.DeliveryReceipt, error)
}

// ObservationDelivery persists observation events first and delivers them from
// a recoverable worker. Errors here never reach the relay business path.
type ObservationDelivery struct {
	repo          UsageObservationRepo
	sender        UsageObservationSender
	scope         *ObservationScope
	deliveryEvery time.Duration
	statsEvery    time.Duration
	statsBudget   time.Duration
}

func NewObservationDelivery(repo UsageObservationRepo, sender UsageObservationSender) *ObservationDelivery {
	return &ObservationDelivery{repo: repo, sender: sender, scope: &ObservationScope{}, deliveryEvery: time.Second, statsEvery: 15 * time.Second, statsBudget: 2 * time.Second}
}

// SetScope narrows which sources produce observation evidence. It is
// independent of the financial allowlist.
func (u *ObservationDelivery) SetScope(scope *ObservationScope) {
	if scope != nil {
		u.scope = scope
	}
}

// Record persists one observation event locally. The caller treats a failure as
// degraded observation, never as a business failure.
func (u *ObservationDelivery) Record(ctx context.Context, e evidence.SendEvent) error {
	if _, _, err := e.Encode(); err != nil {
		return err
	}
	return u.repo.Put(ctx, e)
}

// Deliver persists, sends and acknowledges one event. The ACK only means the
// Log owner committed the row and agreed on the digest.
func (u *ObservationDelivery) Deliver(ctx context.Context, e evidence.SendEvent) (evidence.DeliveryReceipt, error) {
	if u.sender == nil {
		return evidence.DeliveryReceipt{}, ErrObservationReceipt
	}
	_, digest, err := e.Encode()
	if err != nil {
		return evidence.DeliveryReceipt{}, err
	}
	if err := u.repo.Put(ctx, e); err != nil {
		return evidence.DeliveryReceipt{}, err
	}
	receipt, err := u.sender.Send(ctx, e)
	if err != nil {
		switch {
		case errors.Is(err, ErrObservationConflict):
			metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverConflict).Inc()
			metrics.RecordUsageObservationGap("conflict", float64(time.Now().Unix()))
		case errors.Is(err, ErrObservationStorageUnavailable):
			metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverRejected).Inc()
			metrics.RecordUsageObservationGap("rejected", float64(time.Now().Unix()))
		default:
			metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverFailed).Inc()
		}
		return receipt, err
	}
	if !receipt.Accepted || receipt.Digest != digest {
		return receipt, ErrObservationReceipt
	}
	if err := u.repo.Acknowledge(ctx, e.ID, digest); err != nil {
		return receipt, err
	}
	metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverAcked).Inc()
	return receipt, nil
}

// Run delivers pending events while statistics refresh on an independent,
// cancellable goroutine. A slow or blocked Send can no longer starve the stats
// loop, which is what turns a healthy local queue into a false "stale" alert.
func (u *ObservationDelivery) Run(ctx context.Context, onError func(error)) {
	defer recoverLoop("observation delivery", onError)
	statsCtx, stopStats := context.WithCancel(ctx)
	var stats sync.WaitGroup
	stats.Add(1)
	go func() {
		defer stats.Done()
		defer recoverLoop("observation stats", onError)
		u.statsLoop(statsCtx, onError)
	}()
	defer func() {
		stopStats()
		stats.Wait()
	}()

	u.deliveryLoop(ctx, onError)
}

func (u *ObservationDelivery) statsLoop(ctx context.Context, onError func(error)) {
	stats := time.NewTicker(u.statsEvery)
	defer stats.Stop()
	u.refresh(ctx, onError)
	for {
		select {
		case <-ctx.Done():
			return
		case <-stats.C:
			u.refresh(ctx, onError)
		}
	}
}

func (u *ObservationDelivery) deliveryLoop(ctx context.Context, onError func(error)) {
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
				call, endTask := requesttrace.StartTask(call, row.Telemetry, "observation.outbox.worker")
				_, err := u.Deliver(call, row.Event)
				endTask(err)
				cancel()
				if err != nil {
					onError(err)
					metrics.UsageObservationDelivery.WithLabelValues(evidence.DeliverRetried).Inc()
					if deferErr := u.repo.Defer(ctx, row.Event.ID, time.Now().Add(30*time.Second)); deferErr != nil {
						onError(deferErr)
					}
				}
			}
		}
	}
}

func (u *ObservationDelivery) refresh(parent context.Context, onError func(error)) {
	ctx, cancel := context.WithTimeout(parent, u.statsBudget)
	defer cancel()
	stats, err := u.repo.Stats(ctx)
	if err != nil {
		// Keep the last known values: the stale freshness metric is the signal,
		// not a fabricated zero backlog.
		onError(fmt.Errorf("observation outbox stats: %w", err))
		return
	}
	metrics.UsageObservationOutboxPending.Set(float64(stats.Pending))
	metrics.UsageObservationOutboxBytes.Set(float64(stats.Bytes))
	age := float64(0)
	if stats.OldestUnix > 0 {
		age = float64(time.Now().Unix() - stats.OldestUnix)
		if age < 0 {
			age = 0
		}
	}
	metrics.UsageObservationOutboxOldestAge.Set(age)
	metrics.UsageObservationLastStats.Set(float64(time.Now().Unix()))
	for _, signal := range stats.Signals {
		if signal.Status != "" {
			metrics.RecordUsageObservationAnomaly(signal.Protocol, signal.SourceKind, signal.Status, float64(signal.At))
		}
		if signal.Gap != "" {
			metrics.RecordUsageObservationGap(signal.Gap, float64(signal.At))
		}
	}
}

// recoverLoop keeps one panicking background job from taking the process down.
func recoverLoop(name string, onError func(error)) {
	if recovered := recover(); recovered != nil {
		applogger.Log.Error("observation background job panicked",
			zap.String("job", name), zap.Any("panic", recovered), zap.ByteString("stack", debug.Stack()))
		if onError != nil {
			onError(fmt.Errorf("%s panicked: %v", name, recovered))
		}
	}
}
