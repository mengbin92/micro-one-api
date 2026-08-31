package biz

import (
	"context"
	"os"
	"strconv"
	"time"
)

// Usage-semantics quarantine (token-usage-billing-semantics-remediation §5.2).
//
// When a (source_kind, source_id, upstream_model_id, adapter_protocol) key
// repeatedly yields ambiguous usage, the key is paused so a broken adapter
// cannot keep producing unverifiable bills. The database row
// (usage_semantic_source_blocks, migration 087) is the authoritative
// cross-instance state; the in-memory cache below is a short-TTL read
// optimization only, and recovery (manual resolve or probe) clears the
// persisted row first. This control plane is deliberately separate from
// transport health: the upstream HTTP call succeeded.

const (
	UsageSemanticSourceKindChannel      = "channel"
	UsageSemanticSourceKindSubscription = "subscription"

	UsageSemanticBlockStatusActive   = "active"
	UsageSemanticBlockStatusBlocked  = "blocked"
	UsageSemanticBlockStatusResolved = "resolved"
)

// UsageSemanticBlock is one quarantine row.
type UsageSemanticBlock struct {
	ID                   int64
	SourceKind           string
	SourceID             int64
	UpstreamModelID      string
	AdapterProtocol      string
	Status               string
	Reason               string
	WindowStartedAt      time.Time
	ConsecutiveAmbiguous int32
	BlockedUntil         time.Time
	LastVerifiedAt       time.Time
	UpdatedAt            time.Time
}

// UsageSemanticVerdict is the relay-reported parse verdict of a final
// attempt. Only "verified" and "ambiguous" move the quarantine state.
type UsageSemanticVerdict struct {
	SourceKind      string
	SourceID        int64
	UpstreamModelID string
	AdapterProtocol string
	ParseStatus     string // verified | estimated | ambiguous
	Reason          string
}

// usageSemanticBlockRepo is the persistence surface for the quarantine. It
// is satisfied by data.Repository; the usecase type-asserts (same pattern
// as idempotentUsageRepo) so memory-mode deployments degrade to no-ops.
type usageSemanticBlockRepo interface {
	UpsertUsageSemanticVerdict(ctx context.Context, verdict UsageSemanticVerdict, window, blockDuration time.Duration, threshold int32, now time.Time) (*UsageSemanticBlock, error)
	ListBlockedUsageSemanticBlocks(ctx context.Context, now time.Time) ([]UsageSemanticBlock, error)
	ResolveUsageSemanticBlock(ctx context.Context, sourceKind string, sourceID int64, upstreamModelID, adapterProtocol string, now time.Time) (bool, error)
	ListUsageSemanticBlocks(ctx context.Context, onlyBlocked bool, page, pageSize int32) ([]UsageSemanticBlock, int64, error)
}

// UsageSemanticConfig holds the tunable quarantine thresholds (§5.2 point 4:
// defaults 3 consecutive ambiguous inside 5 minutes -> 15 minute pause; all
// configurable via env).
type UsageSemanticConfig struct {
	Window        time.Duration
	BlockDuration time.Duration
	Threshold     int32
}

func resolveUsageSemanticConfig() UsageSemanticConfig {
	return UsageSemanticConfig{
		Window:        envDurationSeconds("USAGE_SEMANTIC_AMBIGUOUS_WINDOW_SECONDS", 300),
		BlockDuration: envDurationSeconds("USAGE_SEMANTIC_BLOCK_DURATION_SECONDS", 900),
		Threshold:     envInt32("USAGE_SEMANTIC_AMBIGUOUS_THRESHOLD", 3),
	}
}

func envDurationSeconds(key string, def int64) time.Duration {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v > 0 {
			return time.Duration(v) * time.Second
		}
	}
	return time.Duration(def) * time.Second
}

func envInt32(key string, def int32) int32 {
	if raw := os.Getenv(key); raw != "" {
		if v, err := strconv.ParseInt(raw, 10, 32); err == nil && v > 0 {
			return int32(v)
		}
	}
	return def
}

// RecordUsageSemanticVerdict applies one final-attempt verdict to the
// quarantine. verified resets the consecutive counter; ambiguous increments
// it and trips the block at the threshold. Both channel and subscription
// selectors consult IsUsageSemanticBlocked at source+model granularity.
func (uc *ChannelUsecase) RecordUsageSemanticVerdict(ctx context.Context, verdict UsageSemanticVerdict) (blocked bool, blockedUntil time.Time, consecutive int32, err error) {
	if verdict.SourceID <= 0 || verdict.UpstreamModelID == "" {
		return false, time.Time{}, 0, nil
	}
	// estimated verdicts are informational only (§5.2: the estimator never
	// fabricates cache, so nothing is mis-billed).
	if verdict.ParseStatus != "verified" && verdict.ParseStatus != "ambiguous" {
		return false, time.Time{}, 0, nil
	}
	repo, ok := uc.repo.(usageSemanticBlockRepo)
	if !ok {
		return false, time.Time{}, 0, nil
	}
	cfg := resolveUsageSemanticConfig()
	row, err := repo.UpsertUsageSemanticVerdict(ctx, verdict, cfg.Window, cfg.BlockDuration, cfg.Threshold, uc.now())
	if err != nil {
		return false, time.Time{}, 0, err
	}
	if row == nil {
		return false, time.Time{}, 0, nil
	}
	uc.markUsageSemanticBlocksDirty()
	blocked = row.Status == UsageSemanticBlockStatusBlocked && row.BlockedUntil.After(uc.now())
	return blocked, row.BlockedUntil, row.ConsecutiveAmbiguous, nil
}

// ResolveUsageSemanticBlock is the manual recovery path (§5.2 point 6): an
// operator confirms the adapter is fixed; the persisted block is cleared and
// the selector cache is invalidated.
func (uc *ChannelUsecase) ResolveUsageSemanticBlock(ctx context.Context, sourceKind string, sourceID int64, upstreamModelID, adapterProtocol string) (bool, error) {
	repo, ok := uc.repo.(usageSemanticBlockRepo)
	if !ok {
		return false, nil
	}
	resolved, err := repo.ResolveUsageSemanticBlock(ctx, sourceKind, sourceID, upstreamModelID, adapterProtocol, uc.now())
	if err != nil {
		return false, err
	}
	if resolved {
		uc.markUsageSemanticBlocksDirty()
	}
	return resolved, nil
}

// ListUsageSemanticBlocks returns quarantine rows for the admin surface.
func (uc *ChannelUsecase) ListUsageSemanticBlocks(ctx context.Context, onlyBlocked bool, page, pageSize int32) ([]UsageSemanticBlock, int64, error) {
	repo, ok := uc.repo.(usageSemanticBlockRepo)
	if !ok {
		return nil, 0, nil
	}
	return repo.ListUsageSemanticBlocks(ctx, onlyBlocked, page, pageSize)
}

// IsUsageSemanticBlocked reports whether a (source, model) pair is currently
// quarantined. The adapter protocol is not matched at selection time (it is
// proven only by the response); any active block on the source+model key
// filters the candidate. Reads go through a short-TTL cache; the database
// stays authoritative across instances and restarts.
func (uc *ChannelUsecase) IsUsageSemanticBlocked(ctx context.Context, sourceKind string, sourceID int64, upstreamModelID string) bool {
	if sourceID <= 0 || upstreamModelID == "" {
		return false
	}
	for _, b := range uc.blockedUsageSemanticKeys(ctx) {
		if b.SourceKind == sourceKind && b.SourceID == sourceID && b.UpstreamModelID == upstreamModelID {
			return true
		}
	}
	return false
}

// blockedUsageSemanticKeys caches the blocked set briefly. Record/Resolve
// flip the dirty flag so writers observe their own block immediately while
// other instances converge within the TTL (§6.1: DB is authoritative).
func (uc *ChannelUsecase) blockedUsageSemanticKeys(ctx context.Context) []UsageSemanticBlock {
	repo, ok := uc.repo.(usageSemanticBlockRepo)
	if !ok {
		return nil
	}
	now := uc.now()
	uc.usageSemanticBlocksMu.Lock()
	defer uc.usageSemanticBlocksMu.Unlock()
	if !uc.usageSemanticBlocksDirty && now.Before(uc.usageSemanticBlocksCacheUntil) {
		return append([]UsageSemanticBlock(nil), uc.usageSemanticBlocksCache...)
	}
	blocks, err := repo.ListBlockedUsageSemanticBlocks(ctx, now)
	if err != nil {
		// Fail-open on read errors: a blip in the block store must not stop
		// all routing. Writers still record verdicts independently.
		return append([]UsageSemanticBlock(nil), uc.usageSemanticBlocksCache...)
	}
	uc.usageSemanticBlocksCache = append([]UsageSemanticBlock(nil), blocks...)
	uc.usageSemanticBlocksCacheUntil = now.Add(5 * time.Second)
	uc.usageSemanticBlocksDirty = false
	return append([]UsageSemanticBlock(nil), uc.usageSemanticBlocksCache...)
}

func (uc *ChannelUsecase) markUsageSemanticBlocksDirty() {
	uc.usageSemanticBlocksMu.Lock()
	uc.usageSemanticBlocksDirty = true
	uc.usageSemanticBlocksMu.Unlock()
}
