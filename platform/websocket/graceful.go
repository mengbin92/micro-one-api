// Package websocket provides WebSocket connection management with graceful shutdown.
package websocket

import (
	"context"
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
	mu          sync.RWMutex
	connections map[*Connection]bool
	draining    atomic.Bool
	stopCh      chan struct{}
	wg          sync.WaitGroup
	config      *DrainConfig
	metrics     *drainMetrics
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
	createdAt  time.Time
	lastActive time.Time
	metadata   map[string]string
}

// NewConnection creates a new tracked connection.
func (ct *ConnectionTracker) NewConnection(id string, closeFunc func() error) *Connection {
	c := &Connection{
		id:         id,
		tracker:    ct,
		closeFunc:  closeFunc,
		createdAt:  time.Now(),
		lastActive: time.Now(),
		metadata:   make(map[string]string),
	}
	c.state.Store(int32(StateActive))

	ct.mu.Lock()
	ct.connections[c] = true
	ct.connections[c] = true // Track the connection
	ct.mu.Unlock()

	ct.metrics.totalConnections.Add(1)

	applogger.Log.Debug("WebSocket connection registered",
		zap.String("connection_id", id),
		zap.Int64("total", ct.metrics.totalConnections.Load()),
	)

	return c
}

// Unregister removes the connection from tracking.
func (c *Connection) Unregister() {
	c.tracker.mu.Lock()
	delete(c.tracker.connections, c)
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
	var err error
	c.closeOnce.Do(func() {
		err = c.closeFunc()
		c.SetState(StateClosed)
		if err == nil {
			c.tracker.metrics.closedGracefully.Add(1)
		}
		c.Unregister()
	})
	return err
}

// ID returns the connection ID.
func (c *Connection) ID() string {
	return c.id
}

// Metadata returns the connection metadata.
func (c *Connection) Metadata() map[string]string {
	return c.metadata
}

// SetMetadata sets a metadata key-value pair.
func (c *Connection) SetMetadata(key, value string) {
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
// 2. Notifies all connections of impending close
// 3. Waits for connections to close naturally
// 4. Force-closes remaining connections after timeout
func (ct *ConnectionTracker) Drain(ctx context.Context) error {
	if !ct.draining.CompareAndSwap(false, true) {
		return nil // Already draining
	}

	ct.metrics.drainStartTime.Store(time.Now().Unix())
	defer ct.metrics.drainCompleteTime.Store(time.Now().Unix())

	applogger.Log.Info("Starting WebSocket connection drain",
		zap.Int64("connections", ct.metrics.totalConnections.Load()),
	)

	// Get all connections to drain
	ct.mu.RLock()
	conns := make([]*Connection, 0, len(ct.connections))
	for conn := range ct.connections {
		conns = append(conns, conn)
	}
	ct.mu.RUnlock()

	// Set all connections to draining state
	for _, conn := range conns {
		conn.SetState(StateDraining)
	}

	// Wait for connections to close or timeout
	drainCtx, cancel := context.WithTimeout(ctx, ct.config.DrainTimeout)
	defer cancel()

	// Start goroutine to monitor drain progress
	doneCh := make(chan struct{})
	go func() {
		ct.waitForClose(doneCh)
	}()

	select {
	case <-doneCh:
		applogger.Log.Info("WebSocket drain completed gracefully",
			zap.Int64("closed", ct.metrics.closedGracefully.Load()),
		)
		return nil
	case <-drainCtx.Done():
		// Timeout: force close remaining connections
		return ct.forceCloseRemaining()
	}
}

// waitForClose waits for all connections to close.
func (ct *ConnectionTracker) waitForClose(doneCh chan<- struct{}) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer close(doneCh)

	for {
		select {
		case <-ticker.C:
			ct.mu.RLock()
			remaining := len(ct.connections)
			ct.mu.RUnlock()

			if remaining == 0 {
				return
			}

			applogger.Log.Debug("Waiting for connections to close",
				zap.Int("remaining", remaining),
			)
		case <-ct.stopCh:
			return
		}
	}
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

	// Close remaining connections with concurrency limit
	sem := make(chan struct{}, ct.config.MaxConcurrentClose)
	var wg sync.WaitGroup

	for _, conn := range remaining {
		wg.Add(1)
		go func(c *Connection) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := c.Close(); err != nil {
				applogger.Log.Warn("Failed to close connection",
					zap.String("connection_id", c.id),
					zap.Error(err),
				)
				ct.metrics.closedByForce.Add(1)
			}
		}(conn)
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
			w.Write([]byte(`{"status":"draining","drain":true}`))
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"healthy","drain":false}`))
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
