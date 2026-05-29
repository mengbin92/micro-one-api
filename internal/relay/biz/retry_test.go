package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultRetryPolicy(t *testing.T) {
	p := DefaultRetryPolicy()
	assert.Equal(t, 3, p.MaxAttempts)
	assert.Equal(t, 500*time.Millisecond, p.InitialInterval)
	assert.Equal(t, 5*time.Second, p.MaxInterval)
	assert.Equal(t, 2.0, p.Multiplier)
	assert.True(t, p.RetryableStatus[429])
	assert.True(t, p.RetryableStatus[500])
	assert.True(t, p.RetryableStatus[502])
	assert.True(t, p.RetryableStatus[503])
}

func TestRetryPolicy_IsRetryable(t *testing.T) {
	p := DefaultRetryPolicy()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"retryable error 429", &RetryableError{Status: 429, Err: errors.New("rate limited")}, true},
		{"retryable error 500", &RetryableError{Status: 500, Err: errors.New("internal")}, true},
		{"non-retryable error 400", &RetryableError{Status: 400, Err: errors.New("bad request")}, false},
		{"upstream status=502", errors.New("upstream error: status=502, body=bad gateway"), true},
		{"upstream status=400", errors.New("upstream error: status=400, body=bad request"), false},
		{"connection refused", errors.New("dial tcp: connection refused"), true},
		{"timeout", errors.New("context deadline exceeded: timeout"), true},
		{"EOF", errors.New("unexpected EOF"), true},
		{"dial tcp", errors.New("dial tcp 10.0.0.1:443: connect: no route to host"), true},
		{"generic error", errors.New("something else"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.IsRetryable(tt.err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRetryPolicy_BackoffDuration(t *testing.T) {
	p := &RetryPolicy{
		InitialInterval: 500 * time.Millisecond,
		MaxInterval:     5 * time.Second,
		Multiplier:      2.0,
	}

	// attempt 0: 500ms * 2^0 = 500ms
	assert.Equal(t, 500*time.Millisecond, p.BackoffDuration(0))
	// attempt 1: 500ms * 2^1 = 1s
	assert.Equal(t, 1*time.Second, p.BackoffDuration(1))
	// attempt 2: 500ms * 2^2 = 2s
	assert.Equal(t, 2*time.Second, p.BackoffDuration(2))
	// attempt 3: 500ms * 2^3 = 4s
	assert.Equal(t, 4*time.Second, p.BackoffDuration(3))
	// attempt 4: 500ms * 2^4 = 8s -> clamped to 5s
	assert.Equal(t, 5*time.Second, p.BackoffDuration(4))
}

func TestUpstreamStatus(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"retryable 429", &RetryableError{Status: 429, Err: errors.New("rate limited")}, 429},
		{"upstream 502", errors.New("upstream error: status=502, body=bad gateway"), 502},
		{"no status", errors.New("some error"), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, UpstreamStatus(tt.err))
		})
	}
}

// mockChannelSelector implements ChannelSelector for testing.
type mockChannelSelector struct {
	channels []*Channel
	callIdx  int
}

func (m *mockChannelSelector) SelectChannel(_ context.Context, _, _ string, excludeFirst bool) (*Channel, error) {
	if m.callIdx >= len(m.channels) {
		return nil, errors.New("no channels available")
	}
	ch := m.channels[m.callIdx]
	m.callIdx++
	return ch, nil
}

func TestRetryExecutor_Execute_Success(t *testing.T) {
	selector := &mockChannelSelector{
		channels: []*Channel{{ID: 1, Name: "ch1"}},
	}
	exec := NewRetryExecutor(DefaultRetryPolicy(), selector)

	result := exec.Execute(context.Background(), "default", "gpt-4", func(_ context.Context, ch *Channel) error {
		assert.Equal(t, int64(1), ch.ID)
		return nil
	})

	assert.NoError(t, result.Err)
	assert.Equal(t, 0, result.Attempt)
}

func TestRetryExecutor_ExecuteWithInitialChannel_UsesPlannedChannelFirst(t *testing.T) {
	selector := &mockChannelSelector{}
	exec := NewRetryExecutor(DefaultRetryPolicy(), selector)

	result := exec.ExecuteWithInitialChannel(context.Background(), "default", "mimo-v2.5-pro", &Channel{ID: 9, Name: "planned"}, func(_ context.Context, ch *Channel) error {
		assert.Equal(t, int64(9), ch.ID)
		return nil
	})

	assert.NoError(t, result.Err)
	assert.Equal(t, 0, result.Attempt)
	assert.Equal(t, 0, selector.callIdx)
}

func TestRetryExecutor_Execute_RetryOnRetryableError(t *testing.T) {
	selector := &mockChannelSelector{
		channels: []*Channel{
			{ID: 1, Name: "ch1"},
			{ID: 2, Name: "ch2"},
		},
	}
	policy := &RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      1.0,
		RetryableStatus: map[int]bool{502: true},
	}
	exec := NewRetryExecutor(policy, selector)

	callCount := 0
	result := exec.Execute(context.Background(), "default", "gpt-4", func(_ context.Context, ch *Channel) error {
		callCount++
		if callCount == 1 {
			return &RetryableError{Status: 502, Err: errors.New("bad gateway")}
		}
		return nil
	})

	assert.NoError(t, result.Err)
	assert.Equal(t, 1, result.Attempt)
	assert.Equal(t, int64(2), result.Channel.ID) // second channel
}

func TestRetryExecutor_Execute_NonRetryableFailsImmediately(t *testing.T) {
	selector := &mockChannelSelector{
		channels: []*Channel{{ID: 1, Name: "ch1"}},
	}
	exec := NewRetryExecutor(DefaultRetryPolicy(), selector)

	result := exec.Execute(context.Background(), "default", "gpt-4", func(_ context.Context, ch *Channel) error {
		return &RetryableError{Status: 400, Err: errors.New("bad request")}
	})

	assert.Error(t, result.Err)
	assert.Equal(t, 0, result.Attempt)
}

func TestRetryExecutor_Execute_ExhaustsRetries(t *testing.T) {
	selector := &mockChannelSelector{
		channels: []*Channel{
			{ID: 1, Name: "ch1"},
			{ID: 2, Name: "ch2"},
			{ID: 3, Name: "ch3"},
		},
	}
	policy := &RetryPolicy{
		MaxAttempts:     3,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      1.0,
		RetryableStatus: map[int]bool{502: true},
	}
	exec := NewRetryExecutor(policy, selector)

	result := exec.Execute(context.Background(), "default", "gpt-4", func(_ context.Context, ch *Channel) error {
		return &RetryableError{Status: 502, Err: errors.New("bad gateway")}
	})

	assert.Error(t, result.Err)
	assert.Equal(t, 3, result.Attempt)
}
