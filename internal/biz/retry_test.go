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
	channels     []*Channel
	callIdx      int
	healthEvents []healthEvent
}

type healthEvent struct {
	channelID    int64
	success      bool
	err          string
	responseTime int64
}

func (m *mockChannelSelector) SelectChannel(_ context.Context, _, _ string, excludeFirst bool) (*Channel, error) {
	if m.callIdx >= len(m.channels) {
		return nil, errors.New("no channels available")
	}
	ch := m.channels[m.callIdx]
	m.callIdx++
	return ch, nil
}

func (m *mockChannelSelector) RecordSubscriptionAccountHealth(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (m *mockChannelSelector) RecordChannelHealth(_ context.Context, channelID int64, success bool, err string, responseTime int64) error {
	m.healthEvents = append(m.healthEvents, healthEvent{
		channelID:    channelID,
		success:      success,
		err:          err,
		responseTime: responseTime,
	})
	return nil
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
	if len(selector.healthEvents) != 2 {
		t.Fatalf("health events = %d, want 2", len(selector.healthEvents))
	}
	assert.False(t, selector.healthEvents[0].success)
	assert.Equal(t, int64(1), selector.healthEvents[0].channelID)
	assert.True(t, selector.healthEvents[1].success)
	assert.Equal(t, int64(2), selector.healthEvents[1].channelID)
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

// TestRetryExecutor_FailoverDoesNotWidenToCatchAll verifies that a missing
// lower-priority alternative retries the current channel without calling
// SelectChannel(false), which could silently expand failover to catch-all.
func TestRetryExecutor_FailoverDoesNotWidenToCatchAll(t *testing.T) {
	sel := newTrackingSelector()
	sel.failoverErr = errors.New("no channel")
	sel.first = &Channel{ID: 1, Name: "catchall"}
	policy := &RetryPolicy{
		MaxAttempts:     2,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      1.0,
		RetryableStatus: map[int]bool{502: true},
	}
	exec := NewRetryExecutor(policy, sel)

	attempts := 0
	result := exec.ExecuteWithInitialChannel(context.Background(), "default", "gpt-4", sel.first, func(_ context.Context, ch *Channel) error {
		attempts++
		return &RetryableError{Status: 502, Err: errors.New("bad gateway")}
	})
	// Must NOT have widened: excludeFirstPriority=false should never be called.
	if sel.falseCalls > 0 {
		t.Fatalf("failover widened to SelectChannel(false) %d times — contract violated", sel.falseCalls)
	}
	if result.Err == nil {
		t.Fatal("expected the upstream error to surface, got nil")
	}
	if attempts != policy.MaxAttempts {
		t.Fatalf("same-channel attempts = %d, want %d", attempts, policy.MaxAttempts)
	}
}

type trackingSelector struct {
	first        *Channel
	failoverErr  error
	trueCalls    int
	falseCalls   int
	healthEvents []healthEvent
}

func newTrackingSelector() *trackingSelector { return &trackingSelector{} }

func (m *trackingSelector) SelectChannel(_ context.Context, _, _ string, excludeFirst bool) (*Channel, error) {
	if excludeFirst {
		m.trueCalls++
		// No alternative tier available — mirrors SelectChannel returning
		// ErrChannelNotFound on the failover path.
		return nil, m.failoverErr
	}
	m.falseCalls++
	return m.first, nil
}

func (m *trackingSelector) RecordSubscriptionAccountHealth(_ context.Context, _ int64, _ bool) error {
	return nil
}

func (m *trackingSelector) RecordChannelHealth(_ context.Context, channelID int64, success bool, err string, responseTime int64) error {
	m.healthEvents = append(m.healthEvents, healthEvent{channelID, success, err, responseTime})
	return nil
}

// TestRetryExecutor_ExecuteWithAccountHealth_FeedsSelector (🟡#8): the P2
// #7 selector's RecordAccountHealth must be called after every attempt so
// healthFactor/circuit-breaker track live relay results (previously inert).
type recordingAccountSelector struct {
	mockChannelSelector
	accountHealthCalls []accountHealthCall
}

type accountHealthCall struct {
	id      int64
	success bool
}

func (m *recordingAccountSelector) RecordSubscriptionAccountHealth(_ context.Context, accountID int64, success bool) error {
	m.accountHealthCalls = append(m.accountHealthCalls, accountHealthCall{accountID, success})
	return nil
}

func TestRetryExecutor_ExecuteWithAccountHealth_FeedsSelector(t *testing.T) {
	sel := &recordingAccountSelector{
		mockChannelSelector: mockChannelSelector{
			channels: []*Channel{{ID: 1, Name: "ch1"}},
		},
	}
	policy := &RetryPolicy{
		MaxAttempts:     1,
		InitialInterval: 1 * time.Millisecond,
		MaxInterval:     5 * time.Millisecond,
		Multiplier:      1.0,
		RetryableStatus: map[int]bool{502: true},
	}
	exec := NewRetryExecutor(policy, sel)

	_ = exec.ExecuteWithAccountHealth(context.Background(), "default", "gpt-4", &Channel{ID: 1}, 42, func(_ context.Context, _ *Channel) error {
		return nil // success
	})
	if len(sel.accountHealthCalls) != 1 {
		t.Fatalf("expected 1 account-health call, got %d", len(sel.accountHealthCalls))
	}
	if sel.accountHealthCalls[0].id != 42 || !sel.accountHealthCalls[0].success {
		t.Fatalf("expected (42,true), got %+v", sel.accountHealthCalls[0])
	}

	// accountID<=0 degrades to plain execute — no account-health recording.
	sel.accountHealthCalls = nil
	_ = exec.ExecuteWithAccountHealth(context.Background(), "default", "gpt-4", &Channel{ID: 1}, 0, func(_ context.Context, _ *Channel) error {
		return nil
	})
	if len(sel.accountHealthCalls) != 0 {
		t.Fatalf("accountID<=0 must not record account health, got %d calls", len(sel.accountHealthCalls))
	}
}

func TestRetryExecutor_AccountHealthIsNotAttributedToFallbackChannel(t *testing.T) {
	initial := &Channel{ID: 42, Name: "subscription-account"}
	fallback := &Channel{ID: 7, Name: "api-key-fallback"}
	sel := &recordingAccountSelector{
		mockChannelSelector: mockChannelSelector{channels: []*Channel{fallback}},
	}
	exec := NewRetryExecutor(&RetryPolicy{
		MaxAttempts:     2,
		InitialInterval: time.Nanosecond,
		MaxInterval:     time.Nanosecond,
		Multiplier:      1,
		RetryableStatus: map[int]bool{502: true},
	}, sel)

	result := exec.ExecuteWithAccountHealth(context.Background(), "default", "gpt-4", initial, 42, func(_ context.Context, ch *Channel) error {
		if ch == initial {
			return &RetryableError{Status: 502, Err: errors.New("initial account failed")}
		}
		return nil
	})
	if result.Err != nil || result.Channel != fallback {
		t.Fatalf("retry result = %+v, want successful fallback", result)
	}
	if len(sel.accountHealthCalls) != 1 || sel.accountHealthCalls[0].id != 42 || sel.accountHealthCalls[0].success {
		t.Fatalf("account health calls = %+v, want only (42,false) for the known initial account", sel.accountHealthCalls)
	}
}
