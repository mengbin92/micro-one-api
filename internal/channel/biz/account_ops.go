package biz

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"micro-one-api/internal/pkg/metrics"
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
	metaKeyLastError            = "last_error"
	metaKeyRecoveryPolicy       = "recovery_policy"
	metaKeyUnschedulableReason  = "unschedulable_reason"
	metaKeyUnschedulableSince   = "unschedulable_since"
	metaKeyUnschedulableUntil   = "unschedulable_until"
	metaKeyExpectedRecoveryAt   = "expected_recovery_at"
	metaKeyLastQuotaAlertAt     = "last_quota_alert_at"
	metaKeyLastQuotaAlertKind   = "last_quota_alert_kind"
)

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
	Enabled   bool
	Interval  time.Duration
	PageSize  int32
	Timeout   time.Duration
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
	repo     ChannelRepo
	now      func() time.Time
	cfg      QuotaResetSweeperConfig
	recorder QuotaResetRunRecorder
}

// QuotaResetRunRecorder durably records a reset run for idempotency and audit.
// Implementations return ErrQuotaResetRunDuplicate when the (account, scope,
// window_start) tuple has already been recorded.
type QuotaResetRunRecorder interface {
	RecordQuotaResetRun(ctx context.Context, run *SubscriptionAccountQuotaResetRun) error
}

// SubscriptionAccountQuotaResetRun is the audit row for an automated reset.
type SubscriptionAccountQuotaResetRun struct {
	AccountID  int64
	Scope      string
	WindowStart int64
	Strategy   string
	Timezone   string
	ResetAt    time.Time
}

// ErrQuotaResetRunDuplicate is returned by RecordQuotaResetRun when the run has
// already been recorded (duplicate worker tick within the same window).
var ErrQuotaResetRunDuplicate = fmt.Errorf("quota reset run already recorded")

// NewQuotaResetSweeper builds a fixed-strategy quota reset sweeper.
func NewQuotaResetSweeper(repo ChannelRepo, recorder QuotaResetRunRecorder, cfg QuotaResetSweeperConfig) *QuotaResetSweeper {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Minute
	}
	if cfg.PageSize <= 0 {
		cfg.PageSize = 200
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	return &QuotaResetSweeper{repo: repo, now: time.Now, cfg: cfg, recorder: recorder}
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
	startedAt := time.Now()
	defer func() {
		metrics.SubscriptionAccountQuotaResetScanDuration.Observe(time.Since(startedAt).Seconds())
	}()
	now := s.now()
	page := int32(1)
	for {
		accounts, total, err := s.repo.ListSubscriptionAccounts(ctx, page, s.cfg.PageSize, "", "", 0, "")
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if account == nil || !account.UsesFixedQuotaReset() {
				continue
			}
			s.resetIfCrossedBoundary(ctx, account, now, "daily")
			s.resetIfCrossedBoundary(ctx, account, now, "weekly")
		}
		if int64(page)*int64(s.cfg.PageSize) >= total {
			return nil
		}
		page++
	}
}

// resetIfCrossedBoundary resets a single scope for an account when the stored
// window_start is older than the current fixed window start. The durable
// reset-run record is written first; if it already exists (duplicate tick),
// the reset is skipped — this keeps multiple replicas from double-resetting.
func (s *QuotaResetSweeper) resetIfCrossedBoundary(ctx context.Context, account *SubscriptionAccount, now time.Time, scope string) {
	fixedStart := account.FixedQuotaWindowStart(now, scope)
	var storedStart int64
	switch scope {
	case "daily":
		storedStart = account.QuotaDailyWindowStart
	case "weekly":
		storedStart = account.QuotaWeeklyWindowStart
	default:
		return
	}
	// No usage yet, or already aligned to the current fixed window: nothing to do.
	if storedStart <= 0 || storedStart >= fixedStart {
		return
	}
	if s.recorder != nil {
		run := &SubscriptionAccountQuotaResetRun{
			AccountID:   account.ID,
			Scope:       scope,
			WindowStart: fixedStart,
			Strategy:    account.EffectiveQuotaResetStrategy(),
			Timezone:    account.EffectiveQuotaTimezone(),
			ResetAt:     now,
		}
		if err := s.recorder.RecordQuotaResetRun(ctx, run); err != nil {
			// Already recorded by another replica/tick — skip the write.
			metrics.SubscriptionAccountQuotaResetsTotal.WithLabelValues(scope, "duplicate").Inc()
			return
		}
	}
	if err := s.repo.ResetSubscriptionAccountQuota(ctx, account.ID, scope); err != nil {
		metrics.SubscriptionAccountQuotaResetsTotal.WithLabelValues(scope, "error").Inc()
		return
	}
	metrics.SubscriptionAccountQuotaResetsTotal.WithLabelValues(scope, "success").Inc()
}

// AccountRecoverySweeperConfig configures the automated account-recovery sweep.
type AccountRecoverySweeperConfig struct {
	Enabled   bool
	Interval  time.Duration
	PageSize  int32
	Timeout   time.Duration
}

// AccountRecoverySweeper scans unschedulable subscription accounts and, for
// those whose recovery policy allows automatic recovery, clears the temporary
// unschedulable markers once their TTL has elapsed. Authorization errors
// (401/403) are stamped manual and never auto-recovered; local-quota and
// codex-snapshot exhaustion are only recovered after the underlying window or
// snapshot has reset (detected by re-checking IsSchedulableAt).
type AccountRecoverySweeper struct {
	repo ChannelRepo
	now  func() time.Time
	cfg  AccountRecoverySweeperConfig
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
	startedAt := time.Now()
	defer func() {
		metrics.SubscriptionAccountRecoveryScanDuration.Observe(time.Since(startedAt).Seconds())
	}()
	now := s.now()
	page := int32(1)
	for {
		// status=1 (enabled) — we only consider enabled accounts for recovery;
		// disabled accounts were paused by AutoPauseAccount and require manual
		// re-enablement.
		accounts, total, err := s.repo.ListSubscriptionAccounts(ctx, page, s.cfg.PageSize, "", "", ChannelStatusEnabled, "")
		if err != nil {
			return err
		}
		for _, account := range accounts {
			if account == nil {
				continue
			}
			s.tryRecover(ctx, account, now)
		}
		if int64(page)*int64(s.cfg.PageSize) >= total {
			return nil
		}
		page++
	}
}

// tryRecover evaluates a single account and clears its unschedulable markers
// when safe. Recovery classes:
//   - auto: TTL elapsed -> clear rate_limited_until + last_error + markers.
//   - quota: local window reset (detected via IsSchedulableAt) -> clear markers.
//   - codex: snapshot reset (detected via IsSchedulableAt) -> clear markers.
//   - manual: never auto-recover (authorization error).
func (s *AccountRecoverySweeper) tryRecover(ctx context.Context, account *SubscriptionAccount, now time.Time) {
	policy := subscriptionAccountMetadataValue(account.Metadata, metaKeyRecoveryPolicy)
	if policy == "" {
		policy = RecoveryPolicyRolling
	}
	// Authorization errors are never auto-recovered.
	if policy == RecoveryPolicyManual {
		metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "skipped").Inc()
		return
	}
	// Only attempt recovery when the account is currently unschedulable.
	if account.IsSchedulableAt(now) {
		// Already schedulable: clear stale markers if any remain.
		if account.RateLimitedUntil > 0 || subscriptionAccountMetadataValue(account.Metadata, metaKeyUnschedulableReason) != "" {
			s.clearMarkers(ctx, account, policy, "already_schedulable")
		}
		return
	}
	switch policy {
	case RecoveryPolicyAuto:
		// TTL-based: only recover once rate_limited_until is in the past.
		if account.RateLimitedUntil > 0 && now.Unix() < account.RateLimitedUntil {
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "waiting").Inc()
			return
		}
		s.clearMarkers(ctx, account, policy, "ttl_elapsed")
	case RecoveryPolicyQuota, RecoveryPolicyCodex:
		// Recover only once the underlying window/snapshot has actually reset
		// (IsSchedulableAt flips back to true). This avoids re-enabling an
		// account that is still over quota.
		if account.LocalQuotaExceededAt(now) {
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "waiting").Inc()
			return
		}
		s.clearMarkers(ctx, account, policy, "window_reset")
	default:
		// rolling / unknown: treat like auto (TTL-based) for backward compat.
		if account.RateLimitedUntil > 0 && now.Unix() < account.RateLimitedUntil {
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "waiting").Inc()
			return
		}
		s.clearMarkers(ctx, account, policy, "ttl_elapsed")
	}
}

// clearMarkers clears the temporary unschedulable state and stamps metadata so
// the admin UI can show "recovered" rather than the stale reason.
func (s *AccountRecoverySweeper) clearMarkers(ctx context.Context, account *SubscriptionAccount, policy, result string) {
	if account.RateLimitedUntil > 0 {
		if err := s.repo.ClearTempUnschedulable(ctx, account.ID); err != nil {
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "error").Inc()
			return
		}
	}
	if msg := subscriptionAccountMetadataValue(account.Metadata, metaKeyLastError); msg != "" {
		if err := s.repo.SetSubscriptionAccountError(ctx, account.ID, ""); err != nil {
			metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "error").Inc()
			return
		}
	}
	if err := s.repo.ClearRecoveryMetadata(ctx, account.ID); err != nil {
		metrics.SubscriptionAccountRecoveriesTotal.WithLabelValues(policy, "error").Inc()
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
	var values map[string]interface{}
	if err := jsonUnmarshal(raw, &values); err != nil {
		return ""
	}
	v, _ := values[key].(string)
	return v
}

// jsonUnmarshal parses a metadata JSON blob into a map. Wraps encoding/json
// so callers in this package share one implementation.
var jsonUnmarshal = func(raw string, out *map[string]interface{}) error {
	return json.Unmarshal([]byte(raw), out)
}

// SubscriptionAccountRecoveryInfo summarizes why an account is unschedulable
// and when it is expected to recover, for the admin UI.
type SubscriptionAccountRecoveryInfo struct {
	Policy            string
	Reason            string
	Since             int64
	Until             int64
	ExpectedRecoveryAt int64
}

// RecoveryInfo extracts the recovery metadata for an account. When the account
// is schedulable the returned info is zero-valued (policy="auto").
func (a *SubscriptionAccount) RecoveryInfo(now time.Time) SubscriptionAccountRecoveryInfo {
	if a == nil {
		return SubscriptionAccountRecoveryInfo{Policy: RecoveryPolicyAuto}
	}
	info := SubscriptionAccountRecoveryInfo{
		Policy: subscriptionAccountMetadataValue(a.Metadata, metaKeyRecoveryPolicy),
		Reason: subscriptionAccountMetadataValue(a.Metadata, metaKeyUnschedulableReason),
		Since:  parseMetadataInt(a.Metadata, metaKeyUnschedulableSince),
		Until:  a.RateLimitedUntil,
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
	var values map[string]interface{}
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
