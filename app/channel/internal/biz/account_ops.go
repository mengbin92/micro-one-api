package biz

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"micro-one-api/pkg/jsonx"

	"micro-one-api/platform/metrics"
)

// Recovery policy classifications for unschedulable subscription accounts.
//
// The recovery sweeper uses these to decide whether an account may be
// auto-recovered: temporary upstream errors (429/5xx/529) auto-recover once
// their TTL expires; authorization errors (401/403) never auto-recover and
// require OAuth rebind or manual confirmation; local quota exhaustion waits
// for a window reset or manual reset; codex snapshot exhaustion waits for the
// upstream snapshot to reset.
const (
	RecoveryPolicyAuto    = "auto"    // temporary upstream error: auto-clear once TTL elapses
	RecoveryPolicyManual  = "manual"  // authorization error: never auto-recover
	RecoveryPolicyQuota   = "quota"   // local quota exhausted: wait for window reset
	RecoveryPolicyCodex   = "codex"   // codex snapshot exhausted: wait for snapshot reset
	RecoveryPolicyRolling = "rolling" // default when no marker is set
)

// metadata keys persisted on subscription_accounts.metadata (JSON).
const (
	metaKeyLastError           = "last_error"
	metaKeyRecoveryPolicy      = "recovery_policy"
	metaKeyRecoveryRevision    = "recovery_revision"
	metaKeyUnschedulableReason = "unschedulable_reason"
	metaKeyUnschedulableSince  = "unschedulable_since"
	metaKeyUnschedulableUntil  = "unschedulable_until"
	metaKeyExpectedRecoveryAt  = "expected_recovery_at"
	metaKeyLastQuotaAlertAt    = "last_quota_alert_at"
	metaKeyLastQuotaAlertKind  = "last_quota_alert_kind"
)

// AccountRecoveryState is the optimistic-concurrency token captured before
// probing. Metadata includes a fresh recovery_revision for every new incident,
// including identical failures in the same second. Legacy markers without a
// revision are compared in full; a new incident always introduces a revision.
type AccountRecoveryState struct {
	AccountID          int64
	CredentialRevision int64
	Status             int32
	RateLimitedUntil   int64
	Metadata           string
	LastError          string
}

func (a *SubscriptionAccount) RecoveryState() AccountRecoveryState {
	return AccountRecoveryState{
		AccountID: a.ID, CredentialRevision: a.CredentialRevision,
		Status: a.Status, RateLimitedUntil: a.RateLimitedUntil,
		Metadata: a.Metadata, LastError: a.LastError,
	}
}

// CanAutoRecoverAt is rechecked under the repository's write lock. An upstream
// probe cannot authorize clearing a newer incident or exhausted local quota.
func (a *SubscriptionAccount) CanAutoRecoverAt(now time.Time) bool {
	if !a.IsSchedulableAt(now) {
		return false
	}
	// Missing policy is the legacy rolling case. Malformed metadata or an
	// unrecognised policy cannot grant permission to recover an account.
	var metadata map[string]any
	if strings.TrimSpace(a.Metadata) != "" {
		if err := jsonx.Unmarshal([]byte(a.Metadata), &metadata); err != nil || metadata == nil {
			return false
		}
	}
	policy := RecoveryPolicyRolling
	if value, present := metadata[metaKeyRecoveryPolicy]; present {
		var ok bool
		policy, ok = value.(string)
		if !ok {
			return false
		}
	}
	switch policy {
	case "", RecoveryPolicyAuto, RecoveryPolicyRolling, RecoveryPolicyQuota, RecoveryPolicyCodex:
		return !a.CodexSnapshotQuotaExceeded()
	default:
		return false
	}
}

// AccountScanShard assigns each positive account ID to exactly one shard.
// The zero value retains the single-worker behavior.
type AccountScanShard struct {
	Index int64
	Count int64
}

func (s AccountScanShard) Validate() error {
	if s.Count == 0 && s.Index == 0 {
		return nil
	}
	if s.Count < 1 || s.Index < 0 || s.Index >= s.Count {
		return fmt.Errorf("account scan shard requires count >= 1 and 0 <= index < count")
	}
	return nil
}

func (s AccountScanShard) EffectiveCount() int64 {
	if s.Count == 0 {
		return 1
	}
	return s.Count
}

type AccountScan struct {
	Shard     AccountScanShard
	AfterID   int64
	Limit     int32
	Status    int32
	FixedOnly bool
}

// recoveryPolicyFromStatus maps an upstream HTTP status code to the recovery
// policy that should be stamped on the account when it is marked unschedulable.
// 401/403 -> manual; 429/5xx/529 -> auto; everything else -> rolling (the
// caller stamps a more specific policy when the cause is local quota or codex
// snapshot exhaustion).
func recoveryPolicyForStatus(statusCode int) string {
	switch {
	case statusCode == 401 || statusCode == 403:
		return RecoveryPolicyManual
	case statusCode == 429 || statusCode == 529 || statusCode >= 500:
		return RecoveryPolicyAuto
	default:
		return RecoveryPolicyRolling
	}
}

// QuotaResetSweeperConfig configures the fixed-strategy quota reset sweeper.
type QuotaResetSweeperConfig struct {
	Enabled  bool
	Interval time.Duration
	PageSize int32
	Timeout  time.Duration
	Shard    AccountScanShard
}

// QuotaResetSweeper periodically scans subscription accounts using the fixed
// quota-reset strategy and, when the natural-day or natural-week boundary has
// rolled past the stored window start, resets the corresponding daily/weekly
// usage counters to zero.
//
// Idempotency: a reset is only applied when the stored window_start is older
// than the current fixed window start. After the reset the window_start is
// advanced to the current fixed window start, so a repeated worker tick within
// the same boundary observes no drift and performs no work. A durable record
// is written to subscription_account_quota_reset_runs whose (account, scope,
// window_start) unique key prevents duplicate resets even across replicas.
type QuotaResetSweeper struct {
	repo    ChannelRepo
	now     func() time.Time
	cfg     QuotaResetSweeperConfig
	applier QuotaResetRunApplier
	sweepMu sync.Mutex
	afterID int64
}

// QuotaResetRunApplier records the reset run and updates usage atomically.
type QuotaResetRunApplier interface {
	RecordQuotaResetAndReset(ctx context.Context, run *SubscriptionAccountQuotaResetRun) error
}

// RecoveryProber performs a lightweight upstream probe to verify an account is
// actually serving again before the recovery sweeper clears its unschedulable
// markers (roadmap §1.2 "增加恢复前探测策略, 只对可安全探测的平台执行轻量
// 请求"). Implementations MUST be safe to leave nil (no probe available): the
// sweeper then falls back to its existing local-state check. Only platforms the
// prober explicitly supports are probed; others recover via the local path.
type RecoveryProber interface {
	// ProbeRecovery performs a lightweight upstream check for the account. It
	// returns ok=true when the upstream confirms the account is healthy
	// (e.g. a 1-token request succeeds), ok=false when the upstream still
	// rejects it, and err!=nil when the probe could not be run (network,
	// unsupported platform). A nil prober means "no probe configured" and the
	// sweeper treats every account as probe-eligible via its local checks.
	ProbeRecovery(ctx context.Context, account *SubscriptionAccount) (ok bool, err error)
}

// SubscriptionAccountQuotaResetRun is the audit row for an automated reset.
type SubscriptionAccountQuotaResetRun struct {
	AccountID   int64
	Scope       string
	WindowStart int64
	Strategy    string
	Timezone    string
	ResetAt     time.Time
}

// ErrQuotaResetRunDuplicate is returned by RecordQuotaResetRun when the run has
// already been recorded (duplicate worker tick within the same window).
var ErrQuotaResetRunDuplicate = fmt.Errorf("quota reset run already recorded")

var ErrQuotaResetRunStale = fmt.Errorf("quota reset configuration changed")

var ErrAccountSweepInProgress = fmt.Errorf("account sweep already running")

var ErrAccountRecoveryStateChanged = fmt.Errorf("subscription account recovery state changed; reload and retry")

// MatchesAccount prevents a scanned reset from overwriting usage after a
// timezone or strategy change has established a different current window.
func (r *SubscriptionAccountQuotaResetRun) MatchesAccount(a *SubscriptionAccount) bool {
	return a != nil && a.UsesFixedQuotaReset() && r.Strategy == a.EffectiveQuotaResetStrategy() &&
		r.Timezone == a.EffectiveQuotaTimezone() && r.WindowStart == a.FixedQuotaWindowStart(r.ResetAt, r.Scope)
}

// NewQuotaResetSweeper builds a fixed-strategy quota reset sweeper.
func NewQuotaResetSweeper(repo ChannelRepo, applier QuotaResetRunApplier, cfg QuotaResetSweeperConfig) *QuotaResetSweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 200
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &QuotaResetSweeper{repo: repo, now: time.Now, cfg: cfg, applier: applier}
}

// SetNow overrides the clock (tests).
func (s *QuotaResetSweeper) SetNow(f func() time.Time) { s.now = f }

// Run loops until ctx is cancelled, executing SweepOnce every Interval.
func (s *QuotaResetSweeper) Run(ctx context.Context) {
	if s == nil || !s.cfg.Enabled {
		return
	}
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.SweepOnce(ctx)
		}
	}
}

// SweepOnce performs a single scan of fixed-strategy accounts and resets any
// daily/weekly window that has crossed its natural boundary.
func (s *QuotaResetSweeper) SweepOnce(ctx context.Context) error {
	if !s.sweepMu.TryLock() {
		return ErrAccountSweepInProgress
	}
	defer s.sweepMu.Unlock()
	if err := s.cfg.Shard.Validate(); err != nil {
		return err
	}
	if s.applier == nil {
		return fmt.Errorf("atomic quota reset applier is required")
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	startedAt := time.Now()
	defer func() {
		metrics.SubscriptionAccountQuotaResetScanDuration.Observe(time.Since(startedAt).Seconds())
	}()
	now := s.now()
	for {
		accounts, err := s.repo.ScanSubscriptionAccounts(ctx, AccountScan{Shard: s.cfg.Shard, AfterID: s.afterID, Limit: s.cfg.PageSize, FixedOnly: true})
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if err := ctx.Err(); err != nil {
				return err
			}
			if account != nil {
				// Resume after attempted accounts on the next tick. Failed
				// accounts are retried after the cursor completes a full pass.
				s.afterID = account.ID
			}
			if account == nil || !account.UsesFixedQuotaReset() {
				continue
			}
			if err := s.resetIfCrossedBoundary(ctx, account, now, "daily"); err != nil {
				return err
			}
			if err := s.resetIfCrossedBoundary(ctx, account, now, "weekly"); err != nil {
				return err
			}
		}
		if len(accounts) < int(s.cfg.PageSize) {
			s.afterID = 0
			return nil
		}
	}
}

// resetIfCrossedBoundary resets a single scope for an account when the stored
// window_start is older than the current fixed window start. The repository
// atomically records the run and conditionally advances the stored window.
func (s *QuotaResetSweeper) resetIfCrossedBoundary(ctx context.Context, account *SubscriptionAccount, now time.Time, scope string) error {
	fixedStart := account.FixedQuotaWindowStart(now, scope)
	var storedStart int64
	switch scope {
	case "daily":
		storedStart = account.QuotaDailyWindowStart
	case "weekly":
		storedStart = account.QuotaWeeklyWindowStart
	default:
		return nil
	}
	// No usage yet, or already aligned to the current fixed window: nothing to do.
	if storedStart <= 0 || storedStart >= fixedStart {
		return nil
	}
	run := &SubscriptionAccountQuotaResetRun{
		AccountID:   account.ID,
		Scope:       scope,
		WindowStart: fixedStart,
		Strategy:    account.EffectiveQuotaResetStrategy(),
		Timezone:    account.EffectiveQuotaTimezone(),
		ResetAt:     now,
	}
	if err := s.applier.RecordQuotaResetAndReset(ctx, run); err != nil {
		if errors.Is(err, ErrQuotaResetRunStale) {
			metrics.SubscriptionAccountQuotaResetsTotal.WithLabelValues(scope, "stale").Inc()
			return nil
		}
		if errors.Is(err, ErrQuotaResetRunDuplicate) {
			metrics.SubscriptionAccountQuotaResetsTotal.WithLabelValues(scope, "duplicate").Inc()
			return nil
		}
		metrics.SubscriptionAccountQuotaResetsTotal.WithLabelValues(scope, "error").Inc()
		return err
	}
	metrics.SubscriptionAccountQuotaResetsTotal.WithLabelValues(scope, "success").Inc()
	return nil
}

// AccountRecoverySweeperConfig configures the automated account-recovery sweep.
type AccountRecoverySweeperConfig struct {
	Enabled  bool
	Interval time.Duration
	PageSize int32
	Timeout  time.Duration
	Shard    AccountScanShard
}

// AccountRecoverySweeper scans unschedulable subscription accounts and, for
// those whose recovery policy allows automatic recovery, clears the temporary
// unschedulable markers once their TTL has elapsed. Authorization errors
// (401/403) are stamped manual and never auto-recovered; local-quota and
// codex-snapshot exhaustion are only recovered after the underlying window or
// snapshot has reset (detected by re-checking IsSchedulableAt).
type AccountRecoverySweeper struct {
	repo    ChannelRepo
	now     func() time.Time
	cfg     AccountRecoverySweeperConfig
	prober  RecoveryProber // optional upstream probe (roadmap §1.2)
	sweepMu sync.Mutex
	afterID int64
}

// NewAccountRecoverySweeper builds an automated account-recovery sweeper.
func NewAccountRecoverySweeper(repo ChannelRepo, cfg AccountRecoverySweeperConfig) *AccountRecoverySweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 200
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &AccountRecoverySweeper{repo: repo, now: time.Now, cfg: cfg}
}

// SetNow overrides the clock (tests).
func (s *AccountRecoverySweeper) SetNow(f func() time.Time) { s.now = f }

// SetProber wires an optional upstream recovery probe (roadmap §1.2). When set,
// the sweeper asks the prober to confirm the account is healthy before clearing
// unschedulable markers for auto-policy accounts; this prevents re-enabling an
// account that still fails upstream even though its local TTL has elapsed. Nil
// (the default) keeps the local-state-only recovery behavior.
func (s *AccountRecoverySweeper) SetProber(p RecoveryProber) { s.prober = p }

// Run loops until ctx is cancelled, executing SweepOnce every Interval.
func (s *AccountRecoverySweeper) Run(ctx context.Context) {
	if s == nil || !s.cfg.Enabled {
		return
	}
	ticker := time.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.SweepOnce(ctx)
		}
	}
}

// SweepOnce scans all enabled-but-unschedulable accounts and attempts recovery
// for those whose policy is auto or whose blocking condition has cleared.
func (s *AccountRecoverySweeper) SweepOnce(ctx context.Context) error {
	if !s.sweepMu.TryLock() {
		return ErrAccountSweepInProgress
	}
	defer s.sweepMu.Unlock()
	if err := s.cfg.Shard.Validate(); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, s.cfg.Timeout)
	defer cancel()
	startedAt := time.Now()
	defer func() {
		metrics.SubscriptionAccountRecoveryScanDuration.Observe(time.Since(startedAt).Seconds())
	}()
	now := s.now()
	for {
		// status=1 (enabled) — we only consider enabled accounts for recovery;
		// disabled accounts were paused by AutoPauseAccount and require manual
		// re-enablement.
		accounts, err := s.repo.ScanSubscriptionAccounts(ctx, AccountScan{Shard: s.cfg.Shard, AfterID: s.afterID, Limit: s.cfg.PageSize, Status: ChannelStatusEnabled})
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if err := ctx.Err(); err != nil {
				return err
			}
			if account == nil {
				continue
			}
			s.afterID = account.ID
			s.tryRecover(ctx, account, now)
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if len(accounts) < int(s.cfg.PageSize) {
			s.afterID = 0
			return nil
		}
	}
}

// tryRecover evaluates a single account and clears its unschedulable markers
// when safe. Recovery classes:
//   - auto: TTL elapsed -> clear rate_limited_until + last_error + markers.
//   - quota: local window reset (detected via IsSchedulableAt) -> clear markers.
//   - codex: snapshot reset (detected via IsSchedulableAt) -> clear markers.
//   - manual: never auto-recover (authorization error).
func (s *AccountRecoverySweeper) tryRecover(ctx context.Context, account *SubscriptionAccount, now time.Time) {
	expected := account.RecoveryState()
	policy := subscriptionAccountMetadataValue(account.Metadata, metaKeyRecoveryPolicy)
	if policy == "" {
		policy = RecoveryPolicyRolling
	}
	if policy == RecoveryPolicyManual {
		metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "skipped").Inc()
		return
	}
	if !account.CanAutoRecoverAt(now) {
		metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "waiting").Inc()
		return
	}
	if expected.RateLimitedUntil == 0 && expected.LastError == "" &&
		subscriptionAccountMetadataValue(expected.Metadata, metaKeyRecoveryPolicy) == "" &&
		subscriptionAccountMetadataValue(expected.Metadata, metaKeyUnschedulableReason) == "" &&
		subscriptionAccountMetadataValue(expected.Metadata, metaKeyRecoveryRevision) == "" {
		return
	}
	if s.prober != nil && (policy == RecoveryPolicyAuto || policy == RecoveryPolicyRolling) {
		ok, err := s.prober.ProbeRecovery(ctx, account)
		if ctx.Err() != nil {
			return
		}
		switch {
		case err != nil:
			// Unsupported platforms and unavailable probes retain the local
			// fallback. The repository still checks the captured state and quota.
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "probe_unavailable").Inc()
		case !ok:
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "probe_negative").Inc()
			return
		default:
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "probe_confirmed").Inc()
		}
	}
	result := "already_schedulable"
	if policy == RecoveryPolicyQuota || policy == RecoveryPolicyCodex {
		result = "window_reset"
	} else if expected.RateLimitedUntil > 0 {
		result = "ttl_elapsed"
	}
	s.clearMarkers(ctx, expected, policy, result)
}

// clearMarkers compares the scanned state and current eligibility inside the
// same transaction as the marker and ability updates. A stale task is a no-op.
func (s *AccountRecoverySweeper) clearMarkers(ctx context.Context, expected AccountRecoveryState, policy, result string) {
	cleared, err := s.repo.ClearRecoveryMarkers(ctx, expected, s.now())
	if err != nil {
		metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "error").Inc()
		return
	}
	if !cleared {
		metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "stale").Inc()
		return
	}
	metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, result).Inc()
}

// subscriptionAccountMetadataValue reads a key from the account metadata JSON
// blob. Mirrors the data-layer helper so the biz layer can classify recovery
// without a repo round-trip.
func subscriptionAccountMetadataValue(raw, key string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// Minimal JSON map read without importing encoding/json here to keep the
	// biz package free of transport concerns; the data layer's canonical
	// implementation remains the source of truth. We only need string values.
	var values map[string]any
	if err := jsonUnmarshal(raw, &values); err != nil {
		return ""
	}
	v, _ := values[key].(string)
	return v
}

// jsonUnmarshal parses a metadata JSON blob into a map. Wraps encoding/json
// so callers in this package share one implementation.
var jsonUnmarshal = func(raw string, out *map[string]any) error {
	return jsonx.Unmarshal([]byte(raw), out)
}

// SubscriptionAccountRecoveryInfo summarizes why an account is unschedulable
// and when it is expected to recover, for the admin UI.
type SubscriptionAccountRecoveryInfo struct {
	Policy             string
	Reason             string
	Since              int64
	Until              int64
	ExpectedRecoveryAt int64
}

// RecoveryInfo extracts the recovery metadata for an account. When the account
// is schedulable the returned info is zero-valued (policy="auto").
func (a *SubscriptionAccount) RecoveryInfo(now time.Time) SubscriptionAccountRecoveryInfo {
	if a == nil {
		return SubscriptionAccountRecoveryInfo{Policy: RecoveryPolicyAuto}
	}
	info := SubscriptionAccountRecoveryInfo{
		Policy:             subscriptionAccountMetadataValue(a.Metadata, metaKeyRecoveryPolicy),
		Reason:             subscriptionAccountMetadataValue(a.Metadata, metaKeyUnschedulableReason),
		Since:              parseMetadataInt(a.Metadata, metaKeyUnschedulableSince),
		Until:              a.RateLimitedUntil,
		ExpectedRecoveryAt: parseMetadataInt(a.Metadata, metaKeyExpectedRecoveryAt),
	}
	if info.Policy == "" {
		info.Policy = RecoveryPolicyRolling
	}
	if info.Reason == "" && a.LastError != "" {
		info.Reason = a.LastError
	}
	// Derive expected recovery for auto/rolling when not explicitly stamped.
	if (info.Policy == RecoveryPolicyAuto || info.Policy == RecoveryPolicyRolling) && info.ExpectedRecoveryAt == 0 && a.RateLimitedUntil > 0 {
		info.ExpectedRecoveryAt = a.RateLimitedUntil
	}
	// For schedulable accounts the markers should already be cleared; return
	// a clean info so the UI shows "available".
	if a.IsSchedulableAt(now) {
		return SubscriptionAccountRecoveryInfo{Policy: info.Policy}
	}
	return info
}

// parseMetadataInt reads an int64 metadata field.
func parseMetadataInt(raw, key string) int64 {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	var values map[string]any
	if err := jsonUnmarshal(raw, &values); err != nil {
		return 0
	}
	switch v := values[key].(type) {
	case float64:
		return int64(v)
	case string:
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			return n
		}
	}
	return 0
}
