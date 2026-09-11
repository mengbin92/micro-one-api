package data

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	applogger "micro-one-api/platform/logging"

	"go.uber.org/zap"
)

// modelHealthQueue moves passive model-health sampling off the request
// goroutine. RecordModelHealth is a best-effort additive RPC whose server side
// is a read-modify-write on the single row keyed (source, model): issuing it
// inline serializes every concurrent request for one hot route behind that row
// lock. Samples are queued FIFO to a single worker — submission order is
// preserved, and per-route interleaving across concurrent requests is no more
// deterministic than the row-lock ordering the synchronous path already had.
//
// Trade-off, accepted by the passive-health contract: a full queue drops the
// sample (counted), and a process crash loses at most the queued samples.
// Neither affects billing or routing eligibility.
type modelHealthQueue struct {
	client    channelv1.ChannelServiceClient
	enqueue   chan modelHealthOp
	callCount atomic.Int64
	dropped   atomic.Int64
	failed    atomic.Int64
	startOnce sync.Once
	closed    atomic.Bool
	wg        sync.WaitGroup
}

// modelHealthOp is one queued recording. A nil request marks a flush point:
// the worker signals done once every earlier sample has been submitted.
type modelHealthOp struct {
	request *channelv1.RecordModelHealthRequest
	done    chan struct{}
}

// modelHealthQueueDepth bounds queued samples. Beyond this the caller drops:
// health is a passive signal and must never backpressure relaying.
const modelHealthQueueDepth = 2048

// modelHealthCallTimeout bounds one background RPC so a wedged channel-service
// cannot stall the worker indefinitely.
const modelHealthCallTimeout = 10 * time.Second

func newModelHealthQueue(client channelv1.ChannelServiceClient) *modelHealthQueue {
	return &modelHealthQueue{client: client, enqueue: make(chan modelHealthOp, modelHealthQueueDepth)}
}

// start launches the single worker. Lazy so adapters that never record model
// health (tests, callers without the RPC) spawn no goroutine.
func (q *modelHealthQueue) start() {
	q.startOnce.Do(func() {
		q.wg.Add(1)
		go q.run()
	})
}

func (q *modelHealthQueue) run() {
	defer q.wg.Done()
	for op := range q.enqueue {
		if op.request == nil {
			close(op.done)
			continue
		}
		q.submit(op.request)
	}
}

func (q *modelHealthQueue) submit(req *channelv1.RecordModelHealthRequest) {
	defer func() {
		// Some lightweight callers embed ChannelServiceClient only to fake the
		// older required methods; the model-health method then panics through
		// a nil embedded interface. The call now runs on the worker goroutine,
		// so the recover must live here — an escaping panic would kill the
		// process instead of merely dropping the sample.
		if r := recover(); r != nil {
			// Same sampled Warn as the RPC-failure branch below: a panicking
			// client is precisely the systematically failing path that Warn
			// exists to surface, so it must not be counter-only silence.
			if failed := q.failed.Add(1); failed%100 == 1 {
				applogger.Log.Warn("model health sample dropped: record panic",
					zap.Any("panic", r), zap.Int64("failed_total", failed))
			}
		}
	}()
	q.callCount.Add(1)
	ctx, cancel := context.WithTimeout(context.Background(), modelHealthCallTimeout)
	defer cancel()
	// The request context is intentionally not propagated: by the time the
	// worker picks the sample up the originating request has usually finished,
	// and its cancellation must not abort the recording.
	reply, err := q.client.RecordModelHealth(ctx, req)
	if err != nil {
		// Warn, not Debug: passive recording is fire-and-forget, so a
		// systematically failing path (nil client, missing RPC, rejected
		// schema) would otherwise be invisible at the production log level
		// while the health page stays silently empty. Sampled so a persistent
		// failure costs one line per 100 samples instead of one per request.
		if failed := q.failed.Add(1); failed%100 == 1 {
			applogger.Log.Warn("model health sample dropped: rpc failed",
				zap.Error(err), zap.Int64("failed_total", failed))
		}
		return
	}
	if reply != nil && !reply.GetSuccess() {
		if failed := q.failed.Add(1); failed%100 == 1 {
			applogger.Log.Warn("model health sample rejected",
				zap.String("message", reply.GetMessage()), zap.Int64("failed_total", failed))
		}
	}
}

// record enqueues one sample without blocking. It returns false when the queue
// is full or closed and the sample was dropped.
func (q *modelHealthQueue) record(req *channelv1.RecordModelHealthRequest) bool {
	if q.closed.Load() {
		return false
	}
	q.start()
	select {
	case q.enqueue <- modelHealthOp{request: req}:
		return true
	default:
		dropped := q.dropped.Add(1)
		if dropped%1000 == 1 {
			applogger.Log.Warn("model health queue full; dropping passive samples", zap.Int64("dropped_total", dropped))
		}
		return false
	}
}

// flush blocks until every sample enqueued before the call has been submitted
// by the worker (FIFO order makes the marker sufficient).
func (q *modelHealthQueue) flush(ctx context.Context) error {
	if q.closed.Load() {
		return nil
	}
	q.start()
	done := make(chan struct{})
	select {
	case q.enqueue <- modelHealthOp{done: done}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// close stops accepting samples and drains the queue. A nil queue is a no-op.
func (q *modelHealthQueue) close(drainTimeout time.Duration) {
	if q == nil || !q.closed.CompareAndSwap(false, true) {
		return
	}
	q.start() // ensure the worker exists so close(q.enqueue) is observed
	close(q.enqueue)
	done := make(chan struct{})
	go func() {
		q.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(drainTimeout):
		applogger.Log.Warn("model health queue drain timed out; dropping remaining samples",
			zap.Int64("dropped_total", q.dropped.Load()))
	}
}

// stats reports submitted / dropped / failed sample counts (diagnostics and tests).
func (q *modelHealthQueue) stats() (submitted, dropped, failed int64) {
	return q.callCount.Load(), q.dropped.Load(), q.failed.Load()
}
