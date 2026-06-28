package websocket

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionState(t *testing.T) {
	tracker := NewConnectionTracker(DefaultDrainConfig())

	closeCalled := atomic.Int32{}
	conn := tracker.NewConnection("test-1", func() error {
		closeCalled.Add(1)
		return nil
	})

	if conn.GetState() != StateActive {
		t.Errorf("Expected StateActive, got %d", conn.GetState())
	}

	conn.SetState(StateDraining)
	if conn.GetState() != StateDraining {
		t.Errorf("Expected StateDraining, got %d", conn.GetState())
	}

	err := conn.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}

	if conn.GetState() != StateClosed {
		t.Errorf("Expected StateClosed, got %d", conn.GetState())
	}

	if closeCalled.Load() != 1 {
		t.Error("Close function was not called")
	}

	// Calling Close again should be idempotent
	conn.Close()
	if closeCalled.Load() != 1 {
		t.Error("Close function was called multiple times")
	}
}

func TestConnectionTracker(t *testing.T) {
	tracker := NewConnectionTracker(DefaultDrainConfig())

	// Create some connections
	conn1 := tracker.NewConnection("conn-1", func() error { return nil })
	conn2 := tracker.NewConnection("conn-2", func() error { return nil })
	conn3 := tracker.NewConnection("conn-3", func() error { return nil })

	if tracker.ActiveCount() != 3 {
		t.Errorf("Expected 3 active connections, got %d", tracker.ActiveCount())
	}

	// Close one connection
	conn1.Close()
	if tracker.ActiveCount() != 2 {
		t.Errorf("Expected 2 active connections, got %d", tracker.ActiveCount())
	}

	// Unregister should remove from tracker
	conn2.Unregister()
	if tracker.ActiveCount() != 1 {
		t.Errorf("Expected 1 active connection, got %d", tracker.ActiveCount())
	}

	// Clean up
	conn3.Close()
}

func TestDrain(t *testing.T) {
	tracker := NewConnectionTracker(&DrainConfig{
		DrainTimeout:       100 * time.Millisecond,
		CloseTimeout:       50 * time.Millisecond,
		MaxConcurrentClose: 10,
	})

	// Create connections that close slowly
	closeCount := atomic.Int32{}
	slowClose := func() error {
		time.Sleep(10 * time.Millisecond)
		closeCount.Add(1)
		return nil
	}

	for range 5 {
		tracker.NewConnection("test-conn", slowClose)
	}

	// Start drain
	ctx := context.Background()
	err := tracker.Drain(ctx)
	if err != nil {
		t.Errorf("Drain() error = %v", err)
	}

	// All connections should be closed
	if closeCount.Load() != 5 {
		t.Errorf("Expected 5 connections closed, got %d", closeCount.Load())
	}

	if tracker.ActiveCount() != 0 {
		t.Errorf("Expected 0 active connections, got %d", tracker.ActiveCount())
	}

	// Verify metrics
	metrics := tracker.Metrics()
	if metrics.TotalConnections != 5 {
		t.Errorf("Expected TotalConnections=5, got %d", metrics.TotalConnections)
	}
	if metrics.ClosedGracefully < 5 {
		t.Errorf("Expected at least 5 gracefully closed, got %d", metrics.ClosedGracefully)
	}
}

func TestDrainWithTimeout(t *testing.T) {
	tracker := NewConnectionTracker(&DrainConfig{
		DrainTimeout:       50 * time.Millisecond,
		CloseTimeout:       200 * time.Millisecond,
		MaxConcurrentClose: 10,
	})

	closeStarted := atomic.Int32{}
	// Create connections that close slowly but eventually do close
	slowClose := func() error {
		closeStarted.Add(1)
		time.Sleep(150 * time.Millisecond)
		return nil
	}

	for range 3 {
		tracker.NewConnection("slow-conn", slowClose)
	}

	// Start drain - should timeout and force close
	ctx := context.Background()
	err := tracker.Drain(ctx)
	// Expect timeout error since we're forcing close
	if err != nil && err != context.DeadlineExceeded {
		t.Logf("Drain() error: %v", err)
	}

	// Give time for force close to complete
	time.Sleep(250 * time.Millisecond)

	// All connections should eventually be closed
	if tracker.ActiveCount() != 0 {
		t.Errorf("Expected 0 active connections after force close, got %d", tracker.ActiveCount())
	}

	// Verify that close was called on all connections
	if closeStarted.Load() != 3 {
		t.Errorf("Expected 3 close calls, got %d", closeStarted.Load())
	}
}

func TestConnectionMetadata(t *testing.T) {
	tracker := NewConnectionTracker(DefaultDrainConfig())
	conn := tracker.NewConnection("meta-conn", func() error { return nil })

	conn.SetMetadata("user_id", "12345")
	conn.SetMetadata("channel", "test")

	if conn.Metadata()["user_id"] != "12345" {
		t.Error("Failed to set/get user_id metadata")
	}

	if conn.ID() != "meta-conn" {
		t.Error("Wrong connection ID")
	}
}

func TestIsDraining(t *testing.T) {
	tracker := NewConnectionTracker(DefaultDrainConfig())

	if tracker.IsDraining() {
		t.Error("Tracker should not be draining initially")
	}

	// Start drain in background
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_ = tracker.Drain(ctx)
	}()

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	if !tracker.IsDraining() {
		t.Error("Tracker should be draining")
	}
}

func TestDrainMetrics(t *testing.T) {
	tracker := NewConnectionTracker(DefaultDrainConfig())
	conn := tracker.NewConnection("metrics-conn", func() error { return nil })

	metrics := tracker.Metrics()
	if metrics.TotalConnections != 1 {
		t.Errorf("Expected TotalConnections=1, got %d", metrics.TotalConnections)
	}
	if metrics.ActiveConnections != 1 {
		t.Errorf("Expected ActiveConnections=1, got %d", metrics.ActiveConnections)
	}

	conn.Close()
	metrics = tracker.Metrics()
	if metrics.ActiveConnections != 0 {
		t.Errorf("Expected ActiveConnections=0, got %d", metrics.ActiveConnections)
	}
}

func TestConnectionCloseError(t *testing.T) {
	tracker := NewConnectionTracker(DefaultDrainConfig())

	expectedErr := errors.New("close failed")
	conn := tracker.NewConnection("error-conn", func() error {
		return expectedErr
	})

	err := conn.Close()
	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}

	// Connection should still be unregistered even with error
	if tracker.ActiveCount() != 0 {
		t.Errorf("Expected 0 active connections after failed close, got %d", tracker.ActiveCount())
	}
}
