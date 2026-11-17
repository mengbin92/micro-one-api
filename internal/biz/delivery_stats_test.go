package biz

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/platform/metrics"
)

// blockingObservationSender holds the delivery loop inside Send so the test can
// prove statistics are refreshed on their own goroutine.
type blockingObservationSender struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingObservationSender) Send(context.Context, evidence.SendEvent) (evidence.DeliveryReceipt, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return evidence.DeliveryReceipt{}, errors.New("send failed")
}

type statsObservationRepo struct {
	pending    []PendingObservation
	statsCalls atomic.Int64
	failStats  atomic.Bool
	mu         sync.Mutex
	stats      ObservationOutboxStats
}

func (r *statsObservationRepo) Stats(context.Context) (ObservationOutboxStats, error) {
	r.statsCalls.Add(1)
	if r.failStats.Load() {
		return ObservationOutboxStats{}, errors.New("stats unavailable")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stats, nil
}
func (r *statsObservationRepo) Put(context.Context, evidence.SendEvent) error { return nil }
func (r *statsObservationRepo) Pending(context.Context, int) ([]PendingObservation, error) {
	return r.pending, nil
}
func (r *statsObservationRepo) Acknowledge(context.Context, string, string) error { return nil }
func (r *statsObservationRepo) Defer(context.Context, string, time.Time) error    { return nil }

func TestObservationDeliveryStatsRefreshWhileSendIsBlocked(t *testing.T) {
	send := evidence.SendRef{ObservationID: "obs-1", SendID: "obs-1/1/send", RequestID: "req-1", AttemptNumber: 1, UserID: 7,
		Purpose: evidence.PurposeUser, SourceKind: "channel", SourceID: 4, Model: "exact", Protocol: "openai_chat"}
	record := &evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", Status: "verified"}, HTTPStatus: 200, Complete: true}
	event := evidence.NewSendEvent(send, "terminal", 1, record, evidence.CaptureComplete, "", time.Now())
	repo := &statsObservationRepo{pending: []PendingObservation{{Event: event}}}
	sender := &blockingObservationSender{entered: make(chan struct{}), release: make(chan struct{})}
	delivery := NewObservationDelivery(repo, sender)
	delivery.deliveryEvery = time.Millisecond
	delivery.statsEvery = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); delivery.Run(ctx, func(error) {}) }()

	select {
	case <-sender.entered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("sender was never called")
	}
	base := repo.statsCalls.Load()
	deadline := time.Now().Add(2 * time.Second)
	for repo.statsCalls.Load() < base+3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.GreaterOrEqual(t, repo.statsCalls.Load(), base+3,
		"stats must keep refreshing while a send is blocked, not share its goroutine")

	close(sender.release)
	cancel()
	<-done
}

func TestObservationDeliveryStatsRecoverAfterFailure(t *testing.T) {
	repo := &statsObservationRepo{}
	repo.failStats.Store(true)
	delivery := NewObservationDelivery(repo, nil)
	delivery.deliveryEvery = time.Hour
	delivery.statsEvery = 5 * time.Millisecond

	before := testutil.ToFloat64(metrics.UsageObservationLastStats)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); delivery.Run(ctx, func(error) {}) }()

	deadline := time.Now().Add(2 * time.Second)
	for repo.statsCalls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.GreaterOrEqual(t, repo.statsCalls.Load(), int64(2), "a failing reader must be retried")
	require.Equal(t, before, testutil.ToFloat64(metrics.UsageObservationLastStats), "a failed refresh never fabricates freshness")

	repo.failStats.Store(false)
	deadline = time.Now().Add(2 * time.Second)
	for testutil.ToFloat64(metrics.UsageObservationLastStats) <= before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.Greater(t, testutil.ToFloat64(metrics.UsageObservationLastStats), before, "a recovered refresh must publish freshness")

	cancel()
	<-done
}

type blockingExecutionRepo struct {
	entered    chan struct{}
	release    chan struct{}
	once       sync.Once
	statsCalls atomic.Int64
}

func (r *blockingExecutionRepo) Stats(context.Context) (int64, int64, error) {
	r.statsCalls.Add(1)
	return 0, 0, nil
}
func (r *blockingExecutionRepo) Admit(context.Context, execution.Event) error { return nil }
func (r *blockingExecutionRepo) Put(context.Context, execution.Event) error   { return nil }
func (r *blockingExecutionRepo) Pending(context.Context, int) ([]PendingExecution, error) {
	return []PendingExecution{{Event: execution.Event{}}}, nil
}
func (r *blockingExecutionRepo) Acknowledge(context.Context, string, string) error { return nil }
func (r *blockingExecutionRepo) Defer(context.Context, string, time.Time) error    { return nil }
func (r *blockingExecutionRepo) Send(context.Context, execution.Event) (execution.Receipt, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return execution.Receipt{}, errors.New("send failed")
}

func TestExecutionDeliveryStatsRefreshWhileSendIsBlocked(t *testing.T) {
	repo := &blockingExecutionRepo{entered: make(chan struct{}), release: make(chan struct{})}
	delivery := NewExecutionDelivery(repo)
	delivery.deliveryEvery = time.Millisecond
	delivery.statsEvery = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); delivery.Run(ctx, func(error) {}) }()

	select {
	case <-repo.entered:
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("financial sender was never called")
	}
	base := repo.statsCalls.Load()
	deadline := time.Now().Add(2 * time.Second)
	for repo.statsCalls.Load() < base+3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	require.GreaterOrEqual(t, repo.statsCalls.Load(), base+3,
		"financial stats must keep refreshing while a send is blocked")

	close(repo.release)
	cancel()
	<-done
}

type trackingObservationRepo struct {
	put, acked int
}

func (r *trackingObservationRepo) Stats(context.Context) (ObservationOutboxStats, error) {
	return ObservationOutboxStats{}, nil
}
func (r *trackingObservationRepo) Put(context.Context, evidence.SendEvent) error { r.put++; return nil }
func (r *trackingObservationRepo) Pending(context.Context, int) ([]PendingObservation, error) {
	return nil, nil
}
func (r *trackingObservationRepo) Acknowledge(context.Context, string, string) error {
	r.acked++
	return nil
}
func (r *trackingObservationRepo) Defer(context.Context, string, time.Time) error { return nil }

type failingObservationSender struct{ err error }

func (s failingObservationSender) Send(context.Context, evidence.SendEvent) (evidence.DeliveryReceipt, error) {
	return evidence.DeliveryReceipt{}, s.err
}

type panickingObservationDeliveryRepo struct {
	statsObservationRepo
	statsEntered chan struct{}
	statsActive  atomic.Bool
}

func (r *panickingObservationDeliveryRepo) Stats(ctx context.Context) (ObservationOutboxStats, error) {
	r.statsActive.Store(true)
	close(r.statsEntered)
	defer r.statsActive.Store(false)
	<-ctx.Done()
	return ObservationOutboxStats{}, ctx.Err()
}

func (r *panickingObservationDeliveryRepo) Pending(context.Context, int) ([]PendingObservation, error) {
	<-r.statsEntered
	panic("pending observation read panicked")
}

func TestObservationDeliveryRecoversPendingPanicAndJoinsStats(t *testing.T) {
	repo := &panickingObservationDeliveryRepo{statsEntered: make(chan struct{})}
	delivery := NewObservationDelivery(repo, nil)
	delivery.deliveryEvery = time.Millisecond
	delivery.statsBudget = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan any, 1)
	reported := make(chan error, 4)
	go func() {
		defer func() { done <- recover() }()
		delivery.Run(ctx, func(err error) { reported <- err })
	}()
	select {
	case escaped := <-done:
		require.Nil(t, escaped, "an observation delivery panic must not escape into the process")
		require.False(t, repo.statsActive.Load(), "Run must cancel and join statistics before returning")
	case <-time.After(2 * time.Second):
		t.Fatal("panicking delivery did not stop")
	}
	select {
	case err := <-reported:
		require.Error(t, err)
	default:
		t.Fatal("delivery panic was not reported")
	}
}

type panickingExecutionDeliveryRepo struct {
	blockingExecutionRepo
	statsEntered chan struct{}
	statsActive  atomic.Bool
}

func (r *panickingExecutionDeliveryRepo) Stats(ctx context.Context) (int64, int64, error) {
	r.statsActive.Store(true)
	close(r.statsEntered)
	defer r.statsActive.Store(false)
	<-ctx.Done()
	return 0, 0, ctx.Err()
}

func (r *panickingExecutionDeliveryRepo) Pending(context.Context, int) ([]PendingExecution, error) {
	<-r.statsEntered
	panic("pending execution read panicked")
}

func TestExecutionDeliveryRecoversPendingPanicAndJoinsStats(t *testing.T) {
	repo := &panickingExecutionDeliveryRepo{statsEntered: make(chan struct{})}
	delivery := NewExecutionDelivery(repo)
	delivery.deliveryEvery = time.Millisecond
	delivery.statsBudget = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); metrics.ExecutionOutboxWorkerExpected.Set(0) })
	done := make(chan any, 1)
	reported := make(chan error, 4)
	go func() {
		defer func() { done <- recover() }()
		delivery.Run(ctx, func(err error) { reported <- err })
	}()
	select {
	case escaped := <-done:
		require.Nil(t, escaped, "a financial delivery panic must not escape into the process")
		require.False(t, repo.statsActive.Load(), "Run must cancel and join statistics before returning")
		require.Equal(t, float64(1), testutil.ToFloat64(metrics.ExecutionOutboxWorkerExpected), "a crashed worker remains expected so freshness alerts cannot silently turn off")
	case <-time.After(2 * time.Second):
		t.Fatal("panicking delivery did not stop")
	}
	select {
	case err := <-reported:
		require.Error(t, err)
	default:
		t.Fatal("delivery panic was not reported")
	}
}

// R19: a consumer that cannot store durably must not lead the relay to ACK
// (and therefore delete) its only durable copy.
func TestObservationDeliveryNeverAcknowledgesOnStorageRefusal(t *testing.T) {
	repo := &trackingObservationRepo{}
	delivery := NewObservationDelivery(repo, failingObservationSender{err: ErrObservationStorageUnavailable})
	send := evidence.SendRef{ObservationID: "obs-1", SendID: "obs-1/1/send", RequestID: "req-1", AttemptNumber: 1, UserID: 7,
		Purpose: evidence.PurposeUser, SourceKind: "channel", SourceID: 4, Model: "exact", Protocol: "openai_chat"}
	record := &evidence.Record{Observation: evidence.Observation{Protocol: "openai_chat", Status: "verified"}, HTTPStatus: 200, Complete: true}
	event := evidence.NewSendEvent(send, "terminal", 1, record, evidence.CaptureComplete, "", time.Now())

	_, err := delivery.Deliver(context.Background(), event)
	require.ErrorIs(t, err, ErrObservationStorageUnavailable)
	require.Zero(t, repo.acked, "a refused durable write must never clear the local payload")
	require.Equal(t, 1, repo.put)
}
