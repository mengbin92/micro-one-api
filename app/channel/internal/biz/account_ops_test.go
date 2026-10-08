package biz

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type recoveryProbeFunc func(context.Context, *SubscriptionAccount) (bool, error)

func (f recoveryProbeFunc) ProbeRecovery(ctx context.Context, a *SubscriptionAccount) (bool, error) {
	return f(ctx, a)
}

type quotaResetFunc func(context.Context, *SubscriptionAccountQuotaResetRun) error

func (f quotaResetFunc) RecordQuotaResetAndReset(ctx context.Context, run *SubscriptionAccountQuotaResetRun) error {
	return f(ctx, run)
}

func TestQuotaResetSweeperResumesAfterTimedOutAccount(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	a := fixedAccount(1, "UTC", at.Add(-48*time.Hour).Unix(), 0)
	b := fixedAccount(2, "UTC", a.QuotaDailyWindowStart, 0)
	repo := newSweeperRepo(a, b)
	applier := quotaResetFunc(func(ctx context.Context, run *SubscriptionAccountQuotaResetRun) error {
		if run.AccountID == 1 {
			<-ctx.Done()
			return ctx.Err()
		}
		return repo.RecordQuotaResetAndReset(ctx, run)
	})
	s := NewQuotaResetSweeper(repo, applier, QuotaResetSweeperConfig{Enabled: true, Timeout: 10 * time.Millisecond})
	s.SetNow(func() time.Time { return at })
	require.ErrorIs(t, s.SweepOnce(context.Background()), context.DeadlineExceeded)
	require.NoError(t, s.SweepOnce(context.Background()))
	require.Equal(t, 1.5, a.QuotaDailyUsedUSD)
	require.Zero(t, b.QuotaDailyUsedUSD)
}

func TestRecoverySweeperRejectsOverlappingInvocation(t *testing.T) {
	a := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","unschedulable_reason":"429"}`)
	s := NewAccountRecoverySweeper(newSweeperRepo(a), AccountRecoverySweeperConfig{Enabled: true})
	s.SetNow(func() time.Time { return time.Unix(1000, 0) })
	entered, release := make(chan struct{}), make(chan struct{})
	s.SetProber(recoveryProbeFunc(func(ctx context.Context, a *SubscriptionAccount) (bool, error) {
		close(entered)
		<-release
		return true, nil
	}))
	done := make(chan error, 1)
	go func() { done <- s.SweepOnce(context.Background()) }()
	<-entered
	err := s.SweepOnce(context.Background())
	close(release)
	require.ErrorIs(t, err, ErrAccountSweepInProgress)
	require.NoError(t, <-done)
}

func TestRecoverySweeperRejectsStateChangedDuringProbe(t *testing.T) {
	acc := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","recovery_revision":"old","unschedulable_reason":"429"}`)
	repo := newSweeperRepo(acc)
	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true})
	s.SetNow(func() time.Time { return time.Unix(1000, 0) })
	s.SetProber(recoveryProbeFunc(func(ctx context.Context, scanned *SubscriptionAccount) (bool, error) {
		acc.Metadata = `{"recovery_policy":"auto","recovery_revision":"new","unschedulable_reason":"429"}`
		// Even a prober that mutates its input must not replace the captured CAS.
		scanned.Metadata = acc.Metadata
		return true, nil
	}))
	require.NoError(t, s.SweepOnce(context.Background()))
	require.EqualValues(t, 500, acc.RateLimitedUntil)
	require.Contains(t, acc.Metadata, `"new"`)
}

func TestAccountSweepersOnlyProcessTheirShard(t *testing.T) {
	at := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	var accounts []*SubscriptionAccount
	for id := int64(1); id <= 7; id++ {
		a := fixedAccount(id, "UTC", at.Add(-48*time.Hour).Unix(), 0)
		a.RateLimitedUntil = at.Add(-time.Minute).Unix()
		a.Metadata = `{"recovery_policy":"auto","unschedulable_reason":"429"}`
		accounts = append(accounts, a)
	}
	repo := newSweeperRepo(accounts...)
	shard := AccountScanShard{Count: 3, Index: 1}
	reset := NewQuotaResetSweeper(repo, repo, QuotaResetSweeperConfig{Enabled: true, PageSize: 1, Shard: shard})
	reset.SetNow(func() time.Time { return at })
	recovery := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 1, Shard: shard})
	recovery.SetNow(func() time.Time { return at })
	var probed []int64
	recovery.SetProber(recoveryProbeFunc(func(ctx context.Context, a *SubscriptionAccount) (bool, error) {
		probed = append(probed, a.ID)
		return true, nil
	}))
	require.NoError(t, reset.SweepOnce(context.Background()))
	require.NoError(t, recovery.SweepOnce(context.Background()))
	require.Equal(t, []int64{1, 4, 7}, probed)
	for _, a := range accounts {
		if a.ID%3 == 1 {
			require.Zero(t, a.QuotaDailyUsedUSD)
			require.Zero(t, a.RateLimitedUntil)
		} else {
			require.Equal(t, 1.5, a.QuotaDailyUsedUSD)
			require.NotZero(t, a.RateLimitedUntil)
		}
	}
}

func TestRecoverySweeperScanTimeoutCancelsProbe(t *testing.T) {
	a := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","unschedulable_reason":"429"}`)
	s := NewAccountRecoverySweeper(newSweeperRepo(a), AccountRecoverySweeperConfig{Enabled: true, Timeout: 10 * time.Millisecond})
	s.SetNow(func() time.Time { return time.Unix(1000, 0) })
	s.SetProber(recoveryProbeFunc(func(ctx context.Context, a *SubscriptionAccount) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}))
	require.ErrorIs(t, s.SweepOnce(context.Background()), context.DeadlineExceeded)
	require.NotEmpty(t, a.Metadata)
	require.EqualValues(t, 500, a.RateLimitedUntil)
}

func TestRecoverySweeperResumesAfterTimedOutAccount(t *testing.T) {
	a := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","unschedulable_reason":"429"}`)
	b := autoBlockedAccount(2, 500, a.Metadata)
	s := NewAccountRecoverySweeper(newSweeperRepo(a, b), AccountRecoverySweeperConfig{Enabled: true, Timeout: 10 * time.Millisecond})
	s.SetNow(func() time.Time { return time.Unix(1000, 0) })
	s.SetProber(recoveryProbeFunc(func(ctx context.Context, a *SubscriptionAccount) (bool, error) {
		if a.ID == 1 {
			<-ctx.Done()
			return false, ctx.Err()
		}
		return true, nil
	}))
	require.ErrorIs(t, s.SweepOnce(context.Background()), context.DeadlineExceeded)
	require.NoError(t, s.SweepOnce(context.Background()))
	require.Zero(t, b.RateLimitedUntil, "an earlier timed-out probe must not starve later accounts")
}

func TestRecoveryPolicyFailsClosed(t *testing.T) {
	for _, metadata := range []string{
		`{"recovery_policy":"future-policy","unschedulable_reason":"blocked"}`,
		`{"recovery_policy":42}`,
		`{"recovery_policy":"manual"`,
		`[]`,
	} {
		t.Run(metadata, func(t *testing.T) {
			a := autoBlockedAccount(1, 500, metadata)
			s := NewAccountRecoverySweeper(newSweeperRepo(a), AccountRecoverySweeperConfig{Enabled: true})
			s.SetNow(func() time.Time { return time.Unix(1000, 0) })
			require.NoError(t, s.SweepOnce(context.Background()))
			require.EqualValues(t, 500, a.RateLimitedUntil)
			require.Equal(t, metadata, a.Metadata)
		})
	}
}

// newSweeperRepo builds a mockChannelRepo seeded with the given accounts.
func newSweeperRepo(accounts ...*SubscriptionAccount) *mockChannelRepo {
	m := &mockChannelRepo{
		accounts:  make(map[int64]*SubscriptionAccount),
		resetRuns: make(map[string]bool),
	}
	for _, a := range accounts {
		m.accounts[a.ID] = a
	}
	return m
}

func fixedAccount(id int64, tz string, dailyStart, weeklyStart int64) *SubscriptionAccount {
	return &SubscriptionAccount{
		ID:                     id,
		Name:                   "fixed-" + strconv.FormatInt(id, 10),
		Status:                 ChannelStatusEnabled,
		Platform:               "codex",
		QuotaResetStrategy:     QuotaResetStrategyFixed,
		QuotaTimezone:          tz,
		QuotaDailyUsedUSD:      1.5,
		QuotaDailyWindowStart:  dailyStart,
		QuotaWeeklyUsedUSD:     9.0,
		QuotaWeeklyWindowStart: weeklyStart,
	}
}

func TestQuotaResetSweeper_FixedDailyBoundaryReset(t *testing.T) {
	// Asia/Shanghai 2026-07-05 00:00 local = 2026-07-04 16:00 UTC
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, loc)
	// window start from the previous day (2026-07-04 00:00 local)
	prevDay := time.Date(2026, 7, 4, 0, 0, 0, 0, loc).Unix()
	acc := fixedAccount(1, "Asia/Shanghai", prevDay, prevDay)
	repo := newSweeperRepo(acc)

	s := NewQuotaResetSweeper(repo, repo, QuotaResetSweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })

	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if acc.QuotaDailyUsedUSD != 0 {
		t.Fatalf("daily used = %v, want 0 after reset", acc.QuotaDailyUsedUSD)
	}
	wantDailyStart := time.Date(2026, 7, 5, 0, 0, 0, 0, loc).Unix()
	if acc.QuotaDailyWindowStart != wantDailyStart {
		t.Fatalf("daily window start = %d, want %d", acc.QuotaDailyWindowStart, wantDailyStart)
	}
	// weekly window is the same boundary (Sunday -> Monday), so it should also
	// reset when the previous window start predates this week's Monday.
	// 2026-07-05 is a Sunday; the Monday start is 2026-06-30. prevDay is after
	// that, so weekly should NOT reset. Verify weekly preserved.
	if acc.QuotaWeeklyUsedUSD == 0 {
		t.Fatalf("weekly used = 0, want preserved (window still within current week)")
	}
}

func TestQuotaResetSweeper_IdempotentRepeatedTick(t *testing.T) {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, loc)
	prevDay := time.Date(2026, 7, 4, 0, 0, 0, 0, loc).Unix()
	acc := fixedAccount(1, "Asia/Shanghai", prevDay, 0)
	// weekly start 0 means no weekly reset consideration (stored <= 0 skip)
	repo := newSweeperRepo(acc)

	s := NewQuotaResetSweeper(repo, repo, QuotaResetSweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })

	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("first SweepOnce() error = %v", err)
	}
	if acc.QuotaDailyUsedUSD != 0 {
		t.Fatalf("first sweep: daily used = %v, want 0", acc.QuotaDailyUsedUSD)
	}
	// Second tick: ResetSubscriptionAccountQuota in mock zeroes used again, but
	// RecordQuotaResetRun should return duplicate and skip the reset call. To
	// detect a double-reset, set used back to a nonzero value and verify the
	// second sweep does NOT zero it.
	acc.QuotaDailyUsedUSD = 2.0
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("second SweepOnce() error = %v", err)
	}
	if acc.QuotaDailyUsedUSD != 2.0 {
		t.Fatalf("second sweep: daily used = %v, want 2.0 (duplicate run must skip reset)", acc.QuotaDailyUsedUSD)
	}
}

func TestQuotaResetSweeper_SkipsRollingStrategy(t *testing.T) {
	now := time.Unix(1710000000, 0)
	acc := fixedAccount(1, "UTC", now.Add(-48*time.Hour).Unix(), 0)
	acc.QuotaResetStrategy = QuotaResetStrategyRolling
	repo := newSweeperRepo(acc)

	s := NewQuotaResetSweeper(repo, repo, QuotaResetSweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if acc.QuotaDailyUsedUSD == 0 {
		t.Fatalf("rolling account must not be reset by fixed sweeper")
	}
}

func TestQuotaResetSweeper_InvalidTimezoneFallsBackUTC(t *testing.T) {
	now := time.Date(2026, 7, 5, 1, 0, 0, 0, time.UTC)
	prevDay := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC).Unix()
	acc := fixedAccount(1, "Bogus/Zone", prevDay, 0)
	repo := newSweeperRepo(acc)

	s := NewQuotaResetSweeper(repo, repo, QuotaResetSweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	// Should fall back to UTC and still reset the daily window.
	if acc.QuotaDailyUsedUSD != 0 {
		t.Fatalf("daily used = %v, want 0 (invalid tz fallback to UTC)", acc.QuotaDailyUsedUSD)
	}
}

// --- Recovery sweeper tests ---

func autoBlockedAccount(id int64, until int64, metadata string) *SubscriptionAccount {
	return &SubscriptionAccount{
		ID:               id,
		Status:           ChannelStatusEnabled,
		Platform:         "codex",
		RateLimitedUntil: until,
		Metadata:         metadata,
	}
}

func TestAccountRecoverySweeper_AutoRecoverAfterTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	acc := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","unschedulable_reason":"upstream 429"}`)
	repo := newSweeperRepo(acc)

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if acc.RateLimitedUntil != 0 {
		t.Fatalf("RateLimitedUntil = %v, want 0 (cleared)", acc.RateLimitedUntil)
	}
	if acc.Metadata != "" {
		t.Fatalf("Metadata = %q, want empty (recovery markers cleared)", acc.Metadata)
	}
}

func TestAccountRecoverySweeper_AutoWaitingBeforeTTL(t *testing.T) {
	now := time.Unix(1000, 0)
	acc := autoBlockedAccount(1, 2000, `{"recovery_policy":"auto","unschedulable_reason":"upstream 429"}`)
	repo := newSweeperRepo(acc)

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if acc.RateLimitedUntil == 0 {
		t.Fatalf("RateLimitedUntil cleared before TTL elapsed; must wait")
	}
}

func TestAccountRecoverySweeper_ManualNeverAutoRecovered(t *testing.T) {
	now := time.Unix(1000, 0)
	// 401 -> manual policy; TTL is in the past but must NOT auto-recover.
	acc := autoBlockedAccount(1, 500, `{"recovery_policy":"manual","unschedulable_reason":"upstream 401"}`)
	repo := newSweeperRepo(acc)

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if acc.RateLimitedUntil == 0 {
		t.Fatalf("manual account was auto-recovered; must require OAuth rebind")
	}
	if acc.Metadata == "" {
		t.Fatalf("manual account metadata was cleared; must preserve markers")
	}
}

func TestAccountRecoverySweeper_QuotaWaitsForWindowReset(t *testing.T) {
	// Use a realistic timestamp so window-start stays positive (the data layer
	// treats windowStart <= 0 as "no window yet").
	now := time.Unix(1710000000, 0)
	acc := &SubscriptionAccount{
		ID:                    1,
		Status:                ChannelStatusEnabled,
		Platform:              "codex",
		QuotaDailyLimitUSD:    1,
		QuotaDailyUsedUSD:     1,
		QuotaDailyWindowStart: now.Add(-time.Hour).Unix(),
		Metadata:              `{"recovery_policy":"quota","unschedulable_reason":"local quota exhausted"}`,
	}
	repo := newSweeperRepo(acc)

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	// Still over quota -> must not clear markers.
	if acc.Metadata == "" {
		t.Fatalf("quota account cleared while still over quota; must wait for window reset")
	}
}

func TestAccountRecoverySweeper_CodexWaitsForSnapshotReset(t *testing.T) {
	now := time.Unix(1710000000, 0)
	used := float64(100)
	acc := &SubscriptionAccount{
		ID:                      1,
		Status:                  ChannelStatusEnabled,
		Platform:                "codex",
		PrimaryQuotaUsedPercent: &used,
		Metadata:                `{"recovery_policy":"codex","unschedulable_reason":"codex quota exhausted"}`,
	}
	repo := newSweeperRepo(acc)

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if acc.Metadata == "" {
		t.Fatalf("codex account cleared while snapshot is still exhausted; must wait for snapshot reset")
	}
}

func TestAccountRecoverySweeper_ClearedWhenAlreadySchedulable(t *testing.T) {
	now := time.Unix(1000, 0)
	acc := autoBlockedAccount(1, 0, `{"recovery_policy":"auto","unschedulable_reason":"upstream 429"}`)
	repo := newSweeperRepo(acc)

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if acc.Metadata != "" {
		t.Fatalf("Metadata = %q, want empty (stale markers cleared for schedulable account)", acc.Metadata)
	}
}

func TestRecoveryPolicyForStatus(t *testing.T) {
	cases := []struct {
		status int
		want   string
	}{
		{401, RecoveryPolicyManual},
		{403, RecoveryPolicyManual},
		{429, RecoveryPolicyAuto},
		{500, RecoveryPolicyAuto},
		{502, RecoveryPolicyAuto},
		{529, RecoveryPolicyAuto},
		{404, RecoveryPolicyRolling},
	}
	for _, c := range cases {
		if got := recoveryPolicyForStatus(c.status); got != c.want {
			t.Errorf("recoveryPolicyForStatus(%d) = %q, want %q", c.status, got, c.want)
		}
	}
}

func TestSubscriptionAccount_RecoveryInfo(t *testing.T) {
	now := time.Unix(1000, 0)
	// Schedulable account -> clean info.
	a := &SubscriptionAccount{Status: ChannelStatusEnabled}
	if info := a.RecoveryInfo(now); info.Reason != "" || info.Policy == "" {
		t.Fatalf("schedulable account info = %+v, want clean", info)
	}
	// Auto-blocked account with stamped metadata.
	a = &SubscriptionAccount{
		Status:           ChannelStatusEnabled,
		RateLimitedUntil: 2000,
		Metadata:         `{"recovery_policy":"auto","unschedulable_reason":"upstream 429","unschedulable_since":500}`,
	}
	info := a.RecoveryInfo(now)
	if info.Policy != RecoveryPolicyAuto {
		t.Fatalf("policy = %q, want auto", info.Policy)
	}
	if info.Reason != "upstream 429" {
		t.Fatalf("reason = %q, want 'upstream 429'", info.Reason)
	}
	if info.ExpectedRecoveryAt != 2000 {
		t.Fatalf("expected recovery = %d, want 2000", info.ExpectedRecoveryAt)
	}
}

func TestErrQuotaResetRunDuplicate_IsDistinct(t *testing.T) {
	if !errors.Is(ErrQuotaResetRunDuplicate, ErrQuotaResetRunDuplicate) {
		t.Fatalf("ErrQuotaResetRunDuplicate must match itself")
	}
	if errors.Is(ErrQuotaResetRunDuplicate, ErrSubscriptionAccountNotFound) {
		t.Fatalf("ErrQuotaResetRunDuplicate must not collide with ErrSubscriptionAccountNotFound")
	}
}

// fakeRecoveryProber is a test double for biz.RecoveryProber.
type fakeRecoveryProber struct {
	ok       bool
	err      error
	probed   int
	platform string // last probed platform
}

func (p *fakeRecoveryProber) ProbeRecovery(ctx context.Context, account *SubscriptionAccount) (bool, error) {
	p.probed++
	if account != nil {
		p.platform = account.Platform
	}
	return p.ok, p.err
}

// TestAccountRecoverySweeper_ProbeConfirmedRecovers verifies that when a
// recovery probe is configured and returns ok=true, an auto-policy account
// past its TTL is recovered via the probe-confirmed path (roadmap §1.2).
func TestAccountRecoverySweeper_ProbeConfirmedRecovers(t *testing.T) {
	now := time.Unix(1000, 0)
	acc := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","unschedulable_reason":"upstream 429"}`)
	acc.Platform = "codex"
	repo := newSweeperRepo(acc)
	probe := &fakeRecoveryProber{ok: true}

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	s.SetProber(probe)
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if probe.probed != 1 {
		t.Fatalf("probe called %d times, want 1", probe.probed)
	}
	if acc.RateLimitedUntil != 0 {
		t.Fatalf("RateLimitedUntil = %v, want 0 (probe-confirmed recovery)", acc.RateLimitedUntil)
	}
}

// TestAccountRecoverySweeper_ProbeNegativeHoldsRecovery verifies that when the
// probe returns ok=false (upstream still failing), the account is NOT recovered
// even though its local TTL elapsed (roadmap §1.2).
func TestAccountRecoverySweeper_ProbeNegativeHoldsRecovery(t *testing.T) {
	now := time.Unix(1000, 0)
	acc := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","unschedulable_reason":"upstream 429"}`)
	acc.Platform = "codex"
	repo := newSweeperRepo(acc)
	probe := &fakeRecoveryProber{ok: false}

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	s.SetProber(probe)
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if probe.probed != 1 {
		t.Fatalf("probe called %d times, want 1", probe.probed)
	}
	if acc.RateLimitedUntil == 0 {
		t.Fatalf("RateLimitedUntil cleared despite negative probe; account still failing upstream must stay blocked")
	}
}

// TestAccountRecoverySweeper_ProbeUnavailableFallsBackToLocal verifies that
// when the probe returns an error (e.g. unsupported platform), the sweeper falls
// back to its local-state recovery path instead of stranding the account
// (roadmap §1.2: probe only applies to safely-probeable platforms).
func TestAccountRecoverySweeper_ProbeUnavailableFallsBackToLocal(t *testing.T) {
	now := time.Unix(1000, 0)
	acc := autoBlockedAccount(1, 500, `{"recovery_policy":"auto","unschedulable_reason":"upstream 429"}`)
	acc.Platform = "claude" // not probeable -> adapter returns error
	repo := newSweeperRepo(acc)
	probe := &fakeRecoveryProber{err: errors.New("platform claude is not probeable")}

	s := NewAccountRecoverySweeper(repo, AccountRecoverySweeperConfig{Enabled: true, PageSize: 10})
	s.SetNow(func() time.Time { return now })
	s.SetProber(probe)
	if err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce() error = %v", err)
	}
	if probe.probed != 1 {
		t.Fatalf("probe called %d times, want 1", probe.probed)
	}
	// Fallback: local recovery path clears the markers.
	if acc.RateLimitedUntil != 0 {
		t.Fatalf("RateLimitedUntil = %v, want 0 (fallback local recovery)", acc.RateLimitedUntil)
	}
}
