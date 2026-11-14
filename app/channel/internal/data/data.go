package data

import (
	"context"
	"errors"
	"fmt"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"micro-one-api/domain/routing"

	"micro-one-api/pkg/jsonx"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/pkg/safecast"
	"micro-one-api/platform/database/xdb"
	applogger "micro-one-api/platform/logging"
	appcrypto "micro-one-api/platform/security/crypto"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// now returns the current time as a UTC Unix timestamp. It is the single time
// seam for the channel data layer (review channel-L1): centralizing it makes
// the UTC basis explicit for cross-service log/debug alignment and gives tests
// one function to stub instead of 29 scattered now() call sites.
func now() int64 {
	return time.Now().UTC().Unix()
}

type Repository struct {
	routingGroupDualWrite bool
	routingGroupRelations bool
	// overrideColsReady caches the 098 column probe so old schemas (pre-F)
	// keep working without per-query migrator checks.
	overrideColsReadyOnce sync.Once
	overrideColsReady     bool
	db                    *gorm.DB
	redis                 *redis.Client
	channels              map[int64]*biz.Channel
	subAccounts           map[int64]*biz.SubscriptionAccount
	quotaEvents           map[string]biz.SubscriptionAccountQuotaEventAggregate
	usageEvents           map[string]bool
	resetRuns             map[string]bool
	lock                  sync.RWMutex
	encKey                []byte // AES key for encrypting API keys at rest (nil = no encryption)

	// Model registry memory store (方案B) — used only when db == nil.
	models                    map[int64]*biz.Model
	modelAliases              map[int64]*biz.ModelAlias
	modelChannelMappings      map[int64]*biz.ModelChannelMapping
	modelSubscriptionMappings map[int64]*biz.ModelSubscriptionMapping
	// Sprint 4: usage stats memory store
	modelUsageStats        map[int64]*biz.ModelUsageStat
	modelUsageStatNextID   int64
	modelHealthStates      map[int64]*biz.ModelHealthState
	modelHealthStateNextID int64
	// Monotonic counters for memory-mode ID generation (avoids ID reuse
	// after deletes, which len(map)+1 would cause).
	modelNextID           int64
	modelAliasNextID      int64
	modelMappingNextID    int64
	modelSubMappingNextID int64
	// P2 #3: model→account routing memory store (model_routings table).
	modelRoutings      map[int64]*biz.ModelRouting
	modelRoutingNextID int64
}

type channelModel struct {
	AuthorizationRevision             int64   `gorm:"column:authorization_revision;default:1"`
	ID                                int64   `gorm:"column:id"`
	Type                              int32   `gorm:"column:type"`
	Key                               string  `gorm:"column:key"`
	Status                            int32   `gorm:"column:status"`
	Name                              string  `gorm:"column:name"`
	Weight                            *uint   `gorm:"column:weight"`
	CreatedTime                       int64   `gorm:"column:created_time"`
	TestTime                          int64   `gorm:"column:test_time"`
	ResponseTime                      int64   `gorm:"column:response_time"`
	BaseURL                           *string `gorm:"column:base_url"`
	Balance                           float64 `gorm:"column:balance"`
	BalanceUpdatedTime                int64   `gorm:"column:balance_updated_time"`
	BalanceRefreshLastError           *string `gorm:"column:balance_refresh_last_error"`
	BalanceRefreshLastSuccessTime     int64   `gorm:"column:balance_refresh_last_success_time"`
	ConsecutiveBalanceRefreshFailures int32   `gorm:"column:consecutive_balance_refresh_failures"`
	HealthStatus                      string  `gorm:"column:health_status"`
	HealthLastError                   *string `gorm:"column:health_last_error"`
	HealthLastSuccessTime             int64   `gorm:"column:health_last_success_time"`
	HealthLastFailureTime             int64   `gorm:"column:health_last_failure_time"`
	HealthConsecutiveFailures         int32   `gorm:"column:health_consecutive_failures"`
	CircuitOpenedUntil                int64   `gorm:"column:circuit_opened_until"`
	Models                            string  `gorm:"column:models"`
	Group                             string  `gorm:"column:group"`
	UsedQuota                         int64   `gorm:"column:used_quota"`
	ModelMapping                      *string `gorm:"column:model_mapping"`
	Priority                          *int64  `gorm:"column:priority"`
	Config                            string  `gorm:"column:config"`
	SystemPrompt                      *string `gorm:"column:system_prompt"`
}

type channelUsageEventModel struct {
	ReservationID string    `gorm:"column:reservation_id;primaryKey"`
	ChannelID     int64     `gorm:"column:channel_id"`
	Quota         int64     `gorm:"column:quota"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

func (channelUsageEventModel) TableName() string { return "channel_usage_events" }

func (channelModel) TableName() string { return "channels" }

type subscriptionAccountModel struct {
	CredentialRefreshPending bool `gorm:"column:credential_refresh_pending"`

	CredentialRevision int64   `gorm:"column:credential_revision"`
	ID                 int64   `gorm:"column:id"`
	Name               string  `gorm:"column:name"`
	Platform           string  `gorm:"column:platform"`
	AccountType        string  `gorm:"column:account_type"`
	Status             int32   `gorm:"column:status"`
	Group              string  `gorm:"column:group"`
	Models             string  `gorm:"column:models"`
	Priority           int64   `gorm:"column:priority"`
	Weight             int32   `gorm:"column:weight"`
	BaseURL            *string `gorm:"column:base_url"`
	AccessToken        *string `gorm:"column:access_token"`
	RefreshToken       *string `gorm:"column:refresh_token"`
	ExpiresAt          int64   `gorm:"column:expires_at"`
	AccountID          string  `gorm:"column:account_id"`
	Fingerprint        *string `gorm:"column:fingerprint"`
	Metadata           *string `gorm:"column:metadata"`
	CreatedAt          int64   `gorm:"column:created_at"`
	UpdatedAt          int64   `gorm:"column:updated_at"`

	LastUsedAt       int64   `gorm:"column:last_used_at"`
	RateLimitedUntil int64   `gorm:"column:rate_limited_until"`
	QuotaUsedPercent float32 `gorm:"column:quota_used_percent"`
	QuotaResetAt     int64   `gorm:"column:quota_reset_at"`
	Concurrency      int32   `gorm:"column:concurrency"`

	QuotaLimitUSD          float64 `gorm:"column:quota_limit_usd"`
	QuotaUsedUSD           float64 `gorm:"column:quota_used_usd"`
	Quota5hLimitUSD        float64 `gorm:"column:quota_5h_limit_usd"`
	Quota5hUsedUSD         float64 `gorm:"column:quota_5h_used_usd"`
	Quota5hWindowStart     int64   `gorm:"column:quota_5h_window_start"`
	QuotaDailyLimitUSD     float64 `gorm:"column:quota_daily_limit_usd"`
	QuotaDailyUsedUSD      float64 `gorm:"column:quota_daily_used_usd"`
	QuotaDailyWindowStart  int64   `gorm:"column:quota_daily_window_start"`
	QuotaWeeklyLimitUSD    float64 `gorm:"column:quota_weekly_limit_usd"`
	QuotaWeeklyUsedUSD     float64 `gorm:"column:quota_weekly_used_usd"`
	QuotaWeeklyWindowStart int64   `gorm:"column:quota_weekly_window_start"`
	RateMultiplier         float64 `gorm:"column:rate_multiplier"`
	RPMLimit               int32   `gorm:"column:rpm_limit"`
	SessionWindowLimitUSD  float64 `gorm:"column:session_window_limit_usd"`
	QuotaResetStrategy     string  `gorm:"column:quota_reset_strategy"`
	QuotaTimezone          string  `gorm:"column:quota_timezone"`
	ModelMapping           string  `gorm:"column:model_mapping"`
}

func (subscriptionAccountModel) TableName() string { return "subscription_accounts" }

type accountQuotaSnapshotModel struct {
	AccountID                   int64      `gorm:"column:account_id;primaryKey"`
	PrimaryUsedPercent          *float64   `gorm:"column:primary_used_percent"`
	PrimaryResetAfterSeconds    *int32     `gorm:"column:primary_reset_after_seconds"`
	PrimaryWindowMinutes        *int32     `gorm:"column:primary_window_minutes"`
	SecondaryUsedPercent        *float64   `gorm:"column:secondary_used_percent"`
	SecondaryResetAfterSeconds  *int32     `gorm:"column:secondary_reset_after_seconds"`
	SecondaryWindowMinutes      *int32     `gorm:"column:secondary_window_minutes"`
	PrimaryOverSecondaryPercent *float64   `gorm:"column:primary_over_secondary_percent"`
	UpdatedAt                   *time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
	SnapshotPaused              bool       `gorm:"column:snapshot_paused"`
}

func (accountQuotaSnapshotModel) TableName() string { return "account_quota_snapshots" }

type subscriptionAccountQuotaEventModel struct {
	ID                    int64   `gorm:"column:id"`
	ReservationID         string  `gorm:"column:reservation_id"`
	SubscriptionAccountID int64   `gorm:"column:subscription_account_id"`
	CostSource            string  `gorm:"column:cost_source"`
	CostUSD               float64 `gorm:"column:cost_usd"`
	ChargedUSD            float64 `gorm:"column:charged_usd"`
	RateMultiplier        float64 `gorm:"column:rate_multiplier"`
	OccurredAt            int64   `gorm:"column:occurred_at"`
	CreatedAt             int64   `gorm:"column:created_at"`
}

func (subscriptionAccountQuotaEventModel) TableName() string {
	return "subscription_account_quota_events"
}

func NewRepositoryFromEnv(driver string, dsn ...string) (*Repository, error) {
	var dbDSN string
	if len(dsn) > 0 && dsn[0] != "" {
		dbDSN = dsn[0]
	} else {
		dbDSN = os.Getenv("CHANNEL_SQL_DSN")
		if dbDSN == "" {
			dbDSN = os.Getenv("SQL_DSN")
		}
	}
	if dbDSN == "" {
		if allowMemoryRepository() {
			return newMemoryRepository(), nil
		}
		return nil, fmt.Errorf("channel database DSN is required; set CHANNEL_SQL_DSN/SQL_DSN or CHANNEL_MEMORY_MODE=true for development")
	}
	key, err := encryptionKeyFromEnv()
	if err != nil {
		return nil, err
	}
	// Schema isolation (Phase 2.4): effective schema comes from the wire
	// argument, the per-service env var, or the global DATABASE_SCHEMA fallback.
	schema := xdb.ResolveSchema("", "CHANNEL_SCHEMA", "DATABASE_SCHEMA")
	if len(dsn) > 1 && dsn[1] != "" {
		schema = dsn[1]
	}
	db, err := xdb.Open(xdb.DatabaseConfig{Driver: xdb.NormalizeDriver(driver, dbDSN), DSN: dbDSN, Schema: schema})
	if err != nil {
		return nil, err
	}
	redisAddr := os.Getenv("REDIS_ADDR")
	redisPassword := os.Getenv("REDIS_PASSWORD")
	rdb := xdb.NewRedisClient(redisAddr, redisPassword)
	if rdb != nil {
		if pingErr := rdb.Ping(context.Background()).Err(); pingErr != nil {
			_ = rdb.Close()
			rdb = nil
			applogger.Log.Warn("channel-service Redis unavailable; continuing without Redis-backed features",
				zap.String("component", "channel.data"), zap.Error(pingErr))
		}
	}
	repo := &Repository{db: db, redis: rdb}
	if os.Getenv("CHANNEL_ROUTING_GROUP_DUAL_WRITE") == "true" {
		// Dual writes need both the schema and the explicit legacy backfill:
		// without the backfill the relation projection would drift from the CSVs.
		dualWriteErr := routingGroupSchemaReady(db)
		if dualWriteErr == nil && !routingGroupBackfillRecorded(db) {
			dualWriteErr = biz.ErrRoutingGroupMigrationRequired
		}
		if dualWriteErr != nil {
			if rdb != nil {
				_ = rdb.Close()
			}
			if sqlDB, e := db.DB(); e == nil {
				_ = sqlDB.Close()
			}
			return nil, dualWriteErr
		}
		repo.routingGroupDualWrite = true
	}
	repo.encKey = key
	return repo, nil
}

func allowMemoryRepository() bool {
	allowed, _ := strconv.ParseBool(os.Getenv("CHANNEL_MEMORY_MODE"))
	return allowed
}

func encryptionKeyFromEnv() ([]byte, error) {
	key := os.Getenv("CHANNEL_ENCRYPTION_KEY")
	if key == "" {
		return nil, fmt.Errorf("CHANNEL_ENCRYPTION_KEY is required for the persistent channel repository")
	}
	switch len(key) {
	case 16, 24, 32:
		return []byte(key), nil
	default:
		return nil, fmt.Errorf("CHANNEL_ENCRYPTION_KEY must be 16, 24, or 32 bytes")
	}
}

func newMemoryRepository() *Repository {
	return &Repository{
		channels:                  make(map[int64]*biz.Channel),
		subAccounts:               make(map[int64]*biz.SubscriptionAccount),
		quotaEvents:               make(map[string]biz.SubscriptionAccountQuotaEventAggregate),
		usageEvents:               make(map[string]bool),
		resetRuns:                 make(map[string]bool),
		models:                    make(map[int64]*biz.Model),
		modelAliases:              make(map[int64]*biz.ModelAlias),
		modelChannelMappings:      make(map[int64]*biz.ModelChannelMapping),
		modelSubscriptionMappings: make(map[int64]*biz.ModelSubscriptionMapping),
		modelUsageStats:           make(map[int64]*biz.ModelUsageStat),
		modelHealthStates:         make(map[int64]*biz.ModelHealthState),
		modelRoutings:             make(map[int64]*biz.ModelRouting),
	}
}

func (r *Repository) Redis() *redis.Client {
	if r == nil {
		return nil
	}
	return r.redis
}

func (r *Repository) FindByID(ctx context.Context, channelID int64) (*biz.Channel, error) {
	if r.db != nil {
		return r.findByIDDB(ctx, channelID)
	}
	return r.findByIDMemory(ctx, channelID)
}

func (r *Repository) ListChannelCandidates(ctx context.Context, group, model string) ([]biz.RoutingCandidate, error) {
	if r.db != nil {
		return r.listChannelCandidatesDB(ctx, group, model)
	}
	return r.listChannelCandidatesMemory(ctx, group, model)
}

func (r *Repository) FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error) {
	if r.db != nil {
		return r.findSubscriptionAccountByIDDB(ctx, accountID)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return nil, biz.ErrSubscriptionAccountNotFound
	}
	return cloneSubscriptionAccount(account), nil
}

func (r *Repository) ListSubscriptionCandidates(ctx context.Context, group, model, platform string) ([]biz.SubscriptionRoutingCandidate, error) {
	if r.db != nil {
		return r.listSubscriptionCandidatesDB(ctx, group, model, platform)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	managedModelPKs := make(map[int64]*biz.Model)
	for id, managed := range r.models {
		if biz.ModelIDEqual(managed.ModelID, model) {
			managedModelPKs[id] = managed
		}
	}
	candidates := make([]biz.SubscriptionRoutingCandidate, 0)
	for _, mapping := range r.modelSubscriptionMappings {
		managed, ok := managedModelPKs[mapping.ModelPK]
		if !ok || !mapping.Enabled || managed.Status != biz.ModelStatusEnabled || mapping.GroupName != group {
			continue
		}
		account, ok := r.subAccounts[mapping.SubscriptionAccountID]
		if !ok || account.Status != biz.ChannelStatusEnabled || (platform != "" && account.Platform != platform) {
			continue
		}
		priority := int64(mapping.Priority)
		if priority == 0 {
			priority = account.Priority
		}
		upstream := strings.TrimSpace(mapping.UpstreamModelID)
		if upstream == "" {
			upstream = managed.ModelID
		}
		candidates = append(candidates, biz.SubscriptionRoutingCandidate{
			Group: group, Model: managed.ModelID, Platform: account.Platform,
			AccountID: account.ID, Enabled: true, Priority: priority, UpstreamModelID: upstream,
		})
	}
	if len(candidates) == 0 {
		return nil, biz.ErrSubscriptionAccountNotFound
	}
	return candidates, nil
}

func (r *Repository) ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*biz.SubscriptionAccount, int64, error) {
	if r.db == nil {
		if _, iam := authorization.QueryScopeFromContext(ctx, "channel.account.list"); iam {
			return nil, 0, authorization.ErrDenied
		}
	}
	if r.db != nil {
		return r.listSubscriptionAccountsDB(ctx, page, pageSize, keyword, group, status, platform)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.SubscriptionAccount
	for _, account := range r.subAccounts {
		result = append(result, account)
	}
	return result, int64(len(result)), nil
}

func (r *Repository) ListOAuthRefreshCandidates(ctx context.Context, within time.Duration) ([]int64, error) {
	if r.db != nil {
		return r.listOAuthRefreshCandidatesDB(ctx, within)
	}
	threshold := time.Now().Add(within).Unix()
	r.lock.RLock()
	defer r.lock.RUnlock()
	ids := make([]int64, 0)
	for id, account := range r.subAccounts {
		if account.CredentialRefreshPending || (account.ExpiresAt > 0 && account.ExpiresAt <= threshold) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids, nil
}

func (r *Repository) CreateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	if r.db != nil {
		err := r.createSubscriptionAccountDB(ctx, account)
		if err == nil {
			account.RecoveryBaseline = new(account.RecoveryState())
		}
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account.ID = int64(len(r.subAccounts) + 1)
	if account.CredentialRevision < 1 {
		account.CredentialRevision = 1
	}
	account.RecoveryBaseline = new(account.RecoveryState())
	r.subAccounts[account.ID] = cloneSubscriptionAccount(account)
	r.syncSubscriptionAccountModelMappingsMemoryLocked(account)
	return nil
}

func (r *Repository) UpdateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	if r.db != nil {
		return r.updateSubscriptionAccountDB(ctx, account)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	previous, ok := r.subAccounts[account.ID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	if previous.CredentialRevision != account.CredentialRevision {
		return biz.ErrCredentialConflict
	}
	if account.RecoveryBaseline != nil && previous.RecoveryState() != *account.RecoveryBaseline {
		return biz.ErrAccountRecoveryStateChanged
	}
	if previous.CredentialRefreshPending && previous.RefreshToken == account.RefreshToken {
		return biz.ErrCredentialConflict
	}
	cp := *cloneSubscriptionAccount(account)
	cp.CredentialRevision++
	cp.CredentialRefreshPending = false
	cp.RecoveryBaseline = new(cp.RecoveryState())
	r.subAccounts[account.ID] = &cp
	r.syncSubscriptionAccountModelMappingsMemoryLocked(&cp)
	account.CredentialRevision = cp.CredentialRevision
	account.CredentialRefreshPending = false
	account.RecoveryBaseline = cp.RecoveryBaseline
	return nil
}

func (r *Repository) DeleteSubscriptionAccount(ctx context.Context, accountID int64) error {
	if r.db != nil {
		return r.deleteSubscriptionAccountDB(ctx, accountID)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.subAccounts, accountID)
	for id, m := range r.modelSubscriptionMappings {
		if m != nil && m.SubscriptionAccountID == accountID {
			delete(r.modelSubscriptionMappings, id)
		}
	}
	return nil
}

func (r *Repository) ChangeSubscriptionAccountStatus(ctx context.Context, accountID int64, status int32) error {
	if r.db != nil {
		return r.changeSubscriptionAccountStatusDB(ctx, accountID, status)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	account.Status = status
	return nil
}

func (r *Repository) SetSubscriptionAccountError(ctx context.Context, accountID int64, message string) error {
	if r.db != nil {
		return r.setSubscriptionAccountErrorDB(ctx, accountID, message)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	account.LastError = message
	account.Metadata = subscriptionAccountErrorMetadata(account.Metadata, message)
	account.UpdatedAt = now()
	return nil
}

func (r *Repository) SetTempUnschedulable(ctx context.Context, accountID int64, until time.Time, reason string) error {
	if r.db != nil {
		return r.setTempUnschedulableDB(ctx, accountID, until, reason)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	account.RateLimitedUntil = until.Unix()
	account.LastError = reason
	account.Metadata = stampRecoveryMetadata(account.Metadata, reason, until.Unix(), now())
	account.UpdatedAt = now()
	return nil
}

func (r *Repository) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	if r.db != nil {
		return r.clearTempUnschedulableDB(ctx, accountID)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	account.RateLimitedUntil = 0
	account.UpdatedAt = now()
	return nil
}

func (r *Repository) RecordAccountQuotaSnapshot(ctx context.Context, snapshot *biz.AccountQuotaSnapshot) error {
	if snapshot == nil || snapshot.AccountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	if r.db != nil {
		return r.recordAccountQuotaSnapshotDB(ctx, snapshot)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[snapshot.AccountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	if snapshot.PrimaryUsedPercent != nil {
		usedPercent, err := safecast.Float64ToFloat32(*snapshot.PrimaryUsedPercent)
		if err != nil {
			return fmt.Errorf("primary used percent: %w", err)
		}
		account.QuotaUsedPercent = usedPercent
	}
	resetAt := quotaResetAt(snapshot.UpdatedAt, snapshot.PrimaryResetAfterSeconds, snapshot.SecondaryResetAfterSeconds)
	if resetAt > 0 {
		account.QuotaResetAt = resetAt
	}
	applyAccountQuotaSnapshot(account, snapshot)
	if snapshot.SnapshotPaused {
		account.Status = biz.ChannelStatusDisabled
	}
	return nil
}

func (r *Repository) GetAccountQuotaSnapshot(ctx context.Context, accountID int64) (*biz.AccountQuotaSnapshot, error) {
	if r.db != nil {
		return r.getAccountQuotaSnapshotDB(ctx, accountID)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return nil, biz.ErrSubscriptionAccountNotFound
	}
	used := float64(account.QuotaUsedPercent)
	if account.PrimaryQuotaUsedPercent != nil {
		used = *account.PrimaryQuotaUsedPercent
	}
	var resetAfter *int32
	if account.QuotaResetAt > 0 {
		if value, err := safecast.Int64ToInt32(account.QuotaResetAt - now()); err == nil {
			resetAfter = &value
		}
	}
	if account.PrimaryQuotaResetAfterSeconds != nil {
		resetAfter = account.PrimaryQuotaResetAfterSeconds
	}
	return &biz.AccountQuotaSnapshot{
		AccountID:                   accountID,
		PrimaryUsedPercent:          &used,
		PrimaryResetAfterSeconds:    resetAfter,
		PrimaryWindowMinutes:        account.PrimaryQuotaWindowMinutes,
		SecondaryUsedPercent:        account.SecondaryQuotaUsedPercent,
		SecondaryResetAfterSeconds:  account.SecondaryQuotaResetAfterSeconds,
		SecondaryWindowMinutes:      account.SecondaryQuotaWindowMinutes,
		PrimaryOverSecondaryPercent: account.PrimaryOverSecondaryPercent,
		UpdatedAt:                   time.Now(),
		SnapshotPaused:              account.Status != biz.ChannelStatusEnabled,
	}, nil
}

func (r *Repository) RecordSubscriptionAccountQuotaUsage(ctx context.Context, usage biz.SubscriptionAccountQuotaUsage) error {
	if usage.AccountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	if usage.CostUSD <= 0 {
		return nil
	}
	usage.ReservationID = strings.TrimSpace(usage.ReservationID)
	usage.CostSource = strings.TrimSpace(usage.CostSource)
	if usage.CostSource == "" {
		usage.CostSource = "billing_commit"
	}
	if usage.OccurredAt.IsZero() {
		usage.OccurredAt = time.Now()
	}
	if r.db != nil {
		return r.recordSubscriptionAccountQuotaUsageDB(ctx, usage)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[usage.AccountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	if usage.ReservationID != "" {
		key := subscriptionAccountQuotaEventKey(usage.ReservationID, usage.AccountID, usage.CostSource)
		if _, ok := r.quotaEvents[key]; ok {
			return nil
		}
		if r.quotaEvents == nil {
			r.quotaEvents = make(map[string]biz.SubscriptionAccountQuotaEventAggregate)
		}
		multiplier := account.EffectiveRateMultiplier()
		r.quotaEvents[key] = biz.SubscriptionAccountQuotaEventAggregate{
			SubscriptionAccountID: usage.AccountID,
			CostUSD:               usage.CostUSD,
			ChargedUSD:            usage.CostUSD * multiplier,
			AverageRateMultiplier: multiplier,
			Count:                 1,
			LastOccurredAt:        usage.OccurredAt.Unix(),
		}
	}
	applySubscriptionAccountQuotaUsage(account, usage.CostUSD, usage.OccurredAt)
	return nil
}

func (r *Repository) AggregateSubscriptionAccountQuotaEvents(ctx context.Context, filter biz.SubscriptionAccountQuotaEventFilter) ([]*biz.SubscriptionAccountQuotaEventAggregate, error) {
	if r.db == nil {
		if _, scoped := authorization.QueryScopeFromContext(ctx, "channel.account.list"); scoped {
			return nil, authorization.ErrDenied
		}
	}
	if r.db != nil {
		return r.aggregateSubscriptionAccountQuotaEventsDB(ctx, filter)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	byAccount := map[int64]*biz.SubscriptionAccountQuotaEventAggregate{}
	for _, event := range r.quotaEvents {
		if filter.AccountID > 0 && event.SubscriptionAccountID != filter.AccountID {
			continue
		}
		if !filter.StartTime.IsZero() && event.LastOccurredAt < filter.StartTime.Unix() {
			continue
		}
		if !filter.EndTime.IsZero() && event.LastOccurredAt > filter.EndTime.Unix() {
			continue
		}
		row := byAccount[event.SubscriptionAccountID]
		if row == nil {
			row = &biz.SubscriptionAccountQuotaEventAggregate{SubscriptionAccountID: event.SubscriptionAccountID}
			byAccount[event.SubscriptionAccountID] = row
		}
		row.CostUSD += event.CostUSD
		row.ChargedUSD += event.ChargedUSD
		row.AverageRateMultiplier += event.AverageRateMultiplier
		row.Count += event.Count
		if event.LastOccurredAt > row.LastOccurredAt {
			row.LastOccurredAt = event.LastOccurredAt
		}
	}
	out := make([]*biz.SubscriptionAccountQuotaEventAggregate, 0, len(byAccount))
	for _, row := range byAccount {
		if row.Count > 0 {
			row.AverageRateMultiplier = row.AverageRateMultiplier / float64(row.Count)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ChargedUSD > out[j].ChargedUSD
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

func (r *Repository) ResetSubscriptionAccountQuota(ctx context.Context, accountID int64, scope string) error {
	if accountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		scope = "all"
	}
	if r.db != nil {
		return r.resetSubscriptionAccountQuotaDB(ctx, accountID, scope)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	resetSubscriptionAccountQuota(account, scope)
	return nil
}

func (r *Repository) AutoPauseAccount(ctx context.Context, accountID int64, reason string) error {
	if r.db != nil {
		return r.autoPauseAccountDB(ctx, accountID, reason)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	account.LastError = reason
	// Derive the recovery policy from the reason (review H1/H2 fix). Codex
	// snapshot exhaustion and local quota exhaustion are transient — the
	// account stays ENABLED (status=enabled) so the recovery sweeper can clear
	// the unschedulable markers once the upstream snapshot / local window
	// resets. Only authorization errors (401/403, policy=manual) disable the
	// account, because those never auto-recover and require an OAuth rebind.
	account.Metadata = stampRecoveryMetadata(account.Metadata, reason, 0, now())
	policy := subscriptionAccountMetadataValue(account.Metadata, "recovery_policy")
	if policy == biz.RecoveryPolicyManual {
		account.Status = biz.ChannelStatusDisabled
	}
	account.UpdatedAt = now()
	return nil
}

func (r *Repository) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	if r.db != nil {
		return r.listAvailableModelsDB(ctx, group)
	}
	return r.listAvailableModelsMemory(ctx, group)
}

func (r *Repository) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*biz.Channel, int64, error) {
	if r.db == nil {
		if _, iam := authorization.QueryScopeFromContext(ctx, "channel.channel.list"); iam {
			return nil, 0, authorization.ErrDenied
		}
	}
	if r.db != nil {
		return r.listChannelsDB(ctx, page, pageSize, keyword, group, status, chType)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	var result []*biz.Channel
	for _, ch := range r.channels {
		result = append(result, ch)
	}
	return result, int64(len(result)), nil
}

func (r *Repository) CreateChannel(ctx context.Context, channel *biz.Channel) error {
	if r.db != nil {
		return r.createChannelDB(ctx, channel)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	channel.ID = int64(len(r.channels) + 1)
	r.channels[channel.ID] = channel
	r.syncChannelModelMappingsMemoryLocked(channel)
	return nil
}

func (r *Repository) UpdateChannel(ctx context.Context, channel *biz.Channel) error {
	if r.db != nil {
		return r.updateChannelDB(ctx, channel)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.channels[channel.ID]; !ok {
		return biz.ErrChannelNotFound
	}
	r.channels[channel.ID] = channel
	r.syncChannelModelMappingsMemoryLocked(channel)
	return nil
}

func (r *Repository) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	if quota <= 0 {
		return nil
	}
	if r.db != nil {
		return r.recordUsageDB(ctx, channelID, quota)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	channel, ok := r.channels[channelID]
	if !ok {
		return biz.ErrChannelNotFound
	}
	channel.UsedQuota += quota
	return nil
}

// RecordUsageOnce increments a channel counter at most once per billing
// reservation. The dedupe claim and counter update share one transaction, so
// a failed update does not consume the retry key.
func (r *Repository) RecordUsageOnce(ctx context.Context, reservationID string, channelID int64, quota int64) error {
	if reservationID == "" {
		return r.RecordUsage(ctx, channelID, quota)
	}
	if quota <= 0 {
		return nil
	}
	if r.db != nil {
		err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			claim := &channelUsageEventModel{ReservationID: reservationID, ChannelID: channelID, Quota: quota, CreatedAt: time.Now().UTC()}
			if err := tx.Create(claim).Error; err != nil {
				return err
			}
			updated := tx.Model(&channelModel{}).Where("id = ?", channelID).
				UpdateColumn("used_quota", gorm.Expr("used_quota + ?", quota))
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected == 0 {
				return biz.ErrChannelNotFound
			}
			return nil
		})
		if isDuplicateKeyErr(err) {
			return nil
		}
		return err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.usageEvents == nil {
		r.usageEvents = make(map[string]bool)
	}
	if r.usageEvents[reservationID] {
		return nil
	}
	channel, ok := r.channels[channelID]
	if !ok {
		return biz.ErrChannelNotFound
	}
	channel.UsedQuota += quota
	r.usageEvents[reservationID] = true
	return nil
}

func (r *Repository) RecordHealth(ctx context.Context, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*biz.Channel, error) {
	if r.db != nil {
		return r.recordHealthDB(ctx, event, threshold, cooldown)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	channel, ok := r.channels[event.ChannelID]
	if !ok {
		return nil, biz.ErrChannelNotFound
	}
	if event.ConfigurationRevision != "" && event.ConfigurationRevision != channel.UpstreamRevision() {
		return nil, biz.ErrChannelNotFound
	}
	applyHealthEvent(channel, event, threshold, cooldown)
	cloned := *channel
	cloned.Models = append([]string(nil), channel.Models...)
	return &cloned, nil
}

func (r *Repository) DeleteChannel(ctx context.Context, channelID int64) error {
	if r.db != nil {
		return r.deleteChannelDB(ctx, channelID)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.channels, channelID)
	for id, m := range r.modelChannelMappings {
		if m != nil && m.ChannelID == channelID {
			delete(r.modelChannelMappings, id)
		}
	}
	return nil
}

func (r *Repository) ChangeStatus(ctx context.Context, channelID int64, status int32) error {
	if r.db != nil {
		return r.changeStatusDB(ctx, channelID, status)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	ch, ok := r.channels[channelID]
	if !ok {
		return biz.ErrChannelNotFound
	}
	ch.Status = status
	return nil
}

func (r *Repository) findSubscriptionAccountByIDDB(ctx context.Context, accountID int64) (*biz.SubscriptionAccount, error) {
	var model subscriptionAccountModel
	if err := r.db.WithContext(ctx).Where("id = ?", accountID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionAccountNotFound
		}
		return nil, err
	}
	return r.subscriptionAccountModelToBiz(&model)
}

func (r *Repository) listSubscriptionCandidatesDB(ctx context.Context, group, model, platform string) ([]biz.SubscriptionRoutingCandidate, error) {
	candidates, err := r.listRegistrySubscriptionAbilitiesDB(ctx, group, model, platform)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, biz.ErrSubscriptionAccountNotFound
	}
	return candidates, nil
}

func (r *Repository) listSubscriptionAccountsDB(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*biz.SubscriptionAccount, int64, error) {
	query := r.db.WithContext(ctx).Model(&subscriptionAccountModel{})
	var err error
	if query, err = r.subscriptionAccountScope(ctx, query, "subscription_accounts"); err != nil {
		return nil, 0, err
	}
	if keyword != "" {
		query = query.Where("name LIKE ? ESCAPE '!' OR account_id LIKE ? ESCAPE '!'", "%"+escapeLike(keyword)+"%", "%"+escapeLike(keyword)+"%")
	}
	if group != "" {
		query = query.Where(r.routingGroupSQL("`group` = ?"), group)
	}
	if status != 0 {
		query = query.Where("status = ?", status)
	}
	if platform != "" {
		query = query.Where("platform = ?", platform)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []subscriptionAccountModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.SubscriptionAccount, len(models))
	for i, m := range models {
		account, err := r.subscriptionAccountModelToBiz(&m)
		if err != nil {
			return nil, 0, err
		}
		result[i] = account
	}
	if err := r.attachAccountQuotaSnapshots(ctx, result); err != nil {
		return nil, 0, err
	}
	return result, total, nil
}

func (r *Repository) attachAccountQuotaSnapshots(ctx context.Context, accounts []*biz.SubscriptionAccount) error {
	if len(accounts) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(accounts))
	accountByID := make(map[int64]*biz.SubscriptionAccount, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		ids = append(ids, account.ID)
		accountByID[account.ID] = account
	}
	if len(ids) == 0 {
		return nil
	}
	var snapshots []accountQuotaSnapshotModel
	if err := r.db.WithContext(ctx).Where("account_id IN ?", ids).Find(&snapshots).Error; err != nil {
		return err
	}
	for i := range snapshots {
		account := accountByID[snapshots[i].AccountID]
		if account == nil {
			continue
		}
		applyAccountQuotaSnapshot(account, accountQuotaSnapshotModelToBiz(&snapshots[i]))
	}
	return nil
}

func (r *Repository) listOAuthRefreshCandidatesDB(ctx context.Context, within time.Duration) ([]int64, error) {
	threshold := time.Now().Add(within).Unix()
	var rows []subscriptionAccountModel
	if err := r.db.WithContext(ctx).
		Select("id").
		Where("credential_refresh_pending = ? OR (expires_at > 0 AND expires_at <= ?)", true, threshold).
		Order("expires_at ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

func (r *Repository) createSubscriptionAccountDB(ctx context.Context, account *biz.SubscriptionAccount) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, 0, account.Group, true, "channel.account.create", "channel.account.oauth.bind"); err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, "channel.account.create"); iam && account.RateMultiplier != 0 && account.RateMultiplier != 1 {
			if err := authorization.Require(ctx, "billing.pricing.update", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
				return err
			}
			if err := authzquery.AppendWriteAudit(ctx, tx, "billing.pricing.update", 0); err != nil {
				return err
			}
		}
		if err := r.checkCompatibilityMappingEdit(ctx, tx, 0, account.Group, true, "", account.ModelMapping); err != nil {
			return err
		}
		model, err := r.subscriptionAccountBizToModel(account)
		if err != nil {
			return err
		}
		if model.CredentialRevision < 1 {
			model.CredentialRevision = 1
		}
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		account.ID = model.ID
		account.CredentialRevision = model.CredentialRevision
		for _, op := range []string{"channel.account.create", "channel.account.oauth.bind"} {
			if err := authzquery.AppendWriteAudit(ctx, tx, op, model.ID); err != nil {
				return err
			}
		}
		return r.syncSubscriptionAccountModelMappingsTx(tx, account)
	})
}

func (r *Repository) updateSubscriptionAccountDB(ctx context.Context, account *biz.SubscriptionAccount) error {
	old, err := r.FindSubscriptionAccountByID(ctx, account.ID)
	if err != nil {
		return err
	}
	// The CAS below protects this read without a SQLite read-to-write upgrade.
	// Metadata edits must not make a potentially consumed token reusable.
	if old.CredentialRevision != account.CredentialRevision || (old.CredentialRefreshPending && old.RefreshToken == account.RefreshToken) {
		return biz.ErrCredentialConflict
	}
	err = authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, account.ID, account.Group, true, "channel.account.update"); err != nil {
			return err
		}
		if account.RecoveryBaseline != nil {
			var current subscriptionAccountModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, account.ID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return biz.ErrSubscriptionAccountNotFound
				}
				return err
			}
			currentAccount, err := r.subscriptionAccountModelToBiz(&current)
			if err != nil {
				return err
			}
			if currentAccount.RecoveryState() != *account.RecoveryBaseline {
				return biz.ErrAccountRecoveryStateChanged
			}
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, "channel.account.update"); iam {
			owner := &Repository{db: tx, encKey: r.encKey}
			current, err := owner.FindSubscriptionAccountByID(ctx, account.ID)
			if err != nil {
				return err
			}
			if current.RateMultiplier != account.RateMultiplier {
				if err := authorization.Require(ctx, "billing.pricing.update", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
					return err
				}
				if err := authzquery.AppendWriteAudit(ctx, tx, "billing.pricing.update", account.ID); err != nil {
					return err
				}
			}

			if current.ModelMapping != account.ModelMapping {
				if err := r.checkCompatibilityMappingEdit(ctx, tx, account.ID, account.Group, true, current.ModelMapping, account.ModelMapping); err != nil {
					return err
				}
			}

			if current.Status != account.Status {
				operation := "channel.account.disable"
				if account.Status == 1 {
					operation = "channel.account.enable"
				}
				if err := r.checkResourceTx(ctx, tx, account.ID, account.Group, true, operation); err != nil {
					return err
				}
			}
			if current.QuotaUsedUSD != account.QuotaUsedUSD || current.Quota5hUsedUSD != account.Quota5hUsedUSD || current.QuotaDailyUsedUSD != account.QuotaDailyUsedUSD || current.QuotaWeeklyUsedUSD != account.QuotaWeeklyUsedUSD {
				if err := r.checkResourceTx(ctx, tx, account.ID, account.Group, true, "channel.account.quota.reset"); err != nil {
					return err
				}
			}

			if current.AccessToken != account.AccessToken || current.RefreshToken != account.RefreshToken || current.Fingerprint != account.Fingerprint || current.Metadata != account.Metadata {
				if err := r.checkResourceTx(ctx, tx, account.ID, account.Group, true, "channel.account.credential.update"); err != nil {
					return err
				}
			}
		}

		model, err := r.subscriptionAccountBizToModel(account)
		if err != nil {
			return err
		}
		result := tx.Model(&subscriptionAccountModel{}).Where("id = ? AND credential_revision = ?", account.ID, account.CredentialRevision).Updates(map[string]any{
			"credential_refresh_pending": false,

			"credential_revision":       gorm.Expr("credential_revision + 1"),
			"name":                      model.Name,
			"platform":                  model.Platform,
			"account_type":              model.AccountType,
			"status":                    model.Status,
			"group":                     model.Group,
			"models":                    model.Models,
			"priority":                  model.Priority,
			"base_url":                  model.BaseURL,
			"access_token":              model.AccessToken,
			"refresh_token":             model.RefreshToken,
			"expires_at":                model.ExpiresAt,
			"account_id":                model.AccountID,
			"fingerprint":               model.Fingerprint,
			"metadata":                  model.Metadata,
			"quota_limit_usd":           model.QuotaLimitUSD,
			"quota_used_usd":            model.QuotaUsedUSD,
			"quota_5h_limit_usd":        model.Quota5hLimitUSD,
			"quota_5h_used_usd":         model.Quota5hUsedUSD,
			"quota_5h_window_start":     model.Quota5hWindowStart,
			"quota_daily_limit_usd":     model.QuotaDailyLimitUSD,
			"quota_daily_used_usd":      model.QuotaDailyUsedUSD,
			"quota_daily_window_start":  model.QuotaDailyWindowStart,
			"quota_weekly_limit_usd":    model.QuotaWeeklyLimitUSD,
			"quota_weekly_used_usd":     model.QuotaWeeklyUsedUSD,
			"quota_weekly_window_start": model.QuotaWeeklyWindowStart,
			"rate_multiplier":           model.RateMultiplier,
			"rpm_limit":                 model.RPMLimit,
			"session_window_limit_usd":  model.SessionWindowLimitUSD,
			"quota_reset_strategy":      model.QuotaResetStrategy,
			"quota_timezone":            model.QuotaTimezone,
			"model_mapping":             model.ModelMapping,
			"updated_at":                model.UpdatedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return biz.ErrCredentialConflict
		}
		return r.syncSubscriptionAccountModelMappingsTx(tx, account)
	})
	if err == nil {
		account.CredentialRevision++
		account.CredentialRefreshPending = false
		account.LastError = subscriptionAccountMetadataValue(account.Metadata, "last_error")
		account.RecoveryBaseline = new(account.RecoveryState())
	}
	return err
}

func (r *Repository) deleteSubscriptionAccountDB(ctx context.Context, accountID int64) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, accountID, "", true, "channel.account.delete"); err != nil {
			return err
		}
		if err := requireSourceMappingDeletes(tx, r, accountID, true); err != nil {
			return err
		}
		if err := r.syncRoutingMembersTx(tx, routing.Source{Kind: routing.Subscription, ID: accountID}, ""); err != nil {
			return err
		}
		if err := tx.Where("id = ?", accountID).Delete(&subscriptionAccountModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("subscription_account_id = ?", accountID).Delete(&modelSubscriptionMappingModel{}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ?", accountID).Delete(&accountQuotaSnapshotModel{}).Error
	})
}

func (r *Repository) changeSubscriptionAccountStatusDB(ctx context.Context, accountID int64, status int32) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, accountID, "", true, "channel.account.enable", "channel.account.disable"); err != nil {
			return err
		}
		if err := tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Update("status", status).Error; err != nil {
			return err
		}
		return nil
	})
}

func (r *Repository) setSubscriptionAccountErrorDB(ctx context.Context, accountID int64, message string) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, accountID, "", true, "channel.account.recovery.clear"); err != nil {
			return err
		}
		var row subscriptionAccountModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		metadata := subscriptionAccountErrorMetadata(derefString(row.Metadata), message)
		return tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]any{
			"metadata": new(metadata), "updated_at": now(),
		}).Error
	})
}

func (r *Repository) setTempUnschedulableDB(ctx context.Context, accountID int64, until time.Time, reason string) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var row subscriptionAccountModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		metadata := stampRecoveryMetadata(derefString(row.Metadata), reason, until.Unix(), now())
		return tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]any{
			"rate_limited_until": until.Unix(),
			"metadata":           new(metadata),
			"updated_at":         now(),
		}).Error
	})
}

func (r *Repository) clearTempUnschedulableDB(ctx context.Context, accountID int64) error {
	return r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]any{
		"rate_limited_until": 0,
		"updated_at":         now(),
	}).Error
}

func (r *Repository) recordAccountQuotaSnapshotDB(ctx context.Context, snapshot *biz.AccountQuotaSnapshot) error {
	model := accountQuotaSnapshotBizToModel(snapshot)
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		// Lock the account before its snapshot, as recovery and pause do. The
		// snapshot and recovery check then observe one committed account state.
		var account subscriptionAccountModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, snapshot.AccountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		if err := tx.Save(model).Error; err != nil {
			return err
		}
		updates := map[string]any{
			"updated_at": now(),
		}
		if snapshot.PrimaryUsedPercent != nil {
			updates["quota_used_percent"] = *snapshot.PrimaryUsedPercent
		}
		if resetAt := quotaResetAt(snapshot.UpdatedAt, snapshot.PrimaryResetAfterSeconds, snapshot.SecondaryResetAfterSeconds); resetAt > 0 {
			updates["quota_reset_at"] = resetAt
		}
		if snapshot.SnapshotPaused {
			updates["status"] = biz.ChannelStatusDisabled
		}
		result := tx.Model(&subscriptionAccountModel{}).Where("id = ?", snapshot.AccountID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		// Existence was proved under the account lock above. MySQL reports
		// zero changed rows for a secondary-only snapshot in the same second;
		// that must still commit the snapshot rather than return NotFound.
		return nil
	})
}

func (r *Repository) getAccountQuotaSnapshotDB(ctx context.Context, accountID int64) (*biz.AccountQuotaSnapshot, error) {
	var model accountQuotaSnapshotModel
	if err := r.db.WithContext(ctx).Where("account_id = ?", accountID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrSubscriptionAccountNotFound
		}
		return nil, err
	}
	return accountQuotaSnapshotModelToBiz(&model), nil
}

func (r *Repository) recordSubscriptionAccountQuotaUsageDB(ctx context.Context, usage biz.SubscriptionAccountQuotaUsage) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model subscriptionAccountModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", usage.AccountID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		account, err := r.subscriptionAccountModelToBiz(&model)
		if err != nil {
			return err
		}
		chargedUSD := usage.CostUSD * account.EffectiveRateMultiplier()
		if usage.ReservationID != "" {
			event := subscriptionAccountQuotaEventModel{
				ReservationID:         usage.ReservationID,
				SubscriptionAccountID: usage.AccountID,
				CostSource:            usage.CostSource,
				CostUSD:               usage.CostUSD,
				ChargedUSD:            chargedUSD,
				RateMultiplier:        account.EffectiveRateMultiplier(),
				OccurredAt:            usage.OccurredAt.Unix(),
				CreatedAt:             now(),
			}
			result := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "reservation_id"}, {Name: "subscription_account_id"}, {Name: "cost_source"}},
				DoNothing: true,
			}).Create(&event)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				return nil
			}
		}
		applySubscriptionAccountQuotaUsage(account, usage.CostUSD, usage.OccurredAt)
		return tx.Model(&subscriptionAccountModel{}).Where("id = ?", usage.AccountID).Updates(map[string]any{
			"quota_used_usd":            account.QuotaUsedUSD,
			"quota_5h_used_usd":         account.Quota5hUsedUSD,
			"quota_5h_window_start":     account.Quota5hWindowStart,
			"quota_daily_used_usd":      account.QuotaDailyUsedUSD,
			"quota_daily_window_start":  account.QuotaDailyWindowStart,
			"quota_weekly_used_usd":     account.QuotaWeeklyUsedUSD,
			"quota_weekly_window_start": account.QuotaWeeklyWindowStart,
			"last_used_at":              usage.OccurredAt.Unix(),
			"updated_at":                now(),
		}).Error
	})
}

func (r *Repository) aggregateSubscriptionAccountQuotaEventsDB(ctx context.Context, filter biz.SubscriptionAccountQuotaEventFilter) ([]*biz.SubscriptionAccountQuotaEventAggregate, error) {
	type aggregateRow struct {
		SubscriptionAccountID int64   `gorm:"column:subscription_account_id"`
		CostUSD               float64 `gorm:"column:cost_usd"`
		ChargedUSD            float64 `gorm:"column:charged_usd"`
		AverageRateMultiplier float64 `gorm:"column:average_rate_multiplier"`
		Count                 int64   `gorm:"column:count"`
		LastOccurredAt        int64   `gorm:"column:last_occurred_at"`
	}
	q := r.db.WithContext(ctx).Model(&subscriptionAccountQuotaEventModel{}).
		Select(`
			subscription_account_id,
			COALESCE(SUM(cost_usd), 0) AS cost_usd,
			COALESCE(SUM(charged_usd), 0) AS charged_usd,
			COALESCE(AVG(subscription_account_quota_events.rate_multiplier), 0) AS average_rate_multiplier,
			COUNT(*) AS count,
			COALESCE(MAX(occurred_at), 0) AS last_occurred_at`).
		Group("subscription_account_id").
		Order("charged_usd DESC")
	if _, scoped := authorization.QueryScopeFromContext(ctx, "channel.account.list"); scoped {
		q = q.Joins("JOIN subscription_accounts ON subscription_accounts.id = subscription_account_quota_events.subscription_account_id")
		var err error
		q, err = r.subscriptionAccountScope(ctx, q, "subscription_accounts")
		if err != nil {
			return nil, err
		}
	}
	if filter.AccountID > 0 {
		q = q.Where("subscription_account_id = ?", filter.AccountID)
	}
	if !filter.StartTime.IsZero() {
		q = q.Where("occurred_at >= ?", filter.StartTime.Unix())
	}
	if !filter.EndTime.IsZero() {
		q = q.Where("occurred_at <= ?", filter.EndTime.Unix())
	}
	if filter.Limit > 0 {
		q = q.Limit(filter.Limit)
	}
	var rows []aggregateRow
	if err := q.Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]*biz.SubscriptionAccountQuotaEventAggregate, 0, len(rows))
	for _, row := range rows {
		out = append(out, &biz.SubscriptionAccountQuotaEventAggregate{
			SubscriptionAccountID: row.SubscriptionAccountID,
			CostUSD:               row.CostUSD,
			ChargedUSD:            row.ChargedUSD,
			AverageRateMultiplier: row.AverageRateMultiplier,
			Count:                 row.Count,
			LastOccurredAt:        row.LastOccurredAt,
		})
	}
	return out, nil
}

func (r *Repository) resetSubscriptionAccountQuotaDB(ctx context.Context, accountID int64, scope string) error {
	updates := subscriptionAccountQuotaResetUpdates(scope)
	if updates == nil {
		return nil
	}
	updates["updated_at"] = now()
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, accountID, "", true, "channel.account.quota.reset"); err != nil {
			return err
		}
		result := tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		// MySQL returns zero for an identical reset; prove existence under lock.
		if result.RowsAffected == 0 {
			var row subscriptionAccountModel
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, accountID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return biz.ErrSubscriptionAccountNotFound
				}
				return err
			}
		}
		return nil
	})
}

func (r *Repository) autoPauseAccountDB(ctx context.Context, accountID int64, reason string) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var account subscriptionAccountModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&account, accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		metadata := stampRecoveryMetadata(derefString(account.Metadata), reason, 0, now())
		policy := subscriptionAccountMetadataValue(metadata, "recovery_policy")
		// A transient pause must not undo an administrative disable.
		status := account.Status
		if policy == biz.RecoveryPolicyManual {
			status = biz.ChannelStatusDisabled
		}
		if err := tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]any{
			"status":     status,
			"metadata":   new(metadata),
			"updated_at": now(),
		}).Error; err != nil {
			return err
		}
		return tx.Model(&accountQuotaSnapshotModel{}).Where("account_id = ?", accountID).Updates(map[string]any{
			"snapshot_paused": true,
			"updated_at":      time.Now(),
		}).Error
	})
}

func (r *Repository) recordUsageDB(ctx context.Context, channelID int64, quota int64) error {
	tx := r.db.WithContext(ctx).Model(&channelModel{}).
		Where("id = ?", channelID).
		UpdateColumn("used_quota", gorm.Expr("used_quota + ?", quota))
	if tx.Error != nil {
		return tx.Error
	}
	if tx.RowsAffected == 0 {
		return biz.ErrChannelNotFound
	}
	return nil
}

func (r *Repository) recordHealthDB(ctx context.Context, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) (*biz.Channel, error) {
	var updated *biz.Channel
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model channelModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", event.ChannelID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrChannelNotFound
			}
			return err
		}
		channel, err := r.modelToChannel(&model)
		if err != nil {
			return err
		}
		if event.ConfigurationRevision != "" && event.ConfigurationRevision != channel.UpstreamRevision() {
			return biz.ErrChannelNotFound
		}
		applyHealthEvent(channel, event, threshold, cooldown)
		if err := tx.Model(&channelModel{}).Where("id = ?", event.ChannelID).Updates(map[string]any{
			"test_time":                   channel.TestTime,
			"response_time":               channel.ResponseTime,
			"health_status":               channel.EffectiveHealthStatus(),
			"health_last_error":           new(channel.HealthLastError),
			"health_last_success_time":    channel.HealthLastSuccessTime,
			"health_last_failure_time":    channel.HealthLastFailureTime,
			"health_consecutive_failures": channel.HealthConsecutiveFailures,
			"circuit_opened_until":        channel.CircuitOpenedUntil,
		}).Error; err != nil {
			return err
		}
		updated = channel
		return nil
	})
	return updated, err
}

func (r *Repository) findByIDDB(ctx context.Context, channelID int64) (*biz.Channel, error) {
	var model channelModel
	if err := r.db.WithContext(ctx).Where("id = ?", channelID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrChannelNotFound
		}
		return nil, err
	}
	return r.modelToChannel(&model)
}

func (r *Repository) listChannelCandidatesDB(ctx context.Context, group, model string) ([]biz.RoutingCandidate, error) {
	candidates, err := r.listRegistryChannelAbilitiesDB(ctx, group, model)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, biz.ErrChannelNotFound
	}
	return candidates, nil
}

type registryChannelRoutingCandidateRow struct {
	ChannelID       int64  `gorm:"column:channel_id"`
	Model           string `gorm:"column:model"`
	UpstreamModelID string `gorm:"column:upstream_model_id"`
	MappingPriority int64  `gorm:"column:mapping_priority"`
	SourcePriority  int64  `gorm:"column:source_priority"`
}

func (r *Repository) listRegistryChannelAbilitiesDB(ctx context.Context, group, model string) ([]biz.RoutingCandidate, error) {
	var rows []registryChannelRoutingCandidateRow
	query := r.db.WithContext(ctx).Table("model_channel_mapping AS mcm").
		Select("mcm.channel_id, m.model_id AS model, mcm.upstream_model_id, mcm.priority AS mapping_priority, COALESCE(c.priority, 0) AS source_priority").
		Joins("JOIN models AS m ON m.id = mcm.model_id").
		Joins("JOIN channels AS c ON c.id = mcm.channel_id").
		Where("LOWER(m.model_id) = ? AND mcm.enabled = ? AND m.status = ? AND c.status = ?", strings.ToLower(model), true, biz.ModelStatusEnabled, biz.ChannelStatusEnabled).
		Scopes(func(q *gorm.DB) *gorm.DB { return r.csvGroupScope(q, group, "c.`group`", "c.id") })
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	overrides, err := r.relationOverrides(ctx, group, false)
	if err != nil {
		return nil, err
	}
	out := make([]biz.RoutingCandidate, 0, len(rows))
	for _, row := range rows {
		priority := row.MappingPriority
		if priority == 0 {
			priority = row.SourcePriority
		}
		var weight int64
		// Complete chain: relation override > mapping priority > source priority.
		priority, weight = applyRelationOverride(overrides, row.ChannelID, priority, weight)
		out = append(out, biz.RoutingCandidate{
			Group: group, Model: row.Model, ChannelID: row.ChannelID, Enabled: true, Priority: priority, Weight: weight,
			// Empty is meaningful: ResolveChannelModel must still be able to apply
			// channel.model_mapping or preserve the exact spelling in channel.Models.
			UpstreamModelID: strings.TrimSpace(row.UpstreamModelID),
		})
	}
	return out, nil
}

type registrySubscriptionRoutingCandidateRow struct {
	AccountID       int64  `gorm:"column:account_id"`
	Model           string `gorm:"column:model"`
	Platform        string `gorm:"column:platform"`
	UpstreamModelID string `gorm:"column:upstream_model_id"`
	MappingPriority int64  `gorm:"column:mapping_priority"`
	SourcePriority  int64  `gorm:"column:source_priority"`
}

func (r *Repository) listRegistrySubscriptionAbilitiesDB(ctx context.Context, group, model, platform string) ([]biz.SubscriptionRoutingCandidate, error) {
	var rows []registrySubscriptionRoutingCandidateRow
	query := r.db.WithContext(ctx).Table("model_subscription_mapping AS msm").
		Select("msm.subscription_account_id AS account_id, m.model_id AS model, sa.platform, msm.upstream_model_id, msm.priority AS mapping_priority, COALESCE(sa.priority, 0) AS source_priority").
		Joins("JOIN models AS m ON m.id = msm.model_id").
		Joins("JOIN subscription_accounts AS sa ON sa.id = msm.subscription_account_id").
		Where("LOWER(m.model_id) = ? AND msm.enabled = ? AND m.status = ? AND sa.status = ?", strings.ToLower(model), true, biz.ModelStatusEnabled, biz.ChannelStatusEnabled).
		Scopes(func(q *gorm.DB) *gorm.DB { return r.mappingGroupScope(q, group, "msm.") })
	if platform != "" {
		query = query.Where("sa.platform = ?", platform)
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, err
	}
	overrides, err := r.relationOverrides(ctx, group, true)
	if err != nil {
		return nil, err
	}
	out := make([]biz.SubscriptionRoutingCandidate, 0, len(rows))
	for _, row := range rows {
		priority := row.MappingPriority
		if priority == 0 {
			priority = row.SourcePriority
		}
		var weight int64
		// Complete chain: relation override > mapping priority > source priority.
		priority, weight = applyRelationOverride(overrides, row.AccountID, priority, weight)
		upstream := strings.TrimSpace(row.UpstreamModelID)
		if upstream == "" {
			upstream = row.Model
		}
		out = append(out, biz.SubscriptionRoutingCandidate{Group: group, Model: row.Model, Platform: row.Platform, AccountID: row.AccountID, Enabled: true, Priority: priority, Weight: weight, UpstreamModelID: upstream})
	}
	return out, nil
}

func (r *Repository) listAvailableModelsDB(ctx context.Context, group string) ([]string, error) {
	seen := make(map[string]struct{})
	if err := r.addRegistryChannelModelsDB(ctx, group, seen); err != nil {
		return nil, err
	}
	if err := r.addRegistrySubscriptionModelsDB(ctx, group, seen); err != nil {
		return nil, err
	}
	return sortedModelIDs(seen), nil
}

func sortedModelIDs(seen map[string]struct{}) []string {
	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

// addRegistryChannelModelsDB queries the model registry for models served by
// enabled channels in the given group and adds them to the seen set.
func (r *Repository) addRegistryChannelModelsDB(ctx context.Context, group string, seen map[string]struct{}) error {
	var registryModels []string
	// Join model_channel_mapping → models (enabled) → channels (enabled, group match).
	// The channels.group column stores a CSV (e.g. "default,vip"). We use
	// LIKE patterns instead of FIND_IN_SET for cross-driver compatibility
	// (FIND_IN_SET is MySQL-only; SQLite and Postgres do not support it).
	query := r.db.WithContext(ctx).Table("model_channel_mapping AS mcm").
		Select("m.model_id").
		Joins("JOIN models AS m ON m.id = mcm.model_id").
		Joins("JOIN channels AS c ON c.id = mcm.channel_id").
		Where("mcm.enabled = ? AND m.status = ? AND m.is_public = ? AND c.status = ?", true, biz.ModelStatusEnabled, true, biz.ChannelStatusEnabled).
		Scopes(func(q *gorm.DB) *gorm.DB { return r.csvGroupScope(q, group, "c.`group`", "c.id") }).
		Distinct("m.model_id")
	if err := query.Pluck("m.model_id", &registryModels).Error; err != nil {
		return err
	}
	for _, model := range registryModels {
		seen[strings.ToLower(model)] = struct{}{}
	}
	return nil
}

// addRegistrySubscriptionModelsDB queries the model registry for models served
// by enabled subscription accounts in the given group and adds them to seen.
func (r *Repository) addRegistrySubscriptionModelsDB(ctx context.Context, group string, seen map[string]struct{}) error {
	var registryModels []string
	query := r.db.WithContext(ctx).Table("model_subscription_mapping AS msm").
		Select("m.model_id").
		Joins("JOIN models AS m ON m.id = msm.model_id").
		Joins("JOIN subscription_accounts AS sa ON sa.id = msm.subscription_account_id").
		Where("msm.enabled = ? AND m.status = ? AND m.is_public = ? AND sa.status = ?", true, biz.ModelStatusEnabled, true, biz.ChannelStatusEnabled).
		Scopes(func(q *gorm.DB) *gorm.DB { return r.mappingGroupScope(q, group, "msm.") }).
		Distinct("m.model_id")
	if err := query.Pluck("m.model_id", &registryModels).Error; err != nil {
		return err
	}
	for _, model := range registryModels {
		seen[strings.ToLower(model)] = struct{}{}
	}
	return nil
}

func (r *Repository) findByIDMemory(_ context.Context, channelID int64) (*biz.Channel, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	channel, ok := r.channels[channelID]
	if !ok {
		return nil, biz.ErrChannelNotFound
	}
	cloned := *channel
	cloned.Models = append([]string(nil), channel.Models...)
	return &cloned, nil
}

func (r *Repository) listChannelCandidatesMemory(_ context.Context, group, model string) ([]biz.RoutingCandidate, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	managedModelPKs := make(map[int64]*biz.Model)
	for id, managed := range r.models {
		if biz.ModelIDEqual(managed.ModelID, model) {
			managedModelPKs[id] = managed
		}
	}
	candidates := make([]biz.RoutingCandidate, 0)
	for _, mapping := range r.modelChannelMappings {
		managed, ok := managedModelPKs[mapping.ModelPK]
		if !ok || !mapping.Enabled || managed.Status != biz.ModelStatusEnabled {
			continue
		}
		channel, ok := r.channels[mapping.ChannelID]
		if !ok || channel.Status != biz.ChannelStatusEnabled || !routing.ContainsGroup(channel.Group, group) {
			continue
		}
		priority := int64(mapping.Priority)
		if priority == 0 {
			priority = channel.Priority
		}
		candidates = append(candidates, biz.RoutingCandidate{
			Group: group, Model: managed.ModelID, ChannelID: channel.ID,
			Enabled: true, Priority: priority, UpstreamModelID: strings.TrimSpace(mapping.UpstreamModelID),
		})
	}
	if len(candidates) == 0 {
		return nil, biz.ErrChannelNotFound
	}
	return candidates, nil
}

func (r *Repository) listAvailableModelsMemory(_ context.Context, group string) ([]string, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	seen := make(map[string]struct{})
	for _, mcm := range r.modelChannelMappings {
		if !mcm.Enabled {
			continue
		}
		model, ok := r.models[mcm.ModelPK]
		if !ok || model.Status != biz.ModelStatusEnabled || !model.IsPublic {
			continue
		}
		channel, ok := r.channels[mcm.ChannelID]
		if !ok || channel.Status != biz.ChannelStatusEnabled {
			continue
		}
		if routing.ContainsGroup(channel.Group, group) {
			seen[strings.ToLower(model.ModelID)] = struct{}{}
		}
	}
	for _, msm := range r.modelSubscriptionMappings {
		if !msm.Enabled {
			continue
		}
		model, ok := r.models[msm.ModelPK]
		if !ok || model.Status != biz.ModelStatusEnabled || !model.IsPublic {
			continue
		}
		account, ok := r.subAccounts[msm.SubscriptionAccountID]
		if !ok || account.Status != biz.ChannelStatusEnabled {
			continue
		}
		if msm.GroupName == group {
			seen[strings.ToLower(model.ModelID)] = struct{}{}
		}
	}
	return sortedModelIDs(seen), nil
}

func (r *Repository) listChannelsDB(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*biz.Channel, int64, error) {
	query := r.db.WithContext(ctx).Model(&channelModel{})
	var err error
	if query, err = r.channelScope(ctx, query, "channels"); err != nil {
		return nil, 0, err
	}
	if keyword != "" {
		query = query.Where("name LIKE ? ESCAPE '!'", "%"+escapeLike(keyword)+"%")
	}
	if group != "" {
		query = query.Where(r.routingGroupSQL("`group` = ?"), group)
	}
	if status != 0 {
		query = query.Where("status = ?", status)
	}
	if chType != 0 {
		query = query.Where("type = ?", chType)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	var models []channelModel
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Find(&models).Error; err != nil {
		return nil, 0, err
	}
	result := make([]*biz.Channel, len(models))
	for i, m := range models {
		channel, err := r.modelToChannel(&m)
		if err != nil {
			return nil, 0, err
		}
		result[i] = channel
	}
	return result, total, nil
}

func (r *Repository) createChannelDB(ctx context.Context, channel *biz.Channel) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, 0, channel.Group, false, "channel.channel.create"); err != nil {
			return err
		}
		model, err := r.channelToModel(channel)
		if err != nil {
			return err
		}
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		channel.ID = model.ID
		channel.AuthorizationRevision = model.AuthorizationRevision
		if err := authzquery.AppendWriteAudit(ctx, tx, "channel.channel.create", model.ID); err != nil {
			return err
		}
		return r.syncChannelModelMappingsTx(tx, channel)
	})
}

func (r *Repository) updateChannelDB(ctx context.Context, channel *biz.Channel) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, channel.ID, channel.Group, false, "channel.channel.update"); err != nil {
			return err
		}
		if _, iam := authorization.QueryScopeFromContext(ctx, "channel.channel.update"); iam {
			owner := &Repository{db: tx, encKey: r.encKey}
			current, err := owner.FindByID(ctx, channel.ID)
			if err != nil {
				return err
			}
			if current.Status != channel.Status || current.Balance != channel.Balance || current.BalanceUpdatedTime != channel.BalanceUpdatedTime || current.BalanceRefreshLastError != channel.BalanceRefreshLastError || current.BalanceRefreshLastSuccessTime != channel.BalanceRefreshLastSuccessTime || current.ConsecutiveBalanceRefreshFailures != channel.ConsecutiveBalanceRefreshFailures {
				// Plain update cannot perform the unfinished balance action or
				// smuggle status into the ability projection.
				return authorization.ErrDenied
			}
			if current.ModelMapping != channel.ModelMapping {
				if err := r.checkCompatibilityMappingEdit(ctx, tx, channel.ID, channel.Group, false, current.ModelMapping, channel.ModelMapping); err != nil {
					return err
				}
			}

			if current.Key != channel.Key {
				if err := r.checkResourceTx(ctx, tx, channel.ID, channel.Group, false, "channel.channel.secret.rotate"); err != nil {
					return err
				}
			}
		}

		model, err := r.channelToModel(channel)
		if err != nil {
			return err
		}
		if err := tx.Model(&channelModel{}).Where("id = ?", channel.ID).Updates(map[string]any{
			"type":                                 model.Type,
			"name":                                 model.Name,
			"base_url":                             model.BaseURL,
			"key":                                  model.Key,
			"models":                               model.Models,
			"group":                                model.Group,
			"priority":                             model.Priority,
			"weight":                               model.Weight,
			"model_mapping":                        model.ModelMapping,
			"system_prompt":                        model.SystemPrompt,
			"config":                               model.Config,
			"balance":                              model.Balance,
			"balance_updated_time":                 model.BalanceUpdatedTime,
			"balance_refresh_last_error":           model.BalanceRefreshLastError,
			"balance_refresh_last_success_time":    model.BalanceRefreshLastSuccessTime,
			"consecutive_balance_refresh_failures": model.ConsecutiveBalanceRefreshFailures,
			"health_status":                        model.HealthStatus,
			"health_last_error":                    model.HealthLastError,
			"health_last_success_time":             model.HealthLastSuccessTime,
			"health_last_failure_time":             model.HealthLastFailureTime,
			"health_consecutive_failures":          model.HealthConsecutiveFailures,
			"circuit_opened_until":                 model.CircuitOpenedUntil,
		}).Error; err != nil {
			return err
		}
		return r.syncChannelModelMappingsTx(tx, channel)
	})
}

func (r *Repository) deleteChannelDB(ctx context.Context, channelID int64) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, channelID, "", false, "channel.channel.delete"); err != nil {
			return err
		}
		if err := requireSourceMappingDeletes(tx, r, channelID, false); err != nil {
			return err
		}
		if err := r.syncRoutingMembersTx(tx, routing.Source{Kind: routing.Channel, ID: channelID}, ""); err != nil {
			return err
		}
		if err := tx.Where("id = ?", channelID).Delete(&channelModel{}).Error; err != nil {
			return err
		}
		return tx.Where("channel_id = ?", channelID).Delete(&modelChannelMappingModel{}).Error
	})
}

func (r *Repository) changeStatusDB(ctx context.Context, channelID int64, status int32) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		if err := r.checkResourceTx(ctx, tx, channelID, "", false, "channel.channel.enable", "channel.channel.disable"); err != nil {
			return err
		}
		if err := tx.Model(&channelModel{}).Where("id = ?", channelID).Update("status", status).Error; err != nil {
			return err
		}
		return nil
	})
}

const autoChannelModelMappingConfig = `{"_managed_by":"channel_models"}`

// syncChannelModelMappingsTx keeps the legacy channel.Models editor visible to
// the managed registry without taking ownership of mappings created through
// model management. Auto-created rows carry a private marker in config; only
// those rows may be reprioritised or removed by later channel edits.
//
// Mapping enabled is always true. Channel availability is governed by
// channels.status, so a disabled channel can be re-enabled without leaving its
// auto-created mappings permanently disabled. Empty upstream_model_id is also
// intentional: relay resolution then applies channel.model_mapping and finally
// preserves the exact spelling from channel.Models.
func (r *Repository) syncChannelModelMappingsTx(tx *gorm.DB, channel *biz.Channel) error {
	if err := r.syncRoutingMembersTx(tx, routing.Source{Kind: routing.Channel, ID: channel.ID}, channel.Group); err != nil {
		return err
	}
	// Keep older installations usable until the registry migrations are applied.
	if !tx.Migrator().HasTable(&modelModel{}) || !tx.Migrator().HasTable(&modelChannelMappingModel{}) {
		return nil
	}

	wantedCanonical := make(map[string]string, len(channel.Models))
	for _, raw := range channel.Models {
		raw = strings.TrimSpace(raw)
		if raw == "" || strings.ContainsAny(raw, "*?") {
			continue
		}
		wantedCanonical[biz.NormalizeModelID(raw)] = raw
	}

	canonicalIDs := make([]string, 0, len(wantedCanonical))
	for canonicalID := range wantedCanonical {
		canonicalIDs = append(canonicalIDs, canonicalID)
	}
	registeredByCanonical := make(map[string]int64, len(canonicalIDs))
	if len(canonicalIDs) > 0 {
		var registered []modelModel
		if err := tx.Where("LOWER(model_id) IN ?", canonicalIDs).Find(&registered).Error; err != nil {
			return err
		}
		for _, model := range registered {
			registeredByCanonical[biz.NormalizeModelID(model.ModelID)] = model.ID
		}

		missing := make([]modelModel, 0, len(canonicalIDs)-len(registeredByCanonical))
		for _, canonicalID := range canonicalIDs {
			if _, ok := registeredByCanonical[canonicalID]; ok {
				continue
			}
			now := now()
			missing = append(missing, modelModel{
				ModelID: canonicalID, DisplayName: canonicalID,
				Provider: providerForModelID(canonicalID), ModelType: "chat",
				Status: biz.ModelStatusEnabled, IsPublic: true,
				CreatedAt: now, UpdatedAt: now,
			})
		}
		if len(missing) > 0 {
			for _, operation := range []string{"channel.channel.create", "channel.channel.update"} {
				if _, iam := authorization.QueryScopeFromContext(tx.Statement.Context, operation); iam {
					if err := authorization.Require(tx.Statement.Context, "channel.model.create", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
						return err
					}
				}
			}

			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&missing, len(missing)).Error; err != nil {
				return err
			}
			registeredByCanonical = make(map[string]int64, len(canonicalIDs))
			registered = nil
			if err := tx.Where("LOWER(model_id) IN ?", canonicalIDs).Find(&registered).Error; err != nil {
				return err
			}
			for _, model := range registered {
				registeredByCanonical[biz.NormalizeModelID(model.ModelID)] = model.ID
			}
			if len(registeredByCanonical) != len(canonicalIDs) {
				return fmt.Errorf("resolve channel model registry rows: got %d of %d", len(registeredByCanonical), len(canonicalIDs))
			}
		}
	}
	wanted := make(map[int64]string, len(registeredByCanonical))
	for canonicalID, pk := range registeredByCanonical {
		wanted[pk] = wantedCanonical[canonicalID]
	}

	var existing []modelChannelMappingModel
	if err := tx.Where("channel_id = ?", channel.ID).Find(&existing).Error; err != nil {
		return err
	}
	existingByModel := make(map[int64]struct{}, len(existing))
	now := now()
	priority := safecast.Int64ToInt32Saturating(channel.Priority)
	managedToDelete := make([]int64, 0)
	managedToUpdate := make([]int64, 0)
	for _, row := range existing {
		existingByModel[row.ModelPK] = struct{}{}
		if row.Config != autoChannelModelMappingConfig {
			continue
		}
		if _, ok := wanted[row.ModelPK]; !ok {
			managedToDelete = append(managedToDelete, row.ID)
		} else if !row.Enabled || row.Priority != priority || row.UpstreamModelID != wanted[row.ModelPK] {
			managedToUpdate = append(managedToUpdate, row.ID)
		}
	}
	for _, operation := range []string{"channel.channel.create", "channel.channel.update"} {
		if _, iam := authorization.QueryScopeFromContext(tx.Statement.Context, operation); iam && (len(managedToDelete) > 0 || len(managedToUpdate) > 0) {
			for _, id := range managedToDelete {
				if err := requireSourceMappingEffect(tx, r, channel.ID, id, "channel.model_mapping.delete"); err != nil {
					return err
				}
			}
			for _, id := range managedToUpdate {
				if err := requireSourceMappingEffect(tx, r, channel.ID, id, "channel.model_mapping.update"); err != nil {
					return err
				}
			}
		}
	}
	if len(managedToDelete) > 0 {
		if err := tx.Where("id IN ?", managedToDelete).Delete(&modelChannelMappingModel{}).Error; err != nil {
			return err
		}
	}
	if len(managedToUpdate) > 0 {
		toUpdate := make(map[int64]struct{}, len(managedToUpdate))
		for _, id := range managedToUpdate {
			toUpdate[id] = struct{}{}
		}
		for _, row := range existing {
			if _, ok := toUpdate[row.ID]; !ok {
				continue
			}
			if err := tx.Model(&modelChannelMappingModel{}).Where("id = ?", row.ID).Updates(map[string]any{
				"enabled": true, "priority": priority,
				"upstream_model_id": wanted[row.ModelPK], "updated_at": now,
			}).Error; err != nil {
				return err
			}
		}
	}

	missingMappings := make([]modelChannelMappingModel, 0, len(wanted))
	for modelPK, upstream := range wanted {
		if _, ok := existingByModel[modelPK]; ok {
			continue
		}
		missingMappings = append(missingMappings, modelChannelMappingModel{
			ChannelID: channel.ID, ModelPK: modelPK, Enabled: true,
			Priority: priority, Config: autoChannelModelMappingConfig,
			UpstreamModelID: upstream, CreatedAt: now, UpdatedAt: now,
		})
	}
	if len(missingMappings) > 0 {
		for _, operation := range []string{"channel.channel.create", "channel.channel.update"} {
			if _, iam := authorization.QueryScopeFromContext(tx.Statement.Context, operation); iam {
				if err := requireSourceMappingEffect(tx, r, channel.ID, 0, "channel.model_mapping.create"); err != nil {
					return err
				}
			}
		}
		if err := tx.CreateInBatches(&missingMappings, len(missingMappings)).Error; err != nil {
			return err
		}
	}
	return nil
}

// ensureModelRegistryRowTx ... (unchanged)
// model_id matches, creating a minimal public chat model when it is missing.
func ensureModelRegistryRowTx(tx *gorm.DB, canonicalID string) (int64, error) {
	var po modelModel
	err := tx.Where("LOWER(model_id) = ?", canonicalID).First(&po).Error
	if err == nil {
		return po.ID, nil
	}
	if !isGormNotFound(err) {
		return 0, err
	}
	for _, operation := range []string{"channel.channel.create", "channel.channel.update", "channel.account.create", "channel.account.update"} {
		if _, iam := authorization.QueryScopeFromContext(tx.Statement.Context, operation); iam {
			if err := authorization.Require(tx.Statement.Context, "channel.model.create", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
				return 0, err
			}
		}
	}

	now := now()
	po = modelModel{
		ModelID:     canonicalID,
		DisplayName: canonicalID,
		Provider:    providerForModelID(canonicalID),
		ModelType:   "chat",
		Status:      biz.ModelStatusEnabled,
		IsPublic:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := tx.Create(&po).Error; err != nil {
		// Concurrent insert by another request: re-read.
		if isDuplicateEntry(err) {
			if err := tx.Where("LOWER(model_id) = ?", canonicalID).First(&po).Error; err != nil {
				return 0, err
			}
			return po.ID, nil
		}
		return 0, err
	}
	return po.ID, nil
}

// providerForModelID infers a provider key from common model-name prefixes so
// that auto-created registry rows carry a useful provider for filtering.
func providerForModelID(modelID string) string {
	id := strings.ToLower(modelID)
	switch {
	case strings.HasPrefix(id, "gpt"), strings.HasPrefix(id, "o1"), strings.HasPrefix(id, "o3"), strings.HasPrefix(id, "o4"):
		return "openai"
	case strings.HasPrefix(id, "claude"):
		return "anthropic"
	case strings.HasPrefix(id, "gemini"):
		return "google"
	case strings.HasPrefix(id, "glm"), strings.HasPrefix(id, "kimi"), strings.HasPrefix(id, "k3"):
		return "zhipu"
	case strings.HasPrefix(id, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(id, "qwen"), strings.HasPrefix(id, "tongyi"):
		return "alibaba"
	default:
		return "unknown"
	}
}

const autoSubscriptionModelMappingConfig = `{"_managed_by":"subscription_models"}`

func subscriptionModelMappingKey(group string, modelPK int64) string {
	return fmt.Sprintf("%s\x00%d", group, modelPK)
}

// syncSubscriptionAccountModelMappingsTx projects the account's model CSV into
// registry mappings. Operator-created mappings remain untouched; only rows with
// the private auto marker are rewritten.
func (r *Repository) syncSubscriptionAccountModelMappingsTx(tx *gorm.DB, account *biz.SubscriptionAccount) error {
	if err := r.syncRoutingMembersTx(tx, routing.Source{Kind: routing.Subscription, ID: account.ID}, account.Group); err != nil {
		return err
	}
	now := now()
	priority := safecast.Int64ToInt32Saturating(account.Priority)
	desired := make(map[string]modelSubscriptionMappingModel)
	for _, group := range routing.Groups(account.Group) {
		if group == "" {
			continue
		}
		for _, raw := range account.Models {
			modelID := biz.NormalizeModelID(raw)
			if modelID == "" || strings.ContainsAny(modelID, "*?") {
				continue
			}
			modelPK, err := ensureModelRegistryRowTx(tx, modelID)
			if err != nil {
				return err
			}
			key := subscriptionModelMappingKey(group, modelPK)
			desired[key] = modelSubscriptionMappingModel{
				SubscriptionAccountID: account.ID,
				ModelPK:               modelPK,
				GroupName:             group,
				Enabled:               true,
				Priority:              priority,
				Config:                autoSubscriptionModelMappingConfig,
				UpstreamModelID:       strings.TrimSpace(raw),
				CreatedAt:             now,
				UpdatedAt:             now,
			}
		}
	}

	var existing []modelSubscriptionMappingModel
	if err := tx.Where("subscription_account_id = ?", account.ID).Find(&existing).Error; err != nil {
		return err
	}
	existingByKey := make(map[string]modelSubscriptionMappingModel, len(existing))
	autoByKey := make(map[string]modelSubscriptionMappingModel, len(existing))
	for _, row := range existing {
		key := subscriptionModelMappingKey(row.GroupName, row.ModelPK)
		existingByKey[key] = row
		if row.Config == autoSubscriptionModelMappingConfig {
			autoByKey[key] = row
		}
	}
	managedToDelete := make([]int64, 0)
	managedToUpdate := make([]int64, 0)
	createKeys := make([]string, 0)
	for key, row := range autoByKey {
		if _, ok := desired[key]; !ok {
			managedToDelete = append(managedToDelete, row.ID)
		}
	}
	for key := range desired {
		if current, ok := existingByKey[key]; ok {
			if current.Config == autoSubscriptionModelMappingConfig {
				managedToUpdate = append(managedToUpdate, current.ID)
			}
			continue
		}
		createKeys = append(createKeys, key)
	}
	for _, id := range managedToDelete {
		if err := requireSubscriptionMappingEffect(tx, r, account.ID, id, "channel.model_mapping.delete"); err != nil {
			return err
		}
	}
	for _, id := range managedToUpdate {
		if err := requireSubscriptionMappingEffect(tx, r, account.ID, id, "channel.model_mapping.update"); err != nil {
			return err
		}
	}
	if len(createKeys) > 0 {
		if err := requireSubscriptionMappingEffect(tx, r, account.ID, 0, "channel.model_mapping.create"); err != nil {
			return err
		}
	}
	for _, id := range managedToDelete {
		if err := tx.Where("id = ?", id).Delete(&modelSubscriptionMappingModel{}).Error; err != nil {
			return err
		}
	}
	for key, next := range desired {
		if current, ok := existingByKey[key]; ok {
			if current.Config != autoSubscriptionModelMappingConfig {
				continue
			}
			if err := tx.Model(&modelSubscriptionMappingModel{}).Where("id = ?", current.ID).Updates(map[string]any{
				"enabled":           true,
				"priority":          next.Priority,
				"upstream_model_id": next.UpstreamModelID,
				"updated_at":        now,
			}).Error; err != nil {
				return err
			}
			if err := r.syncRoutingMappingTx(tx, "model_subscription_mapping", map[string]any{"id": current.ID}, next.GroupName); err != nil {
				return err
			}
			continue
		}
		if err := tx.Create(&next).Error; err != nil {
			return err
		}
		if err := r.syncRoutingMappingTx(tx, "model_subscription_mapping", map[string]any{"id": next.ID}, next.GroupName); err != nil {
			return err
		}
	}
	return nil
}

// encryptKey encrypts a credential for storage. Persistent repositories always
// have an encryption key; returning an error prevents accidental plaintext
// writes when the key is invalid or encryption fails.
func (r *Repository) encryptKey(key string) (string, error) {
	if key == "" {
		return "", nil
	}
	if len(r.encKey) == 0 {
		return "", fmt.Errorf("channel credential encryption key is not configured")
	}
	encrypted, err := appcrypto.Encrypt(key, r.encKey)
	if err != nil {
		return "", fmt.Errorf("encrypt channel credential: %w", err)
	}
	return encrypted, nil
}

// Configured encryption requires migration of legacy plaintext before reads.
func (r *Repository) decryptKey(key string) (string, error) {
	if key == "" {
		return "", nil
	}
	if r.encKey == nil {
		if looksLikeCiphertext(key) {
			return "", fmt.Errorf("encrypted credential requires encryption key")
		}
		return key, nil
	}
	decrypted, err := appcrypto.Decrypt(key, r.encKey)
	if err == nil {
		return decrypted, nil
	}
	return "", fmt.Errorf("stored credential could not be decrypted; verify the encryption key or migrate legacy plaintext")
}

func (r *Repository) modelToChannel(m *channelModel) (*biz.Channel, error) {
	key, err := r.decryptKey(m.Key)
	if err != nil {
		return nil, err
	}
	baseURL := ""
	if m.BaseURL != nil {
		baseURL = *m.BaseURL
	}
	priority := int64(0)
	if m.Priority != nil {
		priority = *m.Priority
	}
	return &biz.Channel{
		ID:                                m.ID,
		AuthorizationRevision:             m.AuthorizationRevision,
		Type:                              m.Type,
		Name:                              m.Name,
		Status:                            m.Status,
		BaseURL:                           baseURL,
		Group:                             m.Group,
		Models:                            biz.SplitCSV(m.Models),
		Priority:                          priority,
		Key:                               key,
		Weight:                            derefUint(m.Weight),
		CreatedTime:                       m.CreatedTime,
		TestTime:                          m.TestTime,
		ResponseTime:                      m.ResponseTime,
		Balance:                           m.Balance,
		BalanceUpdatedTime:                m.BalanceUpdatedTime,
		BalanceRefreshLastError:           derefString(m.BalanceRefreshLastError),
		BalanceRefreshLastSuccessTime:     m.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: m.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      m.HealthStatus,
		HealthLastError:                   derefString(m.HealthLastError),
		HealthLastSuccessTime:             m.HealthLastSuccessTime,
		HealthLastFailureTime:             m.HealthLastFailureTime,
		HealthConsecutiveFailures:         m.HealthConsecutiveFailures,
		CircuitOpenedUntil:                m.CircuitOpenedUntil,
		UsedQuota:                         m.UsedQuota,
		ModelMapping:                      derefString(m.ModelMapping),
		SystemPrompt:                      derefString(m.SystemPrompt),
		Config:                            biz.DecodeChannelConfig(m.Config),
	}, nil
}

func (r *Repository) channelToModel(ch *biz.Channel) (*channelModel, error) {
	key, err := r.encryptKey(ch.Key)
	if err != nil {
		return nil, err
	}
	config, err := jsonx.Marshal(ch.Config)
	if err != nil {
		return nil, err
	}
	return &channelModel{
		AuthorizationRevision:             ch.AuthorizationRevision,
		ID:                                ch.ID,
		Type:                              ch.Type,
		Name:                              ch.Name,
		Status:                            ch.Status,
		BaseURL:                           new(ch.BaseURL),
		Weight:                            uintPtr(ch.Weight),
		CreatedTime:                       ch.CreatedTime,
		TestTime:                          ch.TestTime,
		ResponseTime:                      ch.ResponseTime,
		Balance:                           ch.Balance,
		BalanceUpdatedTime:                ch.BalanceUpdatedTime,
		BalanceRefreshLastError:           new(ch.BalanceRefreshLastError),
		BalanceRefreshLastSuccessTime:     ch.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: ch.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      ch.EffectiveHealthStatus(),
		HealthLastError:                   new(ch.HealthLastError),
		HealthLastSuccessTime:             ch.HealthLastSuccessTime,
		HealthLastFailureTime:             ch.HealthLastFailureTime,
		HealthConsecutiveFailures:         ch.HealthConsecutiveFailures,
		CircuitOpenedUntil:                ch.CircuitOpenedUntil,
		Models:                            ch.ModelsCSV(),
		Group:                             ch.Group,
		UsedQuota:                         ch.UsedQuota,
		ModelMapping:                      new(ch.ModelMapping),
		Priority:                          new(ch.Priority),
		Key:                               key,
		Config:                            string(config),
		SystemPrompt:                      new(ch.SystemPrompt),
	}, nil
}

func (r *Repository) subscriptionAccountModelToBiz(m *subscriptionAccountModel) (*biz.SubscriptionAccount, error) {
	accessToken, err := r.decryptKey(derefString(m.AccessToken))
	if err != nil {
		return nil, err
	}
	refreshToken, err := r.decryptKey(derefString(m.RefreshToken))
	if err != nil {
		return nil, err
	}
	baseURL := ""
	if m.BaseURL != nil {
		baseURL = *m.BaseURL
	}
	account := &biz.SubscriptionAccount{
		CredentialRefreshPending: m.CredentialRefreshPending,

		ID:                     m.ID,
		Name:                   m.Name,
		Platform:               m.Platform,
		AccountType:            m.AccountType,
		Status:                 m.Status,
		Group:                  m.Group,
		Models:                 biz.SplitCSV(m.Models),
		Priority:               m.Priority,
		Weight:                 m.Weight,
		BaseURL:                baseURL,
		AccessToken:            accessToken,
		RefreshToken:           refreshToken,
		ExpiresAt:              m.ExpiresAt,
		CredentialRevision:     m.CredentialRevision,
		AccountID:              m.AccountID,
		Fingerprint:            derefString(m.Fingerprint),
		Metadata:               derefString(m.Metadata),
		CreatedAt:              m.CreatedAt,
		UpdatedAt:              m.UpdatedAt,
		LastUsedAt:             m.LastUsedAt,
		RateLimitedUntil:       m.RateLimitedUntil,
		QuotaUsedPercent:       m.QuotaUsedPercent,
		QuotaResetAt:           m.QuotaResetAt,
		Concurrency:            m.Concurrency,
		QuotaLimitUSD:          m.QuotaLimitUSD,
		QuotaUsedUSD:           m.QuotaUsedUSD,
		Quota5hLimitUSD:        m.Quota5hLimitUSD,
		Quota5hUsedUSD:         m.Quota5hUsedUSD,
		Quota5hWindowStart:     m.Quota5hWindowStart,
		QuotaDailyLimitUSD:     m.QuotaDailyLimitUSD,
		QuotaDailyUsedUSD:      m.QuotaDailyUsedUSD,
		QuotaDailyWindowStart:  m.QuotaDailyWindowStart,
		QuotaWeeklyLimitUSD:    m.QuotaWeeklyLimitUSD,
		QuotaWeeklyUsedUSD:     m.QuotaWeeklyUsedUSD,
		QuotaWeeklyWindowStart: m.QuotaWeeklyWindowStart,
		RateMultiplier:         m.RateMultiplier,
		RPMLimit:               m.RPMLimit,
		SessionWindowLimitUSD:  m.SessionWindowLimitUSD,
		QuotaResetStrategy:     m.QuotaResetStrategy,
		QuotaTimezone:          m.QuotaTimezone,
		ModelMapping:           m.ModelMapping,
		LastError:              subscriptionAccountMetadataValue(derefString(m.Metadata), "last_error"),
	}
	account.RecoveryBaseline = new(account.RecoveryState())
	return account, nil
}

func (r *Repository) subscriptionAccountBizToModel(a *biz.SubscriptionAccount) (*subscriptionAccountModel, error) {
	accessToken, err := r.encryptKey(a.AccessToken)
	if err != nil {
		return nil, err
	}
	refreshToken, err := r.encryptKey(a.RefreshToken)
	if err != nil {
		return nil, err
	}
	return &subscriptionAccountModel{
		CredentialRefreshPending: a.CredentialRefreshPending,

		ID:                     a.ID,
		Name:                   a.Name,
		Platform:               a.Platform,
		AccountType:            a.AccountType,
		Status:                 a.Status,
		Group:                  a.Group,
		Models:                 a.ModelsCSV(),
		Priority:               a.Priority,
		Weight:                 a.Weight,
		BaseURL:                new(a.BaseURL),
		AccessToken:            new(accessToken),
		RefreshToken:           new(refreshToken),
		ExpiresAt:              a.ExpiresAt,
		CredentialRevision:     a.CredentialRevision,
		AccountID:              a.AccountID,
		Fingerprint:            new(a.Fingerprint),
		Metadata:               new(a.Metadata),
		CreatedAt:              a.CreatedAt,
		UpdatedAt:              a.UpdatedAt,
		LastUsedAt:             a.LastUsedAt,
		RateLimitedUntil:       a.RateLimitedUntil,
		QuotaUsedPercent:       a.QuotaUsedPercent,
		QuotaResetAt:           a.QuotaResetAt,
		Concurrency:            a.Concurrency,
		QuotaLimitUSD:          a.QuotaLimitUSD,
		QuotaUsedUSD:           a.QuotaUsedUSD,
		Quota5hLimitUSD:        a.Quota5hLimitUSD,
		Quota5hUsedUSD:         a.Quota5hUsedUSD,
		Quota5hWindowStart:     a.Quota5hWindowStart,
		QuotaDailyLimitUSD:     a.QuotaDailyLimitUSD,
		QuotaDailyUsedUSD:      a.QuotaDailyUsedUSD,
		QuotaDailyWindowStart:  a.QuotaDailyWindowStart,
		QuotaWeeklyLimitUSD:    a.QuotaWeeklyLimitUSD,
		QuotaWeeklyUsedUSD:     a.QuotaWeeklyUsedUSD,
		QuotaWeeklyWindowStart: a.QuotaWeeklyWindowStart,
		RateMultiplier:         a.RateMultiplier,
		RPMLimit:               a.RPMLimit,
		SessionWindowLimitUSD:  a.SessionWindowLimitUSD,
		QuotaResetStrategy:     a.EffectiveQuotaResetStrategy(),
		QuotaTimezone:          a.EffectiveQuotaTimezone(),
		ModelMapping:           a.ModelMapping,
	}, nil
}

func applySubscriptionAccountQuotaUsage(account *biz.SubscriptionAccount, costUSD float64, occurredAt time.Time) {
	if account == nil || costUSD <= 0 {
		return
	}
	nowUnix := occurredAt.Unix()
	chargedUSD := costUSD * account.EffectiveRateMultiplier()
	account.QuotaUsedUSD += chargedUSD
	account.Quota5hUsedUSD, account.Quota5hWindowStart = incrementWindowUsage(account.Quota5hUsedUSD, account.Quota5hWindowStart, chargedUSD, nowUnix, 5*time.Hour)
	if account.UsesFixedQuotaReset() {
		account.QuotaDailyUsedUSD, account.QuotaDailyWindowStart = incrementFixedWindowUsage(account, account.QuotaDailyUsedUSD, account.QuotaDailyWindowStart, chargedUSD, occurredAt, "daily")
		account.QuotaWeeklyUsedUSD, account.QuotaWeeklyWindowStart = incrementFixedWindowUsage(account, account.QuotaWeeklyUsedUSD, account.QuotaWeeklyWindowStart, chargedUSD, occurredAt, "weekly")
	} else {
		account.QuotaDailyUsedUSD, account.QuotaDailyWindowStart = incrementWindowUsage(account.QuotaDailyUsedUSD, account.QuotaDailyWindowStart, chargedUSD, nowUnix, 24*time.Hour)
		account.QuotaWeeklyUsedUSD, account.QuotaWeeklyWindowStart = incrementWindowUsage(account.QuotaWeeklyUsedUSD, account.QuotaWeeklyWindowStart, chargedUSD, nowUnix, 7*24*time.Hour)
	}
	account.LastUsedAt = nowUnix
	account.UpdatedAt = now()
}

func incrementWindowUsage(used float64, windowStart int64, delta float64, nowUnix int64, window time.Duration) (float64, int64) {
	used, windowStart = biz.RollAccountQuotaWindow(used, windowStart, nowUnix, window)
	return used + delta, windowStart
}

func incrementFixedWindowUsage(account *biz.SubscriptionAccount, used float64, windowStart int64, delta float64, now time.Time, scope string) (float64, int64) {
	used, windowStart = account.RollFixedQuotaWindow(used, windowStart, now, scope)
	return used + delta, windowStart
}

func resetSubscriptionAccountQuota(account *biz.SubscriptionAccount, scope string) {
	if account == nil {
		return
	}
	switch scope {
	case "total":
		account.QuotaUsedUSD = 0
	case "5h":
		account.Quota5hUsedUSD = 0
		account.Quota5hWindowStart = 0
	case "daily":
		account.QuotaDailyUsedUSD = 0
		account.QuotaDailyWindowStart = 0
	case "weekly":
		account.QuotaWeeklyUsedUSD = 0
		account.QuotaWeeklyWindowStart = 0
	case "all":
		account.QuotaUsedUSD = 0
		account.Quota5hUsedUSD = 0
		account.Quota5hWindowStart = 0
		account.QuotaDailyUsedUSD = 0
		account.QuotaDailyWindowStart = 0
		account.QuotaWeeklyUsedUSD = 0
		account.QuotaWeeklyWindowStart = 0
	}
	account.UpdatedAt = now()
}

func resetSubscriptionAccountQuotaToWindow(account *biz.SubscriptionAccount, scope string, windowStart int64) {
	if account == nil {
		return
	}
	switch scope {
	case "daily":
		account.QuotaDailyUsedUSD = 0
		account.QuotaDailyWindowStart = windowStart
	case "weekly":
		account.QuotaWeeklyUsedUSD = 0
		account.QuotaWeeklyWindowStart = windowStart
	default:
		resetSubscriptionAccountQuota(account, scope)
		return
	}
	account.UpdatedAt = now()
}

func subscriptionAccountQuotaEventKey(reservationID string, accountID int64, costSource string) string {
	return reservationID + "\x00" + strconv.FormatInt(accountID, 10) + "\x00" + costSource
}

func subscriptionAccountQuotaResetUpdates(scope string) map[string]any {
	switch scope {
	case "total":
		return map[string]any{"quota_used_usd": 0}
	case "5h":
		return map[string]any{"quota_5h_used_usd": 0, "quota_5h_window_start": 0}
	case "daily":
		return map[string]any{"quota_daily_used_usd": 0, "quota_daily_window_start": 0}
	case "weekly":
		return map[string]any{"quota_weekly_used_usd": 0, "quota_weekly_window_start": 0}
	case "all":
		return map[string]any{
			"quota_used_usd":            0,
			"quota_5h_used_usd":         0,
			"quota_5h_window_start":     0,
			"quota_daily_used_usd":      0,
			"quota_daily_window_start":  0,
			"quota_weekly_used_usd":     0,
			"quota_weekly_window_start": 0,
		}
	default:
		return nil
	}
}

func subscriptionAccountQuotaResetUpdatesToWindow(scope string, windowStart int64) map[string]any {
	updates := subscriptionAccountQuotaResetUpdates(scope)
	if updates == nil {
		return nil
	}
	switch scope {
	case "daily":
		updates["quota_daily_window_start"] = windowStart
	case "weekly":
		updates["quota_weekly_window_start"] = windowStart
	}
	return updates
}

func accountQuotaSnapshotBizToModel(s *biz.AccountQuotaSnapshot) *accountQuotaSnapshotModel {
	updatedAt := s.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	return &accountQuotaSnapshotModel{
		AccountID:                   s.AccountID,
		PrimaryUsedPercent:          s.PrimaryUsedPercent,
		PrimaryResetAfterSeconds:    s.PrimaryResetAfterSeconds,
		PrimaryWindowMinutes:        s.PrimaryWindowMinutes,
		SecondaryUsedPercent:        s.SecondaryUsedPercent,
		SecondaryResetAfterSeconds:  s.SecondaryResetAfterSeconds,
		SecondaryWindowMinutes:      s.SecondaryWindowMinutes,
		PrimaryOverSecondaryPercent: s.PrimaryOverSecondaryPercent,
		UpdatedAt:                   &updatedAt,
		SnapshotPaused:              s.SnapshotPaused,
	}
}

func accountQuotaSnapshotModelToBiz(m *accountQuotaSnapshotModel) *biz.AccountQuotaSnapshot {
	updatedAt := time.Time{}
	if m.UpdatedAt != nil {
		updatedAt = *m.UpdatedAt
	}
	return &biz.AccountQuotaSnapshot{
		AccountID:                   m.AccountID,
		PrimaryUsedPercent:          m.PrimaryUsedPercent,
		PrimaryResetAfterSeconds:    m.PrimaryResetAfterSeconds,
		PrimaryWindowMinutes:        m.PrimaryWindowMinutes,
		SecondaryUsedPercent:        m.SecondaryUsedPercent,
		SecondaryResetAfterSeconds:  m.SecondaryResetAfterSeconds,
		SecondaryWindowMinutes:      m.SecondaryWindowMinutes,
		PrimaryOverSecondaryPercent: m.PrimaryOverSecondaryPercent,
		UpdatedAt:                   updatedAt,
		SnapshotPaused:              m.SnapshotPaused,
	}
}

func applyAccountQuotaSnapshot(account *biz.SubscriptionAccount, snapshot *biz.AccountQuotaSnapshot) {
	if account == nil || snapshot == nil {
		return
	}
	account.PrimaryQuotaUsedPercent = snapshot.PrimaryUsedPercent
	account.PrimaryQuotaResetAfterSeconds = snapshot.PrimaryResetAfterSeconds
	account.PrimaryQuotaWindowMinutes = snapshot.PrimaryWindowMinutes
	account.SecondaryQuotaUsedPercent = snapshot.SecondaryUsedPercent
	account.SecondaryQuotaResetAfterSeconds = snapshot.SecondaryResetAfterSeconds
	account.SecondaryQuotaWindowMinutes = snapshot.SecondaryWindowMinutes
	account.PrimaryOverSecondaryPercent = snapshot.PrimaryOverSecondaryPercent
	account.QuotaSnapshotPaused = snapshot.SnapshotPaused
	if !snapshot.UpdatedAt.IsZero() {
		account.QuotaSnapshotUpdatedAt = snapshot.UpdatedAt.Unix()
	}
}

func quotaResetAt(updatedAt time.Time, primary, secondary *int32) int64 {
	if updatedAt.IsZero() {
		updatedAt = time.Now()
	}
	var resetAfter int32
	if primary != nil && *primary > resetAfter {
		resetAfter = *primary
	}
	if secondary != nil && *secondary > resetAfter {
		resetAfter = *secondary
	}
	if resetAfter <= 0 {
		return 0
	}
	return updatedAt.Add(time.Duration(resetAfter) * time.Second).Unix()
}

//go:fix inline
func strPtr(s string) *string { return new(s) }

//go:fix inline
func int64Ptr(i int64) *int64 { return new(i) }
func uintPtr(i uint32) *uint {
	v := uint(i)
	return &v
}

//go:fix inline
func stringPtr(s string) *string { return new(s) }
func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
func derefUint(u *uint) uint32 {
	if u == nil {
		return 0
	}
	v, err := safecast.UintToUint32(*u)
	if err != nil {
		return 0
	}
	return v
}

func subscriptionAccountMetadataValue(raw, key string) string {
	values := subscriptionAccountMetadata(raw)
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return value
}

// A non-empty error is a new incident even if the text is unchanged.
func subscriptionAccountErrorMetadata(raw, message string) string {
	if message != "" {
		// This RPC is also the relay's quota-exhaustion path. Persist the
		// policy together with the incident so the sweeper does not guess.
		return stampRecoveryMetadata(raw, message, 0, now())
	}
	return clearSubscriptionAccountRecoveryMetadata(setSubscriptionAccountMetadataValue(raw, "last_error", ""))
}

func setSubscriptionAccountMetadataValue(raw, key, value string) string {
	values := subscriptionAccountMetadata(raw)
	if values == nil {
		values = make(map[string]any)
		if strings.TrimSpace(raw) != "" {
			values["raw"] = raw
		}
	}
	if value == "" {
		delete(values, key)
	} else {
		values[key] = value
	}
	if len(values) == 0 {
		return ""
	}
	b, err := jsonx.Marshal(values)
	if err != nil {
		return raw
	}
	return string(b)
}

func subscriptionAccountMetadata(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return map[string]any{}
	}
	values := make(map[string]any)
	if err := jsonx.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func applyHealthEvent(channel *biz.Channel, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) {
	if event.Success && event.ConfigurationRevision == "" && channel.EffectiveHealthStatus() == biz.ChannelHealthUnavailable {
		return
	}
	if event.CheckedAt.IsZero() {
		event.CheckedAt = time.Now()
	}
	checkedAt := event.CheckedAt.Unix()
	channel.TestTime = checkedAt
	channel.ResponseTime = event.ResponseTime
	if event.Success {
		channel.HealthStatus = biz.ChannelHealthHealthy
		channel.HealthLastError = ""
		channel.HealthLastSuccessTime = checkedAt
		channel.HealthConsecutiveFailures = 0
		channel.CircuitOpenedUntil = 0
		return
	}
	channel.HealthLastError = event.Error
	channel.HealthLastFailureTime = checkedAt
	channel.HealthConsecutiveFailures++
	if threshold <= 0 {
		threshold = 1
	}
	if channel.HealthConsecutiveFailures >= threshold {
		channel.HealthStatus = biz.ChannelHealthUnavailable
		if cooldown > 0 {
			channel.CircuitOpenedUntil = event.CheckedAt.Add(cooldown).Unix()
		}
		return
	}
	channel.HealthStatus = biz.ChannelHealthDegraded
	channel.CircuitOpenedUntil = 0
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "!", "!!")
	s = strings.ReplaceAll(s, "%", "!%")
	s = strings.ReplaceAll(s, "_", "!_")
	return s
}

// subscriptionAccountQuotaResetRunModel mirrors subscription_account_quota_reset_runs.
type subscriptionAccountQuotaResetRunModel struct {
	ID                    int64  `gorm:"column:id"`
	SubscriptionAccountID int64  `gorm:"column:subscription_account_id"`
	Scope                 string `gorm:"column:scope"`
	WindowStart           int64  `gorm:"column:window_start"`
	Strategy              string `gorm:"column:strategy"`
	Timezone              string `gorm:"column:timezone"`
	ResetAt               int64  `gorm:"column:reset_at"`
}

func (subscriptionAccountQuotaResetRunModel) TableName() string {
	return "subscription_account_quota_reset_runs"
}

// RecordQuotaResetRun durably records an automated fixed-strategy quota reset.
// The unique index on (subscription_account_id, scope, window_start) makes a
// duplicate insert (repeated worker tick / another replica) fail with a unique
// constraint violation, which is mapped to biz.ErrQuotaResetRunDuplicate so the
// sweeper can treat it as a no-op rather than an error.
func (r *Repository) RecordQuotaResetRun(ctx context.Context, run *biz.SubscriptionAccountQuotaResetRun) error {
	if run == nil || run.AccountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	if r.db != nil {
		return r.recordQuotaResetRunDB(ctx, run)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.resetRuns == nil {
		r.resetRuns = make(map[string]bool)
	}
	key := strconv.FormatInt(run.AccountID, 10) + "\x00" + run.Scope + "\x00" + strconv.FormatInt(run.WindowStart, 10)
	if r.resetRuns[key] {
		return biz.ErrQuotaResetRunDuplicate
	}
	r.resetRuns[key] = true
	return nil
}

func (r *Repository) RecordQuotaResetAndReset(ctx context.Context, run *biz.SubscriptionAccountQuotaResetRun) error {
	if run == nil || run.AccountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	if (run.Scope != "daily" && run.Scope != "weekly") || run.WindowStart <= 0 {
		return fmt.Errorf("invalid quota reset window")
	}
	if r.db != nil {
		return r.recordQuotaResetAndResetDB(ctx, run)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if r.resetRuns == nil {
		r.resetRuns = make(map[string]bool)
	}
	key := strconv.FormatInt(run.AccountID, 10) + "\x00" + run.Scope + "\x00" + strconv.FormatInt(run.WindowStart, 10)
	if r.resetRuns[key] {
		return biz.ErrQuotaResetRunDuplicate
	}
	account, ok := r.subAccounts[run.AccountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	if !run.MatchesAccount(account) {
		return biz.ErrQuotaResetRunStale
	}
	stored := account.QuotaDailyWindowStart
	if run.Scope == "weekly" {
		stored = account.QuotaWeeklyWindowStart
	}
	if stored > 0 && stored < run.WindowStart {
		resetSubscriptionAccountQuotaToWindow(account, run.Scope, run.WindowStart)
	}
	r.resetRuns[key] = true
	return nil
}

func (r *Repository) recordQuotaResetRunDB(ctx context.Context, run *biz.SubscriptionAccountQuotaResetRun) error {
	model := subscriptionAccountQuotaResetRunModel{
		SubscriptionAccountID: run.AccountID,
		Scope:                 run.Scope,
		WindowStart:           run.WindowStart,
		Strategy:              run.Strategy,
		Timezone:              run.Timezone,
		ResetAt:               run.ResetAt.Unix(),
	}
	result := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
	if result.Error != nil {
		// Map duplicate-key errors to the sentinel so the sweeper can no-op.
		if isDuplicateKeyErr(result.Error) {
			return biz.ErrQuotaResetRunDuplicate
		}
		return result.Error
	}
	if result.RowsAffected == 0 {
		return biz.ErrQuotaResetRunDuplicate
	}
	return nil
}

func (r *Repository) recordQuotaResetAndResetDB(ctx context.Context, run *biz.SubscriptionAccountQuotaResetRun) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var account subscriptionAccountModel
		query := tx.Where("id = ?", run.AccountID)
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&account).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		currentAccount, err := r.subscriptionAccountModelToBiz(&account)
		if err != nil {
			return err
		}
		if !run.MatchesAccount(currentAccount) {
			return biz.ErrQuotaResetRunStale
		}
		model := subscriptionAccountQuotaResetRunModel{
			SubscriptionAccountID: run.AccountID,
			Scope:                 run.Scope,
			WindowStart:           run.WindowStart,
			Strategy:              run.Strategy,
			Timezone:              run.Timezone,
			ResetAt:               run.ResetAt.Unix(),
		}
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model)
		if result.Error != nil {
			if isDuplicateKeyErr(result.Error) {
				return biz.ErrQuotaResetRunDuplicate
			}
			return result.Error
		}
		if result.RowsAffected == 0 {
			return biz.ErrQuotaResetRunDuplicate
		}
		stored := account.QuotaDailyWindowStart
		if run.Scope == "weekly" {
			stored = account.QuotaWeeklyWindowStart
		}
		if stored <= 0 || stored >= run.WindowStart {
			return nil
		}
		updates := subscriptionAccountQuotaResetUpdatesToWindow(run.Scope, run.WindowStart)
		if updates == nil {
			return nil
		}
		updates["updated_at"] = now()
		res := tx.Model(&subscriptionAccountModel{}).Where("id = ?", run.AccountID).Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return biz.ErrSubscriptionAccountNotFound
		}
		return nil
	})
}

// ClearRecoveryMetadata strips the recovery/unschedulable markers from the
// account metadata JSON blob. It is a targeted update so concurrent writers
// (e.g. a snapshot recording) do not lose unrelated metadata keys.
func (r *Repository) ClearRecoveryMetadata(ctx context.Context, accountID int64) error {
	if accountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	if r.db != nil {
		return r.clearRecoveryMetadataDB(ctx, accountID)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	account.Metadata = clearSubscriptionAccountRecoveryMetadata(account.Metadata)
	account.UpdatedAt = now()
	return nil
}

func (r *Repository) clearRecoveryMetadataDB(ctx context.Context, accountID int64) error {
	return r.updateSubscriptionAccountMetadataDB(ctx, accountID, clearSubscriptionAccountRecoveryMetadata)
}

// clearSubscriptionAccountRecoveryMetadata removes the recovery-policy and
// unschedulable marker keys from the metadata JSON blob. It preserves all
// other keys (e.g. last_error is cleared separately by ClearSubscriptionAccountError).
func clearSubscriptionAccountRecoveryMetadata(raw string) string {
	values := subscriptionAccountMetadata(raw)
	if values == nil {
		return raw
	}
	delete(values, "recovery_policy")
	delete(values, "recovery_revision")
	delete(values, "unschedulable_reason")
	delete(values, "unschedulable_since")
	delete(values, "unschedulable_until")
	delete(values, "expected_recovery_at")
	if len(values) == 0 {
		return ""
	}
	b, err := jsonx.Marshal(values)
	if err != nil {
		return raw
	}
	return string(b)
}

// isDuplicateKeyErr reports whether err is a unique-constraint violation,
// covering MySQL, SQLite and Postgres drivers used by this project.
func isDuplicateKeyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Duplicate entry") ||
		strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key value")
}

// stampRecoveryMetadata writes the recovery-policy and unschedulable marker
// keys into the account metadata JSON blob so the recovery sweeper and the
// admin UI can classify why an account is unschedulable. It preserves all
// other keys (e.g. provider-specific metadata).
//
// The recovery policy is derived from the reason text: 401/403 -> manual
// (never auto-recover), 429/5xx/529 -> auto (TTL-based), otherwise rolling.
func stampRecoveryMetadata(raw, reason string, untilUnix, nowUnix int64) string {
	values := subscriptionAccountMetadata(raw)
	if values == nil {
		values = make(map[string]any)
	}
	values["last_error"] = reason
	policy := recoveryPolicyForReason(reason)
	if values["recovery_policy"] == biz.RecoveryPolicyManual && policy != biz.RecoveryPolicyManual {
		// A later 429/quota event is not manual confirmation that the prior
		// authorization failure was resolved. Retain its blocking cause.
		policy = biz.RecoveryPolicyManual
	} else {
		values["unschedulable_reason"] = reason
		values["unschedulable_since"] = nowUnix
		values["unschedulable_until"] = untilUnix
	}
	values["recovery_policy"] = policy
	// Each incident has its own version, even for identical same-second failures.
	values["recovery_revision"] = uuid.NewString()
	if policy == biz.RecoveryPolicyAuto && untilUnix > 0 {
		values["expected_recovery_at"] = untilUnix
	} else {
		delete(values, "expected_recovery_at")
	}
	b, err := jsonx.Marshal(values)
	if err != nil {
		return raw
	}
	return string(b)
}

// recoveryPolicyForReason infers the recovery policy from an unschedulable
// reason string. The reason typically embeds the upstream status code
// (e.g. "status=401", "upstream 429"). 401/403 -> manual; 429/5xx/529 -> auto;
// otherwise rolling.
func recoveryPolicyForReason(reason string) string {
	lower := strings.ToLower(reason)
	switch {
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"),
		strings.Contains(lower, "invalid_grant"), strings.Contains(lower, "invalid grant"), strings.Contains(lower, "invalid refresh"), strings.Contains(lower, "no refresh_token"):
		return biz.RecoveryPolicyManual
	// Codex snapshot exhaustion: a transient upstream quota condition that
	// clears when the upstream snapshot resets. Must NOT be treated as manual
	// (the review H1/H2 found codex-exhausted accounts were permanently
	// disabled because AutoPauseAccount forced manual). The recovery sweeper's
	// codex branch waits for CodexSnapshotQuotaExceeded() to flip false.
	case strings.Contains(lower, "codex"), strings.Contains(lower, "snapshot"):
		return biz.RecoveryPolicyCodex
	// Local quota window exhaustion: clears when the local window resets.
	case strings.Contains(lower, "quota"):
		return biz.RecoveryPolicyQuota
	case strings.Contains(lower, "429"), strings.Contains(lower, "rate limit"), strings.Contains(lower, "529"), strings.Contains(lower, "overloaded"), strings.Contains(lower, "5xx"), strings.Contains(lower, "500"), strings.Contains(lower, "502"), strings.Contains(lower, "503"), strings.Contains(lower, "504"):
		return biz.RecoveryPolicyAuto
	default:
		return biz.RecoveryPolicyRolling
	}
}

// StampQuotaAlertMetadata records the last-emitted quota alert kind and
// timestamp on the account metadata blob so the alert evaluator can dedupe
// within its configured window. Other metadata keys are preserved.
func (r *Repository) StampQuotaAlertMetadata(ctx context.Context, accountID int64, kind string, alertAt int64) error {
	if accountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	if r.db != nil {
		return r.stampQuotaAlertMetadataDB(ctx, accountID, kind, alertAt)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	account.Metadata = setSubscriptionAccountMetadataValue(setSubscriptionAccountMetadataValue(account.Metadata, "last_quota_alert_kind", kind), "last_quota_alert_at", strconv.FormatInt(alertAt, 10))
	account.UpdatedAt = now()
	return nil
}

func (r *Repository) stampQuotaAlertMetadataDB(ctx context.Context, accountID int64, kind string, alertAt int64) error {
	return r.updateSubscriptionAccountMetadataDB(ctx, accountID, func(raw string) string {
		return setSubscriptionAccountMetadataValue(setSubscriptionAccountMetadataValue(raw, "last_quota_alert_kind", kind), "last_quota_alert_at", strconv.FormatInt(alertAt, 10))
	})
}
