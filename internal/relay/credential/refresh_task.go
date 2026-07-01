package credential

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/internal/pkg/logger"
)

// RefreshTask is a background goroutine that periodically scans subscription
// accounts whose access token will expire within the look-ahead window and
// proactively refreshes them. This is the "background refresh" half of the
// double-safety described in plan §4.5; the other half is request-time
// refresh in the token providers.
//
// It mirrors sub2api's token_refresh_service.go and new-api's
// codex_credential_refresh_task.go: every interval it asks the AccountLookup
// for the set of accounts expiring soon and refreshes each in turn.
type RefreshTask struct {
	providers  map[Platform]TokenProvider
	lookup     AccountLookup
	interval   time.Duration
	lookahead  time.Duration
	maxRetries int
	backoff    time.Duration
	tempBlock  time.Duration
	hook       RefreshHook
	stopCh     chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
	platformOf func(accountID int64) Platform
	logger     *zap.Logger
}

// RefreshTaskConfig configures the background refresh task.
type RefreshTaskConfig struct {
	// Interval is how often the task scans for soon-to-expire accounts.
	// Defaults to 10 minutes.
	Interval time.Duration
	// Lookahead is how far ahead to look for expiring accounts. Accounts whose
	// token expires within now+Lookahead are refreshed. Defaults to 24 hours.
	Lookahead time.Duration
	// MaxRetries is the number of attempts per account before the refresh is
	// considered failed. Defaults to 3.
	MaxRetries int
	// RetryBackoff is the base backoff between retry attempts. Defaults to 2s.
	RetryBackoff time.Duration
	// TempUnschedulableDuration is how long a retry-exhausted account is marked
	// as temporarily unschedulable by the configured hook. Defaults to 10m.
	TempUnschedulableDuration time.Duration
	// Hook receives lifecycle callbacks around refresh success/failure. Nil is
	// allowed; the refresh task still refreshes tokens and logs errors.
	Hook RefreshHook
}

// DefaultRefreshTaskConfig returns sensible defaults.
func DefaultRefreshTaskConfig() RefreshTaskConfig {
	return RefreshTaskConfig{
		Interval:                  10 * time.Minute,
		Lookahead:                 24 * time.Hour,
		MaxRetries:                3,
		RetryBackoff:              2 * time.Second,
		TempUnschedulableDuration: 10 * time.Minute,
	}
}

// PlatformResolver returns the platform for an account ID. The refresh task
// needs this to pick the right TokenProvider. Implementations typically query
// the channel/identity service.
type PlatformResolver func(accountID int64) Platform

// NewRefreshTask builds a background refresh task. providers maps a platform
// to its token provider; only platforms present in the map are refreshed.
// platformOf resolves an account ID to its platform (may return "" to skip).
func NewRefreshTask(
	providers map[Platform]TokenProvider,
	lookup AccountLookup,
	platformOf PlatformResolver,
	cfg RefreshTaskConfig,
) *RefreshTask {
	if cfg.Interval <= 0 {
		cfg.Interval = 10 * time.Minute
	}
	if cfg.Lookahead <= 0 {
		cfg.Lookahead = 24 * time.Hour
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryBackoff <= 0 {
		cfg.RetryBackoff = 2 * time.Second
	}
	if cfg.TempUnschedulableDuration <= 0 {
		cfg.TempUnschedulableDuration = 10 * time.Minute
	}
	return &RefreshTask{
		providers:  providers,
		lookup:     lookup,
		interval:   cfg.Interval,
		lookahead:  cfg.Lookahead,
		maxRetries: cfg.MaxRetries,
		backoff:    cfg.RetryBackoff,
		tempBlock:  cfg.TempUnschedulableDuration,
		hook:       cfg.Hook,
		stopCh:     make(chan struct{}),
		platformOf: platformOf,
		logger:     applogger.Log,
	}
}

// Start launches the background refresh goroutine. It is safe to call once.
func (t *RefreshTask) Start() {
	t.wg.Add(1)
	go t.run()
}

// Stop signals the background goroutine to exit and waits for it.
func (t *RefreshTask) Stop() {
	t.stopOnce.Do(func() { close(t.stopCh) })
	t.wg.Wait()
}

func (t *RefreshTask) run() {
	defer t.wg.Done()
	// Run once immediately on start so a freshly-booted gateway does not wait
	// a full interval before its first sweep.
	t.sweep()
	ticker := time.NewTicker(t.interval)
	defer ticker.Stop()
	for {
		select {
		case <-t.stopCh:
			return
		case <-ticker.C:
			t.sweep()
		}
	}
}

// sweep performs a single refresh pass. The MVP iterates the accounts the
// AccountLookup reports as expiring soon; a full deployment would push this
// scan into the database / service layer.
func (t *RefreshTask) sweep() {
	ctx, cancel := context.WithTimeout(context.Background(), t.interval)
	defer cancel()

	// ExpiringSoon is optional: if the AccountLookup does not implement
	// ExpiringScanner we simply skip the proactive sweep (request-time refresh
	// still covers correctness).
	scanner, ok := t.lookup.(ExpiringScanner)
	if !ok {
		return
	}
	ids, err := scanner.ExpiringSoon(ctx, t.lookahead)
	if err != nil {
		if t.logger != nil {
			t.logger.Warn("credential refresh sweep failed", zap.Error(err))
		}
		return
	}
	for _, id := range ids {
		platform := t.platformOf(id)
		if platform == "" {
			continue
		}
		provider, ok := t.providers[platform]
		if !ok {
			continue
		}
		if err := t.refreshAccount(ctx, provider, id); err != nil {
			if t.logger != nil {
				t.logger.Warn("credential refresh failed",
					zap.Int64("account_id", id),
					zap.String("platform", string(platform)),
					zap.Error(err))
			}
		}
	}
}

func (t *RefreshTask) refreshAccount(ctx context.Context, provider TokenProvider, accountID int64) error {
	var lastErr error
	for attempt := 1; attempt <= t.maxRetries; attempt++ {
		err := provider.Refresh(ctx, accountID)
		if err == nil {
			t.invalidate(provider, accountID)
			if t.hook != nil {
				if hookErr := t.hook.OnRefreshSuccess(ctx, accountID); hookErr != nil && t.logger != nil {
					t.logger.Warn("credential refresh success hook failed", zap.Int64("account_id", accountID), zap.Error(hookErr))
				}
			}
			return nil
		}
		lastErr = err
		if isNonRetryableRefreshError(err) {
			t.handleRefreshFailure(ctx, accountID, err, true)
			return err
		}
		if attempt < t.maxRetries {
			if !t.sleepBackoff(ctx, attempt) {
				return ctx.Err()
			}
		}
	}
	t.handleRefreshFailure(ctx, accountID, lastErr, false)
	return lastErr
}

func (t *RefreshTask) invalidate(provider TokenProvider, accountID int64) {
	if inv, ok := provider.(TokenInvalidator); ok {
		inv.Invalidate(accountID)
	}
}

func (t *RefreshTask) handleRefreshFailure(ctx context.Context, accountID int64, err error, nonRetryable bool) {
	if t.hook == nil {
		return
	}
	reason := ""
	if err != nil {
		reason = err.Error()
	}
	if nonRetryable {
		if hookErr := t.hook.OnRefreshNonRetryable(ctx, accountID, reason); hookErr != nil && t.logger != nil {
			t.logger.Warn("credential refresh non-retryable hook failed", zap.Int64("account_id", accountID), zap.Error(hookErr))
		}
		return
	}
	until := time.Now().Add(t.tempBlock)
	if hookErr := t.hook.OnRefreshRetryExhausted(ctx, accountID, until, reason); hookErr != nil && t.logger != nil {
		t.logger.Warn("credential refresh failure hook failed", zap.Int64("account_id", accountID), zap.Error(hookErr))
	}
}

func (t *RefreshTask) sleepBackoff(ctx context.Context, attempt int) bool {
	d := t.backoff * time.Duration(1<<(attempt-1))
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.stopCh:
		return false
	case <-timer.C:
		return true
	}
}

func isNonRetryableRefreshError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNoRefreshToken) || errors.Is(err, ErrAccountNotFound) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "invalid_grant") || strings.Contains(msg, "invalid refresh")
}

// ExpiringScanner is an optional extension of AccountLookup that reports the
// set of account IDs whose access token expires within the given duration.
// When the lookup does not implement it the background task is a no-op.
type ExpiringScanner interface {
	ExpiringSoon(ctx context.Context, within time.Duration) ([]int64, error)
}

// RefreshHook receives background refresh lifecycle events. Implementations
// can clear account errors after success, persist non-retryable auth failures,
// and mark accounts temporarily unschedulable after transient failures exhaust
// retries.
type RefreshHook interface {
	OnRefreshSuccess(ctx context.Context, accountID int64) error
	OnRefreshNonRetryable(ctx context.Context, accountID int64, reason string) error
	OnRefreshRetryExhausted(ctx context.Context, accountID int64, until time.Time, reason string) error
}
