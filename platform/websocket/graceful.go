// Package websocket provides WebSocket connection management with graceful shutdown.
package websocket

import (
	"context"
	"maps"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
	"micro-one-api/pkg/safecast"
	applogger "micro-one-api/platform/logging"
)

// ConnectionState represents the current state of a WebSocket connection.
type ConnectionState int

const (
	// StateActive indicates the connection is active and processing messages.
	StateActive ConnectionState = iota
	// StateDraining indicates the connection is in drain mode (no new messages).
	StateDraining
	// StateClosed indicates the connection is closed.
	StateClosed
)

// ConnectionTracker tracks active WebSocket connections for graceful shutdown.
type ConnectionTracker struct {
	mu            sync.RWMutex
	connections   map[*Connection]bool
	draining      atomic.Bool
	stopCh        chan struct{}
	stopCloseOnce sync.Once
	config        *DrainConfig
	metrics       *drainMetrics
}

// DrainConfig holds configuration for connection draining.
type DrainConfig struct {
	// DrainTimeout is how long to wait for connections to close gracefully.
	DrainTimeout time.Duration
	// CloseTimeout is how long to wait for force close after drain timeout.
	CloseTimeout time.Duration
	// NotifyBeforeClose is how long before close to send notification.
	NotifyBeforeClose time.Duration
	// MaxConcurrentClose is the maximum number of connections to close concurrently.
	MaxConcurrentClose int
}

// DefaultDrainConfig returns default drain configuration.
func DefaultDrainConfig() *DrainConfig {
	return &DrainConfig{
		DrainTimeout:       30 * time.Second,
		CloseTimeout:       10 * time.Second,
		NotifyBeforeClose:  5 * time.Second,
		MaxConcurrentClose: 100,
	}
}

// drainMetrics holds metrics for drain operations.
type drainMetrics struct {
	totalConnections  atomic.Int64
	closedGracefully  atomic.Int64
	closedByForce     atomic.Int64
	drainStartTime    atomic.Int64
	drainCompleteTime atomic.Int64
}

// NewConnectionTracker creates a new connection tracker.
func NewConnectionTracker(cfg *DrainConfig) *ConnectionTracker {
	if cfg == nil {
		cfg = DefaultDrainConfig()
	}
	return &ConnectionTracker{
		connections: make(map[*Connection]bool),
		stopCh:      make(chan struct{}),
		config:      cfg,
		metrics:     &drainMetrics{},
	}
}

// Connection represents a tracked WebSocket connection.
type Connection struct {
	id         string
	state      atomic.Int32 // using int32 for ConnectionState
	tracker    *ConnectionTracker
	closeFunc  func() error
	closeOnce  sync.Once
	forced     atomic.Bool
	createdAt  time.Time
	metadataMu sync.RWMutex
	metadata   map[string]string
}

// NewConnection creates a tracked connection or returns nil once draining starts.
func (ct *ConnectionTracker) NewConnection(id string, closeFunc func() error) *Connection {
	c := &Connection{
		id:        id,
		tracker:   ct,
		closeFunc: closeFunc,
		createdAt: time.Now(),
		metadata:  make(map[string]string),
	}
	c.state.Store(int32(StateActive))

	ct.mu.Lock()
	if ct.draining.Load() {
		ct.mu.Unlock()
		return nil
	}
	ct.connections[c] = true
	ct.metrics.totalConnections.Add(1)
	ct.mu.Unlock()

	applogger.Log.Debug("WebSocket connection registered",
		zap.String("connection_id", id),
		zap.Int64("total", ct.metrics.totalConnections.Load()),
	)

	return c
}

// Unregister removes the connection from tracking.
func (c *Connection) Unregister() {
	c.unregister(true)
}

func (c *Connection) unregister(closed bool) {
	c.tracker.mu.Lock()
	if c.tracker.connections[c] {
		delete(c.tracker.connections, c)
		c.SetState(StateClosed)
		if closed {
			if c.forced.Load() {
				c.tracker.metrics.closedByForce.Add(1)
			} else {
				c.tracker.metrics.closedGracefully.Add(1)
			}
		}
	}
	c.tracker.mu.Unlock()

	applogger.Log.Debug("WebSocket connection unregistered",
		zap.String("connection_id", c.id),
	)
}

// State returns the current connection state.
func (c *Connection) GetState() ConnectionState {
	return ConnectionState(c.state.Load())
}

// SetState sets the connection state.
func (c *Connection) SetState(state ConnectionState) {
	c.state.Store(safecast.IntToInt32Saturating(int(state)))
}

// Close closes the connection gracefully.
func (c *Connection) Close() error {
	return c.close(false)
}

func (c *Connection) close(forced bool) error {
	var err error
	c.closeOnce.Do(func() {
		c.forced.Store(forced)
		err = c.closeFunc()
		c.unregister(err == nil)
	})
	return err
}

// ID returns the connection ID.
func (c *Connection) ID() string {
	return c.id
}

// Metadata returns a snapshot copy of the connection metadata. Concurrent
// handlers previously read/wrote the map without a lock (platform-L8).
func (c *Connection) Metadata() map[string]string {
	c.metadataMu.RLock()
	defer c.metadataMu.RUnlock()
	out := make(map[string]string, len(c.metadata))
	maps.Copy(out, c.metadata)
	return out
}

// SetMetadata sets a metadata key-value pair under the connection's lock
// (platform-L8).
func (c *Connection) SetMetadata(key, value string) {
	c.metadataMu.Lock()
	defer c.metadataMu.Unlock()
	c.metadata[key] = value
}

// IsActive returns whether the connection is active.
func (c *Connection) IsActive() bool {
	return c.GetState() == StateActive
}

// Drain initiates graceful shutdown of all tracked connections.
//
// The drain process:
// 1. Sets draining flag (stops accepting new connections)
// 2. Marks all connections as draining
// 3. Waits for connections to close naturally
// 4. Force-closes remaining connections after timeout
func (ct *ConnectionTracker) Drain(ctx context.Context) error {
	if !ct.draining.CompareAndSwap(false, true) {
		return nil // Already draining
	}

	ct.metrics.drainStartTime.Store(time.Now().Unix())
	defer func() { ct.metrics.drainCompleteTime.Store(time.Now().Unix()) }()

	applogger.Log.Info("Starting WebSocket connection drain",
		zap.Int64("connections", ct.metrics.totalConnections.Load()),
	)

	ct.mu.RLock()
	for conn := range ct.connections {
		conn.state.CompareAndSwap(int32(StateActive), int32(StateDraining))
	}
	ct.mu.RUnlock()

	// Wait for connections to close or timeout
	drainCtx, cancel := context.WithTimeout(ctx, ct.config.DrainTimeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for ct.ActiveCount() > 0 {
		select {
		case <-ticker.C:
		case <-drainCtx.Done():
			return ct.forceCloseRemaining()
		case <-ct.stopCh:
			return nil
		}
	}
	applogger.Log.Info("WebSocket drain completed gracefully", zap.Int64("closed", ct.metrics.closedGracefully.Load()))
	return nil
}

// forceCloseRemaining forcefully closes any remaining connections.
func (ct *ConnectionTracker) forceCloseRemaining() error {
	ct.mu.Lock()
	remaining := make([]*Connection, 0, len(ct.connections))
	for conn := range ct.connections {
		remaining = append(remaining, conn)
	}
	ct.mu.Unlock()

	applogger.Log.Warn("Force closing remaining WebSocket connections",
		zap.Int("count", len(remaining)),
	)

	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(max(ct.config.MaxConcurrentClose, 1), len(remaining)) {
		wg.Go(func() {
			for i := next.Add(1) - 1; i < int64(len(remaining)); i = next.Add(1) - 1 {
				conn := remaining[i]
				if err := conn.close(true); err != nil {
					applogger.Log.Warn("Failed to close connection", zap.String("connection_id", conn.id), zap.Error(err))
				}
			}
		})
	}

	// Wait with timeout
	closeCtx, cancel := context.WithTimeout(context.Background(), ct.config.CloseTimeout)
	defer cancel()

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		return nil
	case <-closeCtx.Done():
		return context.DeadlineExceeded
	}
}

// Close stops waiting for natural close. It is safe to call multiple times.
func (ct *ConnectionTracker) Close() {
	ct.stopCloseOnce.Do(func() { close(ct.stopCh) })
}

// ActiveCount returns the number of active connections.
func (ct *ConnectionTracker) ActiveCount() int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()
	return len(ct.connections)
}

// IsDraining returns whether the tracker is in drain mode.
func (ct *ConnectionTracker) IsDraining() bool {
	return ct.draining.Load()
}

// Metrics returns drain metrics.
func (ct *ConnectionTracker) Metrics() *DrainMetrics {
	return &DrainMetrics{
		TotalConnections:  ct.metrics.totalConnections.Load(),
		ClosedGracefully:  ct.metrics.closedGracefully.Load(),
		ClosedByForce:     ct.metrics.closedByForce.Load(),
		DrainStartTime:    ct.metrics.drainStartTime.Load(),
		DrainCompleteTime: ct.metrics.drainCompleteTime.Load(),
		ActiveConnections: int64(ct.ActiveCount()),
	}
}

// DrainMetrics holds drain operation metrics.
type DrainMetrics struct {
	TotalConnections  int64
	ClosedGracefully  int64
	ClosedByForce     int64
	DrainStartTime    int64
	DrainCompleteTime int64
	ActiveConnections int64
}

// Duration returns the drain duration in seconds.
func (m *DrainMetrics) Duration() float64 {
	if m.DrainCompleteTime == 0 {
		return 0
	}
	return float64(m.DrainCompleteTime - m.DrainStartTime)
}

// DrainHandler provides HTTP handlers for drain operations.
type DrainHandler struct {
	tracker *ConnectionTracker
}

// NewDrainHandler creates a new drain handler.
func NewDrainHandler(tracker *ConnectionTracker) *DrainHandler {
	return &DrainHandler{tracker: tracker}
}

// HandleHealthCheck returns a health check handler that reports drain status.
func (h *DrainHandler) HandleHealthCheck() func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.tracker.IsDraining() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"draining","drain":true}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy","drain":false}`))
	}
}

// SetDrainingForTest flips the draining flag without running the full drain
// goroutine. It is intended for tests that need to assert the drain-gate
// behavior of callers (e.g. HTTP 503 on new upgrades) in isolation.
//
// This is intentionally an exported method rather than living in
// export_test.go because cross-package tests (e.g. internal/server's
// openai_ws_drain_test.go) need to flip the flag without invoking the full
// drain goroutine. _test.go files are not visible outside their own package,
// so the symbol must remain in the production file. It must NEVER be called
// from production code: flipping draining without the drain goroutine would
// make the tracker reject new connections forever without ever force-closing
// the existing ones.
func (ct *ConnectionTracker) SetDrainingForTest(v bool) {
	ct.draining.Store(v)
}
