package credential

import (
	"context"
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
	providers   map[Platform]TokenProvider
	lookup      AccountLookup
	interval    time.Duration
	lookahead   time.Duration
	stopCh      chan struct{}
	wg          sync.WaitGroup
	platformOf  func(accountID int64) Platform
	logger      *zap.Logger
}

// RefreshTaskConfig configures the background refresh task.
type RefreshTaskConfig struct {
	// Interval is how often the task scans for soon-to-expire accounts.
	// Defaults to 10 minutes.
	Interval time.Duration
	// Lookahead is how far ahead to look for expiring accounts. Accounts whose
	// token expires within now+Lookahead are refreshed. Defaults to 24 hours.
	Lookahead time.Duration
}

// DefaultRefreshTaskConfig returns sensible defaults.
func DefaultRefreshTaskConfig() RefreshTaskConfig {
	return RefreshTaskConfig{
		Interval:  10 * time.Minute,
		Lookahead: 24 * time.Hour,
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
	return &RefreshTask{
		providers:  providers,
		lookup:     lookup,
		interval:   cfg.Interval,
		lookahead:  cfg.Lookahead,
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
	close(t.stopCh)
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
		if err := provider.Refresh(ctx, id); err != nil {
			if t.logger != nil {
				t.logger.Warn("credential refresh failed",
					zap.Int64("account_id", id),
					zap.String("platform", string(platform)),
					zap.Error(err))
			}
		}
	}
}

// ExpiringScanner is an optional extension of AccountLookup that reports the
// set of account IDs whose access token expires within the given duration.
// When the lookup does not implement it the background task is a no-op.
type ExpiringScanner interface {
	ExpiringSoon(ctx context.Context, within time.Duration) ([]int64, error)
}
