package requesttrace

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

type exportItem struct {
	ctx     context.Context
	summary *TimingSummary
}
type ExportStats struct{ Accepted, Exported, Failed, Dropped int64 }

// Pipeline the next group of RPCs while the Log owner's bounded 128-item
// group writer waits for SQL, acknowledgement and encoding of the current one.
const ExporterWorkerCount = 256

// Absorb a brief durable-storage pause without blocking the producer.
// At the controlled 64 x 50ms load, this is roughly three seconds of slack.
const ExporterQueueCapacity = 4096

// Exporter owns a fixed queue and worker count. A slow/down log owner never
// blocks the request, output, billing or shutdown indefinitely.
type Exporter struct {
	mu                                  sync.Mutex
	closed                              bool
	queue                               chan exportItem
	wg                                  sync.WaitGroup
	ctx                                 context.Context
	cancel                              context.CancelFunc
	sink                                func(context.Context, *TimingSummary) error
	accepted, exported, failed, dropped atomic.Int64
	onResult                            func(string)
}

func NewExporter(sink func(context.Context, *TimingSummary) error, onResult func(string)) *Exporter {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Exporter{queue: make(chan exportItem, ExporterQueueCapacity), ctx: ctx, cancel: cancel, sink: sink, onResult: onResult}
	for range ExporterWorkerCount {
		e.wg.Go(e.worker)
	}
	return e
}

func (e *Exporter) result(result string) {
	if e.onResult != nil {
		e.onResult(result)
	}
}

func (e *Exporter) DropTurn() {
	if e == nil {
		return
	}
	e.dropped.Add(1)
	e.result("turn_capacity")
}

func (e *Exporter) Submit(ctx context.Context, summary *TimingSummary) bool {
	if e == nil || summary == nil {
		return false
	}
	if err := summary.Validate(); err != nil {
		e.failed.Add(1)
		e.result("invalid")
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		e.dropped.Add(1)
		e.result("closed")
		return false
	}
	select {
	case e.queue <- exportItem{context.WithoutCancel(ctx), summary.Clone()}:
		e.accepted.Add(1)
		e.result("queued")
		return true
	default:
		e.dropped.Add(1)
		e.result("full")
		return false
	}
}

func (e *Exporter) worker() {
	for item := range e.queue {
		var err error
		for attempt := 0; attempt < 2; attempt++ {
			if e.ctx.Err() != nil {
				err = e.ctx.Err()
				break
			}
			ctx, cancel := context.WithTimeout(item.ctx, 2*time.Second)
			stop := context.AfterFunc(e.ctx, cancel)
			err = e.sink(ctx, item.summary)
			stop()
			cancel()
			if err == nil {
				break
			}
		}
		if err != nil {
			e.failed.Add(1)
			e.result("failed")
		} else {
			e.exported.Add(1)
			e.result("exported")
		}
	}
}

func (e *Exporter) Stats() ExportStats {
	if e == nil {
		return ExportStats{}
	}
	return ExportStats{e.accepted.Load(), e.exported.Load(), e.failed.Load(), e.dropped.Load()}
}

func (e *Exporter) Close(ctx context.Context) error {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		close(e.queue)
	}
	e.mu.Unlock()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		e.cancel()
		return nil
	case <-ctx.Done():
		e.cancel()
		return ctx.Err()
	}
}
