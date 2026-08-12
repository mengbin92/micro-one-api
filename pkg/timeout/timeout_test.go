package timeout

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetTimeout_Unset_ReturnsDefault(t *testing.T) {
	t.Setenv("MOA_TIMEOUT_UNSET", "")
	assert.Equal(t, 42*time.Second, GetTimeout("MOA_TIMEOUT_UNSET", 42*time.Second, 0))
}

func TestGetTimeout_Invalid_ReturnsDefault(t *testing.T) {
	t.Setenv("MOA_TIMEOUT_INVALID", "not-a-duration")
	assert.Equal(t, 5*time.Second, GetTimeout("MOA_TIMEOUT_INVALID", 5*time.Second, 0))
}

func TestGetTimeout_Valid_ReturnsValue(t *testing.T) {
	t.Setenv("MOA_TIMEOUT_VALID", "3s")
	assert.Equal(t, 3*time.Second, GetTimeout("MOA_TIMEOUT_VALID", 5*time.Second, 0))
}

func TestGetTimeout_OverMax_Clamps(t *testing.T) {
	t.Setenv("MOA_TIMEOUT_CLAMP", "10m")
	assert.Equal(t, MaxHTTPTimeout, GetTimeout("MOA_TIMEOUT_CLAMP", 30*time.Second, MaxHTTPTimeout))
}

func TestGetTimeout_ZeroMax_NoClamp(t *testing.T) {
	t.Setenv("MOA_TIMEOUT_NOCLAMP", "15m")
	assert.Equal(t, 15*time.Minute, GetTimeout("MOA_TIMEOUT_NOCLAMP", 30*time.Second, 0),
		"maxValue <= 0 must disable clamping")
}

func TestGetHTTPTimeout_EnvWins(t *testing.T) {
	t.Setenv("HTTP_TIMEOUT", "90s")
	assert.Equal(t, 90*time.Second, GetHTTPTimeout())
}

func TestGetHTTPTimeout_Default(t *testing.T) {
	t.Setenv("HTTP_TIMEOUT", "")
	assert.Equal(t, DefaultHTTPTimeout, GetHTTPTimeout())
}

func TestGetGRPCTimeout_Default(t *testing.T) {
	t.Setenv("GRPC_TIMEOUT", "")
	assert.Equal(t, DefaultGRPCTimeout, GetGRPCTimeout())
}

func TestGetDBQueryTimeout_Default(t *testing.T) {
	t.Setenv("DB_QUERY_TIMEOUT", "")
	assert.Equal(t, DefaultDBQueryTimeout, GetDBQueryTimeout())
}

func TestGetUpstreamTimeout_Default(t *testing.T) {
	t.Setenv("UPSTREAM_TIMEOUT", "")
	assert.Equal(t, DefaultUpstreamTimeout, GetUpstreamTimeout())
}

func TestWithTimeout_NoParentDeadline_UsesFullTimeout(t *testing.T) {
	ctx, cancel := WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	deadline, ok := ctx.Deadline()
	require.True(t, ok, "a fresh WithTimeout must install a deadline")
	remaining := time.Until(deadline)
	assert.Greater(t, remaining, 25*time.Second, "deadline should be ~30s out")
	assert.LessOrEqual(t, remaining, 30*time.Second)
}

func TestWithTimeout_ParentHasEarlierDeadline_Shrinks(t *testing.T) {
	parent, pcancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer pcancel()

	ctx, cancel := WithTimeout(parent, 30*time.Second)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	remaining := time.Until(deadline)
	assert.Less(t, remaining, 30*time.Second, "child timeout must not exceed the parent's remaining time")
	assert.Greater(t, remaining, 0*time.Second)
}

func TestWithTimeout_ParentAlreadyExpired_ImmediateDeadline(t *testing.T) {
	parent, pcancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer pcancel()

	ctx, cancel := WithTimeout(parent, 30*time.Second)
	defer cancel()
	assert.Error(t, ctx.Err(), "child of an expired context must itself be expired")
}

func TestWithTimeout_ParentHasLaterDeadline_KeepsOwnTimeout(t *testing.T) {
	parent, pcancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer pcancel()

	ctx, cancel := WithTimeout(parent, 30*time.Second)
	defer cancel()

	deadline, ok := ctx.Deadline()
	require.True(t, ok)
	remaining := time.Until(deadline)
	assert.LessOrEqual(t, remaining, 30*time.Second, "child uses its own timeout when it is tighter")
}

func TestWithTimeout_ZeroTimeout_ExpiresImmediately(t *testing.T) {
	ctx, cancel := WithTimeout(context.Background(), 0)
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("zero timeout must cancel immediately")
	}
}

func TestParseTimeout_Empty_Default(t *testing.T) {
	assert.Equal(t, 7*time.Second, ParseTimeout("", 7*time.Second))
}

func TestParseTimeout_Invalid_Default(t *testing.T) {
	assert.Equal(t, 7*time.Second, ParseTimeout("abc", 7*time.Second))
}

func TestParseTimeout_Negative_PassesThrough(t *testing.T) {
	// time.ParseDuration accepts negative durations, so a "-5s" value is a
	// valid parse and flows through unchanged (context.WithTimeout treats it
	// as an immediately-expiring deadline). This documents current behaviour;
	// callers needing a floor should use ValidateTimeout.
	assert.Equal(t, -5*time.Second, ParseTimeout("-5s", 7*time.Second))
}

func TestParseTimeout_Valid(t *testing.T) {
	assert.Equal(t, 2500*time.Millisecond, ParseTimeout("2.5s", 7*time.Second))
}

func TestParseIntTimeout_Empty_Default(t *testing.T) {
	assert.Equal(t, 9*time.Second, ParseIntTimeout("", 9*time.Second))
}

func TestParseIntTimeout_Invalid_Default(t *testing.T) {
	assert.Equal(t, 9*time.Second, ParseIntTimeout("x", 9*time.Second))
	assert.Equal(t, 9*time.Second, ParseIntTimeout("0", 9*time.Second))
	assert.Equal(t, 9*time.Second, ParseIntTimeout("-3", 9*time.Second))
}

func TestParseIntTimeout_Valid(t *testing.T) {
	assert.Equal(t, 12*time.Second, ParseIntTimeout("12", 9*time.Second))
}

func TestValidateTimeout_WithinBounds_NoError(t *testing.T) {
	assert.NoError(t, ValidateTimeout(5*time.Second, 1*time.Second, 10*time.Second))
}

func TestValidateTimeout_BelowMin_Error(t *testing.T) {
	err := ValidateTimeout(500*time.Millisecond, 1*time.Second, 10*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "less than minimum")
}

func TestValidateTimeout_AboveMax_Error(t *testing.T) {
	err := ValidateTimeout(20*time.Second, 1*time.Second, 10*time.Second)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
}

func TestValidateTimeout_ZeroBounds_NoValidation(t *testing.T) {
	assert.NoError(t, ValidateTimeout(1*time.Hour, 0, 0))
	assert.NoError(t, ValidateTimeout(-1*time.Second, 0, 0))
}
