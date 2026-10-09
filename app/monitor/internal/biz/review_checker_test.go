package biz

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type boundedProbeClient struct {
	entered chan struct{}
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (*boundedProbeClient) ListEnabledChannels(context.Context, int32, int32) ([]ChannelProbeSummary, error) {
	channels := make([]ChannelProbeSummary, 8)
	for i := range channels {
		channels[i] = ChannelProbeSummary{ID: int64(i + 1), Type: 1}
	}
	return channels, nil
}
func (c *boundedProbeClient) GetChannelDetail(ctx context.Context, _ int64) (*ChannelProbeDetail, error) {
	n := c.active.Add(1)
	defer c.active.Add(-1)
	for old := c.peak.Load(); n > old; old = c.peak.Load() {
		if c.peak.CompareAndSwap(old, n) {
			break
		}
	}
	c.entered <- struct{}{}
	select {
	case <-c.release:
		return nil, errors.New("probe unavailable")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*boundedProbeClient) RecordChannelHealth(ctx context.Context, _ int64, _ bool, _ string, _ int64) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestHealthCheckerLimitsParallelProbesAndRecordDeadline(t *testing.T) {
	client := &boundedProbeClient{entered: make(chan struct{}, 8), release: make(chan struct{})}
	checker := NewChannelHealthChecker(client, ChannelHealthCheckerConfig{Timeout: time.Second})
	done := make(chan struct{})
	go func() { checker.CheckOnce(context.Background()); close(done) }()
	for i := 0; i < 4; i++ {
		select {
		case <-client.entered:
		case <-time.After(time.Second):
			t.Fatal("probes still serial")
		}
	}
	require.EqualValues(t, 4, client.peak.Load())
	close(client.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("probes did not finish")
	}
	require.EqualValues(t, 4, client.peak.Load())
	checker.cfg.Timeout = 10 * time.Millisecond
	done = make(chan struct{})
	go func() { checker.record(context.Background(), 1, true, "", 0); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("health recording has no deadline")
	}
}
