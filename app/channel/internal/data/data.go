package data

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/pkg/safecast"
	"micro-one-api/pkg/wildcard"
	"micro-one-api/platform/database/xdb"
	appcrypto "micro-one-api/platform/security/crypto"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Repository struct {
	db          *gorm.DB
	redis       *redis.Client
	channels    map[int64]*biz.Channel
	subAccounts map[int64]*biz.SubscriptionAccount
	quotaEvents map[string]biz.SubscriptionAccountQuotaEventAggregate
	resetRuns   map[string]bool
	lock        sync.RWMutex
	encKey      []byte // AES key for encrypting API keys at rest (nil = no encryption)

	// Model registry memory store (方案B) — used only when db == nil.
	models                    map[int64]*biz.Model
	modelAliases              map[int64]*biz.ModelAlias
	modelChannelMappings      map[int64]*biz.ModelChannelMapping
	modelSubscriptionMappings map[int64]*biz.ModelSubscriptionMapping
	// Sprint 4: usage stats memory store
	modelUsageStats      map[int64]*biz.ModelUsageStat
	modelUsageStatNextID int64
	// Monotonic counters for memory-mode ID generation (avoids ID reuse
	// after deletes, which len(map)+1 would cause).
	modelNextID           int64
	modelAliasNextID      int64
	modelMappingNextID    int64
	modelSubMappingNextID int64
}

type channelModel struct {
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
	RestrictModels                    bool    `gorm:"column:restrict_models"`
}

func (channelModel) TableName() string { return "channels" }

type abilityModel struct {
	Group     string `gorm:"column:group"`
	Model     string `gorm:"column:model"`
	ChannelID int64  `gorm:"column:channel_id"`
	Enabled   bool   `gorm:"column:enabled"`
	Priority  *int64 `gorm:"column:priority"`
}

func (abilityModel) TableName() string { return "abilities" }

type subscriptionAccountModel struct {
	ID           int64   `gorm:"column:id"`
	Name         string  `gorm:"column:name"`
	Platform     string  `gorm:"column:platform"`
	AccountType  string  `gorm:"column:account_type"`
	Status       int32   `gorm:"column:status"`
	Group        string  `gorm:"column:group"`
	Models       string  `gorm:"column:models"`
	Priority     int64   `gorm:"column:priority"`
	BaseURL      *string `gorm:"column:base_url"`
	AccessToken  *string `gorm:"column:access_token"`
	RefreshToken *string `gorm:"column:refresh_token"`
	ExpiresAt    int64   `gorm:"column:expires_at"`
	AccountID    string  `gorm:"column:account_id"`
	Fingerprint  *string `gorm:"column:fingerprint"`
	Metadata     *string `gorm:"column:metadata"`
	CreatedAt    int64   `gorm:"column:created_at"`
	UpdatedAt    int64   `gorm:"column:updated_at"`

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

type subscriptionAccountAbilityModel struct {
	ID        int64  `gorm:"column:id"`
	Group     string `gorm:"column:group"`
	Model     string `gorm:"column:model"`
	Platform  string `gorm:"column:platform"`
	AccountID int64  `gorm:"column:account_id"`
	Enabled   bool   `gorm:"column:enabled"`
	Priority  *int64 `gorm:"column:priority"`
}

func (subscriptionAccountAbilityModel) TableName() string { return "subscription_account_abilities" }

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
		return newMemoryRepository(), nil
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
		}
	}
	repo := &Repository{db: db, redis: rdb}
	if key := os.Getenv("CHANNEL_ENCRYPTION_KEY"); key != "" {
		repo.encKey = []byte(key)
	}
	return repo, nil
}

func newMemoryRepository() *Repository {
	return &Repository{
		channels:                  make(map[int64]*biz.Channel),
		subAccounts:               make(map[int64]*biz.SubscriptionAccount),
		quotaEvents:               make(map[string]biz.SubscriptionAccountQuotaEventAggregate),
		resetRuns:                 make(map[string]bool),
		models:                    make(map[int64]*biz.Model),
		modelAliases:              make(map[int64]*biz.ModelAlias),
		modelChannelMappings:      make(map[int64]*biz.ModelChannelMapping),
		modelSubscriptionMappings: make(map[int64]*biz.ModelSubscriptionMapping),
		modelUsageStats:           make(map[int64]*biz.ModelUsageStat),
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

func (r *Repository) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]biz.Ability, error) {
	if r.db != nil {
		return r.listAbilitiesByGroupAndModelDB(ctx, group, model)
	}
	return r.listAbilitiesByGroupAndModelMemory(ctx, group, model)
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
	cloned := *account
	cloned.Models = append([]string(nil), account.Models...)
	return &cloned, nil
}

func (r *Repository) ListUnrestrictedChannelsByGroup(ctx context.Context, group string) ([]*biz.Channel, error) {
	if r.db != nil {
		return r.listUnrestrictedChannelsByGroupDB(ctx, group)
	}
	return r.listUnrestrictedChannelsByGroupMemory(ctx, group)
}

// listUnrestrictedChannelsByGroupDB returns enabled channels in the group
// whose restrict_models flag is false (catch-all channels). They accept any
// model not matched by the abilities table. See docs §9.3 #2.
func (r *Repository) listUnrestrictedChannelsByGroupDB(ctx context.Context, group string) ([]*biz.Channel, error) {
	var models []channelModel
	// channels.group is a CSV; match exact, prefix, suffix, or infix to stay
	// cross-driver compatible (no FIND_IN_SET in SQLite/Postgres).
	if err := r.db.WithContext(ctx).
		Where("status = ? AND restrict_models = ?", biz.ChannelStatusEnabled, false).
		Where("`group` = ? OR `group` LIKE ? OR `group` LIKE ? OR `group` LIKE ?",
			group, group+",%", "%,"+group, "%,"+group+",%").
		Find(&models).Error; err != nil {
		return nil, err
	}
	result := make([]*biz.Channel, 0, len(models))
	for i := range models {
		result = append(result, r.modelToChannel(&models[i]))
	}
	return result, nil
}

func (r *Repository) listUnrestrictedChannelsByGroupMemory(_ context.Context, group string) ([]*biz.Channel, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	result := make([]*biz.Channel, 0)
	for _, channel := range r.channels {
		if channel.Status != biz.ChannelStatusEnabled {
			continue
		}
		if channel.RestrictModels {
			continue
		}
		for _, channelGroup := range biz.SplitCSV(channel.Group) {
			if channelGroup == group {
				cloned := *channel
				cloned.Models = append([]string(nil), channel.Models...)
				result = append(result, &cloned)
				break
			}
		}
	}
	return result, nil
}

func (r *Repository) ListSubscriptionAccountAbilities(ctx context.Context, group, model, platform string) ([]biz.SubscriptionAccountAbility, error) {
	if r.db != nil {
		return r.listSubscriptionAccountAbilitiesDB(ctx, group, model, platform)
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	abilities := make([]biz.SubscriptionAccountAbility, 0)
	for _, account := range r.subAccounts {
		if account.Status != biz.ChannelStatusEnabled {
			continue
		}
		if platform != "" && account.Platform != platform {
			continue
		}
		for _, accountGroup := range biz.SplitCSV(account.Group) {
			if accountGroup != group {
				continue
			}
			for _, accountModel := range account.Models {
				if !strings.EqualFold(accountModel, model) && !wildcard.Match(accountModel, model) {
					continue
				}
				abilities = append(abilities, biz.SubscriptionAccountAbility{
					Group:     group,
					Model:     model,
					Platform:  account.Platform,
					AccountID: account.ID,
					Enabled:   true,
					Priority:  account.Priority,
				})
			}
		}
	}
	return abilities, nil
}

func (r *Repository) ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*biz.SubscriptionAccount, int64, error) {
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
		if account.ExpiresAt > 0 && account.ExpiresAt <= threshold {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func (r *Repository) CreateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	if r.db != nil {
		return r.createSubscriptionAccountDB(ctx, account)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account.ID = int64(len(r.subAccounts) + 1)
	r.subAccounts[account.ID] = account
	return nil
}

func (r *Repository) UpdateSubscriptionAccount(ctx context.Context, account *biz.SubscriptionAccount) error {
	if r.db != nil {
		return r.updateSubscriptionAccountDB(ctx, account)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	if _, ok := r.subAccounts[account.ID]; !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	r.subAccounts[account.ID] = account
	return nil
}

func (r *Repository) DeleteSubscriptionAccount(ctx context.Context, accountID int64) error {
	if r.db != nil {
		return r.deleteSubscriptionAccountDB(ctx, accountID)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	delete(r.subAccounts, accountID)
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
	account.Metadata = setSubscriptionAccountMetadataValue(account.Metadata, "last_error", message)
	account.UpdatedAt = time.Now().Unix()
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
	account.Metadata = stampRecoveryMetadata(account.Metadata, reason, until.Unix(), time.Now().Unix())
	account.UpdatedAt = time.Now().Unix()
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
	account.UpdatedAt = time.Now().Unix()
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
		if value, err := safecast.Int64ToInt32(account.QuotaResetAt - time.Now().Unix()); err == nil {
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
	policy := recoveryPolicyForReason(reason)
	account.Metadata = stampRecoveryMetadata(account.Metadata, reason, 0, time.Now().Unix())
	account.Metadata = setSubscriptionAccountMetadataValue(account.Metadata, "recovery_policy", policy)
	if policy == biz.RecoveryPolicyManual {
		account.Status = biz.ChannelStatusDisabled
	}
	account.UpdatedAt = time.Now().Unix()
	return nil
}

func (r *Repository) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	if r.db != nil {
		return r.listAvailableModelsDB(ctx, group)
	}
	return r.listAvailableModelsMemory(ctx, group)
}

func (r *Repository) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*biz.Channel, int64, error) {
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
	return r.subscriptionAccountModelToBiz(&model), nil
}

func (r *Repository) listSubscriptionAccountAbilitiesDB(ctx context.Context, group, model, platform string) ([]biz.SubscriptionAccountAbility, error) {
	// P1 (#4): support wildcard abilities for subscription accounts too.
	// Exact (case-insensitive) match first; if none, scan wildcard-pattern
	// rows and keep those matching the requested model. See
	// docs/model-management-design.md §9.3 #4.
	query := r.db.WithContext(ctx).Model(&subscriptionAccountAbilityModel{}).
		Where("`group` = ? AND LOWER(model) = ? AND enabled = ?", group, strings.ToLower(model), true)
	if platform != "" {
		query = query.Where("platform = ?", platform)
	}
	var rows []subscriptionAccountAbilityModel
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		patternQuery := r.db.WithContext(ctx).Model(&subscriptionAccountAbilityModel{}).
			Where("`group` = ? AND enabled = ? AND (model LIKE ? OR model LIKE ?)", group, true, "%*%", "%?%")
		if platform != "" {
			patternQuery = patternQuery.Where("platform = ?", platform)
		}
		var patternRows []subscriptionAccountAbilityModel
		if err := patternQuery.Find(&patternRows).Error; err != nil {
			return nil, err
		}
		for _, row := range patternRows {
			if wildcard.Match(row.Model, model) {
				rows = append(rows, row)
			}
		}
	}
	abilities := make([]biz.SubscriptionAccountAbility, 0, len(rows))
	for _, row := range rows {
		priority := int64(0)
		if row.Priority != nil {
			priority = *row.Priority
		}
		abilities = append(abilities, biz.SubscriptionAccountAbility{
			Group:     row.Group,
			Model:     row.Model,
			Platform:  row.Platform,
			AccountID: row.AccountID,
			Enabled:   row.Enabled,
			Priority:  priority,
		})
	}
	return abilities, nil
}

func (r *Repository) listSubscriptionAccountsDB(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*biz.SubscriptionAccount, int64, error) {
	query := r.db.WithContext(ctx).Model(&subscriptionAccountModel{})
	if keyword != "" {
		query = query.Where("name LIKE ? OR account_id LIKE ?", "%"+escapeLike(keyword)+"%", "%"+escapeLike(keyword)+"%")
	}
	if group != "" {
		query = query.Where("`group` = ?", group)
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
		result[i] = r.subscriptionAccountModelToBiz(&m)
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
		Where("expires_at > 0 AND expires_at <= ?", threshold).
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
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := r.subscriptionAccountBizToModel(account)
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		account.ID = model.ID
		return r.syncSubscriptionAccountAbilitiesTx(tx, account)
	})
}

func (r *Repository) updateSubscriptionAccountDB(ctx context.Context, account *biz.SubscriptionAccount) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := r.subscriptionAccountBizToModel(account)
		if err := tx.Model(&subscriptionAccountModel{}).Where("id = ?", account.ID).Updates(map[string]interface{}{
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
			"updated_at":                model.UpdatedAt,
		}).Error; err != nil {
			return err
		}
		return r.syncSubscriptionAccountAbilitiesTx(tx, account)
	})
}

func (r *Repository) deleteSubscriptionAccountDB(ctx context.Context, accountID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", accountID).Delete(&subscriptionAccountModel{}).Error; err != nil {
			return err
		}
		if err := tx.Where("account_id = ?", accountID).Delete(&subscriptionAccountAbilityModel{}).Error; err != nil {
			return err
		}
		return tx.Where("account_id = ?", accountID).Delete(&accountQuotaSnapshotModel{}).Error
	})
}

func (r *Repository) changeSubscriptionAccountStatusDB(ctx context.Context, accountID int64, status int32) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Update("status", status).Error; err != nil {
			return err
		}
		enabled := status == biz.ChannelStatusEnabled
		return tx.Model(&subscriptionAccountAbilityModel{}).Where("account_id = ?", accountID).Update("enabled", enabled).Error
	})
}

func (r *Repository) setSubscriptionAccountErrorDB(ctx context.Context, accountID int64, message string) error {
	account, err := r.findSubscriptionAccountByIDDB(ctx, accountID)
	if err != nil {
		return err
	}
	metadata := setSubscriptionAccountMetadataValue(account.Metadata, "last_error", message)
	return r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]interface{}{
		"metadata":   stringPtr(metadata),
		"updated_at": time.Now().Unix(),
	}).Error
}

func (r *Repository) setTempUnschedulableDB(ctx context.Context, accountID int64, until time.Time, reason string) error {
	account, err := r.findSubscriptionAccountByIDDB(ctx, accountID)
	if err != nil {
		return err
	}
	metadata := stampRecoveryMetadata(account.Metadata, reason, until.Unix(), time.Now().Unix())
	return r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]interface{}{
		"rate_limited_until": until.Unix(),
		"metadata":           stringPtr(metadata),
		"updated_at":         time.Now().Unix(),
	}).Error
}

func (r *Repository) clearTempUnschedulableDB(ctx context.Context, accountID int64) error {
	return r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]interface{}{
		"rate_limited_until": 0,
		"updated_at":         time.Now().Unix(),
	}).Error
}

func (r *Repository) recordAccountQuotaSnapshotDB(ctx context.Context, snapshot *biz.AccountQuotaSnapshot) error {
	model := accountQuotaSnapshotBizToModel(snapshot)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(model).Error; err != nil {
			return err
		}
		updates := map[string]interface{}{
			"updated_at": time.Now().Unix(),
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
		if result.RowsAffected == 0 {
			return biz.ErrSubscriptionAccountNotFound
		}
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
		account := r.subscriptionAccountModelToBiz(&model)
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
				CreatedAt:             time.Now().Unix(),
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
		return tx.Model(&subscriptionAccountModel{}).Where("id = ?", usage.AccountID).Updates(map[string]interface{}{
			"quota_used_usd":            account.QuotaUsedUSD,
			"quota_5h_used_usd":         account.Quota5hUsedUSD,
			"quota_5h_window_start":     account.Quota5hWindowStart,
			"quota_daily_used_usd":      account.QuotaDailyUsedUSD,
			"quota_daily_window_start":  account.QuotaDailyWindowStart,
			"quota_weekly_used_usd":     account.QuotaWeeklyUsedUSD,
			"quota_weekly_window_start": account.QuotaWeeklyWindowStart,
			"last_used_at":              usage.OccurredAt.Unix(),
			"updated_at":                time.Now().Unix(),
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
			COALESCE(AVG(rate_multiplier), 0) AS average_rate_multiplier,
			COUNT(*) AS count,
			COALESCE(MAX(occurred_at), 0) AS last_occurred_at`).
		Group("subscription_account_id").
		Order("charged_usd DESC")
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
	updates["updated_at"] = time.Now().Unix()
	result := r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	return nil
}

func (r *Repository) autoPauseAccountDB(ctx context.Context, accountID int64, reason string) error {
	account, err := r.findSubscriptionAccountByIDDB(ctx, accountID)
	if err != nil {
		return err
	}
	// Derive the recovery policy from the reason (review H1/H2 fix). Codex
	// snapshot exhaustion and local quota exhaustion are transient: keep the
	// account ENABLED so the recovery sweeper can clear the unschedulable
	// markers once the upstream snapshot / local window resets. Only
	// authorization errors (manual policy) disable the account.
	policy := recoveryPolicyForReason(reason)
	metadata := stampRecoveryMetadata(account.Metadata, reason, 0, time.Now().Unix())
	metadata = setSubscriptionAccountMetadataValue(metadata, "recovery_policy", policy)
	status := biz.ChannelStatusEnabled
	abilityEnabled := true
	if policy == biz.RecoveryPolicyManual {
		status = biz.ChannelStatusDisabled
		abilityEnabled = false
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]interface{}{
			"status":     status,
			"metadata":   stringPtr(metadata),
			"updated_at": time.Now().Unix(),
		}).Error; err != nil {
			return err
		}
		if err := tx.Model(&subscriptionAccountAbilityModel{}).Where("account_id = ?", accountID).Update("enabled", abilityEnabled).Error; err != nil {
			return err
		}
		return tx.Model(&accountQuotaSnapshotModel{}).Where("account_id = ?", accountID).Updates(map[string]interface{}{
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
		if err := tx.Where("id = ?", event.ChannelID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrChannelNotFound
			}
			return err
		}
		channel := r.modelToChannel(&model)
		applyHealthEvent(channel, event, threshold, cooldown)
		if err := tx.Model(&channelModel{}).Where("id = ?", event.ChannelID).Updates(map[string]interface{}{
			"test_time":                   channel.TestTime,
			"response_time":               channel.ResponseTime,
			"health_status":               channel.EffectiveHealthStatus(),
			"health_last_error":           stringPtr(channel.HealthLastError),
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
	baseURL := ""
	if model.BaseURL != nil {
		baseURL = *model.BaseURL
	}
	priority := int64(0)
	if model.Priority != nil {
		priority = *model.Priority
	}
	return &biz.Channel{
		ID:                                model.ID,
		Type:                              model.Type,
		Name:                              model.Name,
		Status:                            model.Status,
		BaseURL:                           baseURL,
		Group:                             model.Group,
		Models:                            biz.SplitCSV(model.Models),
		Priority:                          priority,
		Key:                               r.decryptKey(model.Key),
		Weight:                            derefUint(model.Weight),
		CreatedTime:                       model.CreatedTime,
		TestTime:                          model.TestTime,
		ResponseTime:                      model.ResponseTime,
		Balance:                           model.Balance,
		BalanceUpdatedTime:                model.BalanceUpdatedTime,
		BalanceRefreshLastError:           derefString(model.BalanceRefreshLastError),
		BalanceRefreshLastSuccessTime:     model.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: model.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      model.HealthStatus,
		HealthLastError:                   derefString(model.HealthLastError),
		HealthLastSuccessTime:             model.HealthLastSuccessTime,
		HealthLastFailureTime:             model.HealthLastFailureTime,
		HealthConsecutiveFailures:         model.HealthConsecutiveFailures,
		CircuitOpenedUntil:                model.CircuitOpenedUntil,
		UsedQuota:                         model.UsedQuota,
		ModelMapping:                      derefString(model.ModelMapping),
		SystemPrompt:                      derefString(model.SystemPrompt),
		Config:                            biz.DecodeChannelConfig(model.Config),
	}, nil
}

func (r *Repository) listAbilitiesByGroupAndModelDB(ctx context.Context, group, model string) ([]biz.Ability, error) {
	// P1 (#4): support wildcard abilities. First try exact (case-insensitive)
	// model matching. If that yields rows, return them. If not, scan abilities
	// whose model column contains a wildcard metacharacter and keep those
	// whose pattern matches the requested model (e.g. "claude-*" matches
	// "claude-sonnet-4"). This lets a single ability row route a whole
	// family of model names. See docs/model-management-design.md §9.3 #4.
	var rows []abilityModel
	if err := r.db.WithContext(ctx).
		Where("`group` = ? AND LOWER(model) = ? AND enabled = ?", group, strings.ToLower(model), true).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		var patternRows []abilityModel
		if err := r.db.WithContext(ctx).
			Where("`group` = ? AND enabled = ? AND (model LIKE ? OR model LIKE ?)", group, true, "%*%", "%?%").
			Find(&patternRows).Error; err != nil {
			return nil, err
		}
		for _, row := range patternRows {
			if wildcard.Match(row.Model, model) {
				rows = append(rows, row)
			}
		}
	}
	abilities := make([]biz.Ability, 0, len(rows))
	for _, row := range rows {
		priority := int64(0)
		if row.Priority != nil {
			priority = *row.Priority
		}
		abilities = append(abilities, biz.Ability{
			Group:     row.Group,
			Model:     row.Model,
			ChannelID: row.ChannelID,
			Enabled:   row.Enabled,
			Priority:  priority,
		})
	}
	return abilities, nil
}

func (r *Repository) listAvailableModelsDB(ctx context.Context, group string) ([]string, error) {
	seen := make(map[string]struct{})

	// ── Legacy: query regular channel abilities ──────────────────────────
	// P1 (#4): exclude wildcard patterns from the advertised model list —
	// "claude-*" / "*" are routing rules, not real models a client can select.
	var channelModels []string
	if err := r.db.WithContext(ctx).
		Model(&abilityModel{}).
		Where("`group` = ? AND enabled = ? AND model NOT LIKE ? AND model NOT LIKE ?", group, true, "%*%", "%?%").
		Distinct("model").
		Pluck("model", &channelModels).Error; err != nil {
		return nil, err
	}
	for _, model := range channelModels {
		seen[strings.ToLower(model)] = struct{}{}
	}

	// ── Legacy: query subscription account abilities ─────────────────────
	var subscriptionModels []string
	if err := r.db.WithContext(ctx).
		Model(&subscriptionAccountAbilityModel{}).
		Where("`group` = ? AND enabled = ? AND model NOT LIKE ? AND model NOT LIKE ?", group, true, "%*%", "%?%").
		Distinct("model").
		Pluck("model", &subscriptionModels).Error; err != nil {
		return nil, err
	}
	for _, model := range subscriptionModels {
		seen[strings.ToLower(model)] = struct{}{}
	}

	// ── Sprint 3: dual-read from model registry tables ───────────────────
	// Query model_channel_mapping joined with models (enabled models served
	// by enabled channels in the requested group). This is the new-table
	// path; the legacy queries above remain as fallback during migration.
	r.addRegistryChannelModelsDB(ctx, group, seen)
	r.addRegistrySubscriptionModelsDB(ctx, group, seen)

	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

// addRegistryChannelModelsDB queries the model registry for models served by
// enabled channels in the given group and adds them to the seen set. Errors
// are silently ignored — if the registry tables do not exist or are empty,
// the legacy path still provides results.
func (r *Repository) addRegistryChannelModelsDB(ctx context.Context, group string, seen map[string]struct{}) {
	var registryModels []string
	// Join model_channel_mapping → models (enabled) → channels (enabled, group match).
	// The channels.group column stores a CSV (e.g. "default,vip"). We use
	// LIKE patterns instead of FIND_IN_SET for cross-driver compatibility
	// (FIND_IN_SET is MySQL-only; SQLite and Postgres do not support it).
	query := r.db.WithContext(ctx).Table("model_channel_mapping AS mcm").
		Select("m.model_id").
		Joins("JOIN models AS m ON m.id = mcm.model_id").
		Joins("JOIN channels AS c ON c.id = mcm.channel_id").
		Where("mcm.enabled = ? AND m.status = ? AND c.status = ?", true, biz.ModelStatusEnabled, biz.ChannelStatusEnabled).
		Where("c.`group` = ? OR c.`group` LIKE ? OR c.`group` LIKE ? OR c.`group` LIKE ?",
			group, group+",%", "%,"+group, "%,"+group+",%").
		Distinct("m.model_id")
	if err := query.Pluck("m.model_id", &registryModels).Error; err != nil {
		return // registry tables may not exist yet; silently skip
	}
	for _, model := range registryModels {
		seen[strings.ToLower(model)] = struct{}{}
	}
}

// addRegistrySubscriptionModelsDB queries the model registry for models served
// by enabled subscription accounts in the given group and adds them to seen.
func (r *Repository) addRegistrySubscriptionModelsDB(ctx context.Context, group string, seen map[string]struct{}) {
	var registryModels []string
	query := r.db.WithContext(ctx).Table("model_subscription_mapping AS msm").
		Select("m.model_id").
		Joins("JOIN models AS m ON m.id = msm.model_id").
		Joins("JOIN subscription_accounts AS sa ON sa.id = msm.subscription_account_id").
		Where("msm.enabled = ? AND m.status = ? AND sa.status = ?", true, biz.ModelStatusEnabled, biz.ChannelStatusEnabled).
		Where("msm.group_name = ?", group).
		Distinct("m.model_id")
	if err := query.Pluck("m.model_id", &registryModels).Error; err != nil {
		return // registry tables may not exist yet; silently skip
	}
	for _, model := range registryModels {
		seen[strings.ToLower(model)] = struct{}{}
	}
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

func (r *Repository) listAbilitiesByGroupAndModelMemory(_ context.Context, group, model string) ([]biz.Ability, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	abilities := make([]biz.Ability, 0)
	for _, channel := range r.channels {
		if channel.Status != biz.ChannelStatusEnabled {
			continue
		}
		for _, channelGroup := range biz.SplitCSV(channel.Group) {
			if channelGroup != group {
				continue
			}
			for _, channelModel := range channel.Models {
				if !strings.EqualFold(channelModel, model) && !wildcard.Match(channelModel, model) {
					continue
				}
				abilities = append(abilities, biz.Ability{
					Group:     group,
					Model:     model,
					ChannelID: channel.ID,
					Enabled:   true,
					Priority:  channel.Priority,
				})
			}
		}
	}
	return abilities, nil
}

func (r *Repository) listAvailableModelsMemory(_ context.Context, group string) ([]string, error) {
	r.lock.RLock()
	defer r.lock.RUnlock()
	seen := make(map[string]struct{})

	// ── Legacy: collect models from regular channels ─────────────────────
	// P1 (#4): wildcard patterns (e.g. "claude-*", "*") are routing rules,
	// not real advertiseable models — listing them in /v1/models would expose
	// internal glob keys to clients. Skip any model name that is itself a
	// wildcard pattern.
	for _, channel := range r.channels {
		if channel.Status != biz.ChannelStatusEnabled {
			continue
		}
		for _, channelGroup := range biz.SplitCSV(channel.Group) {
			if channelGroup != group {
				continue
			}
			for _, model := range channel.Models {
				if wildcard.IsPattern(model) {
					continue
				}
				seen[strings.ToLower(model)] = struct{}{}
			}
		}
	}

	// ── Legacy: collect models from subscription accounts ────────────────
	for _, account := range r.subAccounts {
		if account.Status != biz.ChannelStatusEnabled {
			continue
		}
		for _, accountGroup := range biz.SplitCSV(account.Group) {
			if accountGroup != group {
				continue
			}
			for _, model := range account.Models {
				if wildcard.IsPattern(model) {
					continue
				}
				seen[strings.ToLower(model)] = struct{}{}
			}
		}
	}

	// ── Sprint 3: dual-read from model registry (memory mode) ────────────
	// Collect model_ids from the in-memory model_channel_mappings where the
	// model is enabled and the channel is enabled and in the requested group.
	for _, mcm := range r.modelChannelMappings {
		if !mcm.Enabled {
			continue
		}
		model, ok := r.models[mcm.ModelPK]
		if !ok || model.Status != biz.ModelStatusEnabled {
			continue
		}
		channel, ok := r.channels[mcm.ChannelID]
		if !ok || channel.Status != biz.ChannelStatusEnabled {
			continue
		}
		for _, channelGroup := range biz.SplitCSV(channel.Group) {
			if channelGroup == group {
				seen[strings.ToLower(model.ModelID)] = struct{}{}
				break
			}
		}
	}

	// Collect model_ids from model_subscription_mappings.
	for _, msm := range r.modelSubscriptionMappings {
		if !msm.Enabled {
			continue
		}
		model, ok := r.models[msm.ModelPK]
		if !ok || model.Status != biz.ModelStatusEnabled {
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

	models := make([]string, 0, len(seen))
	for model := range seen {
		models = append(models, model)
	}
	sort.Strings(models)
	return models, nil
}

func (r *Repository) listChannelsDB(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*biz.Channel, int64, error) {
	query := r.db.WithContext(ctx).Model(&channelModel{})
	if keyword != "" {
		query = query.Where("name LIKE ?", "%"+escapeLike(keyword)+"%")
	}
	if group != "" {
		query = query.Where("`group` = ?", group)
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
		result[i] = r.modelToChannel(&m)
	}
	return result, total, nil
}

func (r *Repository) createChannelDB(ctx context.Context, channel *biz.Channel) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := r.channelToModel(channel)
		if err := tx.Create(model).Error; err != nil {
			return err
		}
		channel.ID = model.ID
		return r.syncAbilitiesTx(tx, channel)
	})
}

func (r *Repository) updateChannelDB(ctx context.Context, channel *biz.Channel) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		model := r.channelToModel(channel)
		if err := tx.Model(&channelModel{}).Where("id = ?", channel.ID).Updates(map[string]interface{}{
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
		return r.syncAbilitiesTx(tx, channel)
	})
}

func (r *Repository) deleteChannelDB(ctx context.Context, channelID int64) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", channelID).Delete(&channelModel{}).Error; err != nil {
			return err
		}
		return tx.Where("channel_id = ?", channelID).Delete(&abilityModel{}).Error
	})
}

func (r *Repository) changeStatusDB(ctx context.Context, channelID int64, status int32) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&channelModel{}).Where("id = ?", channelID).Update("status", status).Error; err != nil {
			return err
		}
		enabled := status == biz.ChannelStatusEnabled
		return tx.Model(&abilityModel{}).Where("channel_id = ?", channelID).Update("enabled", enabled).Error
	})
}

// syncAbilitiesTx rewrites ability rows for one channel: every old row for
// channel_id is deleted, then a fresh row is inserted for each (group, model)
// pair derived from the channel. Caller MUST pass an active gorm transaction.
func (r *Repository) syncAbilitiesTx(tx *gorm.DB, channel *biz.Channel) error {
	if err := tx.Where("channel_id = ?", channel.ID).Delete(&abilityModel{}).Error; err != nil {
		return err
	}
	enabled := channel.Status == biz.ChannelStatusEnabled
	priority := channel.Priority
	rows := make([]abilityModel, 0)
	for _, group := range biz.SplitCSV(channel.Group) {
		if group == "" {
			continue
		}
		for _, model := range channel.Models {
			if model == "" {
				continue
			}
			rows = append(rows, abilityModel{
				Group:     group,
				Model:     model,
				ChannelID: channel.ID,
				Enabled:   enabled,
				Priority:  &priority,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

func (r *Repository) syncSubscriptionAccountAbilitiesTx(tx *gorm.DB, account *biz.SubscriptionAccount) error {
	if err := tx.Where("account_id = ?", account.ID).Delete(&subscriptionAccountAbilityModel{}).Error; err != nil {
		return err
	}
	enabled := account.Status == biz.ChannelStatusEnabled
	priority := account.Priority
	rows := make([]subscriptionAccountAbilityModel, 0)
	for _, group := range biz.SplitCSV(account.Group) {
		if group == "" {
			continue
		}
		for _, model := range account.Models {
			if model == "" {
				continue
			}
			rows = append(rows, subscriptionAccountAbilityModel{
				Group:     group,
				Model:     model,
				Platform:  account.Platform,
				AccountID: account.ID,
				Enabled:   enabled,
				Priority:  &priority,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
}

// encryptKey encrypts an API key for storage. Returns plaintext if no encryption key is set.
func (r *Repository) encryptKey(key string) string {
	if r.encKey == nil || key == "" {
		return key
	}
	encrypted, err := appcrypto.Encrypt(key, r.encKey)
	if err != nil {
		// Log error but return plaintext to avoid data loss
		return key
	}
	return encrypted
}

// decryptKey decrypts an API key from storage. Returns as-is if no encryption key is set.
func (r *Repository) decryptKey(key string) string {
	if r.encKey == nil || key == "" {
		return key
	}
	decrypted, err := appcrypto.Decrypt(key, r.encKey)
	if err != nil {
		// If decryption fails, assume it's stored as plaintext (migration scenario)
		return key
	}
	return decrypted
}

func (r *Repository) modelToChannel(m *channelModel) *biz.Channel {
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
		Type:                              m.Type,
		Name:                              m.Name,
		Status:                            m.Status,
		BaseURL:                           baseURL,
		Group:                             m.Group,
		Models:                            biz.SplitCSV(m.Models),
		Priority:                          priority,
		Key:                               r.decryptKey(m.Key),
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
		RestrictModels:                    m.RestrictModels,
		Config:                            biz.DecodeChannelConfig(m.Config),
	}
}

func (r *Repository) channelToModel(ch *biz.Channel) *channelModel {
	return &channelModel{
		ID:                                ch.ID,
		Type:                              ch.Type,
		Name:                              ch.Name,
		Status:                            ch.Status,
		BaseURL:                           strPtr(ch.BaseURL),
		Weight:                            uintPtr(ch.Weight),
		CreatedTime:                       ch.CreatedTime,
		TestTime:                          ch.TestTime,
		ResponseTime:                      ch.ResponseTime,
		Balance:                           ch.Balance,
		BalanceUpdatedTime:                ch.BalanceUpdatedTime,
		BalanceRefreshLastError:           stringPtr(ch.BalanceRefreshLastError),
		BalanceRefreshLastSuccessTime:     ch.BalanceRefreshLastSuccessTime,
		ConsecutiveBalanceRefreshFailures: ch.ConsecutiveBalanceRefreshFailures,
		HealthStatus:                      ch.EffectiveHealthStatus(),
		HealthLastError:                   stringPtr(ch.HealthLastError),
		HealthLastSuccessTime:             ch.HealthLastSuccessTime,
		HealthLastFailureTime:             ch.HealthLastFailureTime,
		HealthConsecutiveFailures:         ch.HealthConsecutiveFailures,
		CircuitOpenedUntil:                ch.CircuitOpenedUntil,
		Models:                            ch.ModelsCSV(),
		Group:                             ch.Group,
		UsedQuota:                         ch.UsedQuota,
		ModelMapping:                      stringPtr(ch.ModelMapping),
		Priority:                          int64Ptr(ch.Priority),
		Key:                               r.encryptKey(ch.Key),
		Config:                            "{}",
		SystemPrompt:                      stringPtr(ch.SystemPrompt),
		RestrictModels:                    ch.RestrictModels,
	}
}

func (r *Repository) subscriptionAccountModelToBiz(m *subscriptionAccountModel) *biz.SubscriptionAccount {
	baseURL := ""
	if m.BaseURL != nil {
		baseURL = *m.BaseURL
	}
	return &biz.SubscriptionAccount{
		ID:                     m.ID,
		Name:                   m.Name,
		Platform:               m.Platform,
		AccountType:            m.AccountType,
		Status:                 m.Status,
		Group:                  m.Group,
		Models:                 biz.SplitCSV(m.Models),
		Priority:               m.Priority,
		BaseURL:                baseURL,
		AccessToken:            r.decryptKey(derefString(m.AccessToken)),
		RefreshToken:           r.decryptKey(derefString(m.RefreshToken)),
		ExpiresAt:              m.ExpiresAt,
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
}

func (r *Repository) subscriptionAccountBizToModel(a *biz.SubscriptionAccount) *subscriptionAccountModel {
	return &subscriptionAccountModel{
		ID:                     a.ID,
		Name:                   a.Name,
		Platform:               a.Platform,
		AccountType:            a.AccountType,
		Status:                 a.Status,
		Group:                  a.Group,
		Models:                 a.ModelsCSV(),
		Priority:               a.Priority,
		BaseURL:                strPtr(a.BaseURL),
		AccessToken:            stringPtr(r.encryptKey(a.AccessToken)),
		RefreshToken:           stringPtr(r.encryptKey(a.RefreshToken)),
		ExpiresAt:              a.ExpiresAt,
		AccountID:              a.AccountID,
		Fingerprint:            stringPtr(a.Fingerprint),
		Metadata:               stringPtr(a.Metadata),
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
	}
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
	account.UpdatedAt = time.Now().Unix()
}

func incrementWindowUsage(used float64, windowStart int64, delta float64, nowUnix int64, window time.Duration) (float64, int64) {
	if windowStart <= 0 || nowUnix-windowStart >= int64(window.Seconds()) {
		return delta, nowUnix
	}
	return used + delta, windowStart
}

func incrementFixedWindowUsage(account *biz.SubscriptionAccount, used float64, windowStart int64, delta float64, now time.Time, scope string) (float64, int64) {
	fixedStart := account.FixedQuotaWindowStart(now, scope)
	if windowStart < fixedStart || windowStart > now.Unix() {
		return delta, fixedStart
	}
	return used + delta, fixedStart
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
	account.UpdatedAt = time.Now().Unix()
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
	account.UpdatedAt = time.Now().Unix()
}

func subscriptionAccountQuotaEventKey(reservationID string, accountID int64, costSource string) string {
	return reservationID + "\x00" + strconv.FormatInt(accountID, 10) + "\x00" + costSource
}

func subscriptionAccountQuotaResetUpdates(scope string) map[string]interface{} {
	switch scope {
	case "total":
		return map[string]interface{}{"quota_used_usd": 0}
	case "5h":
		return map[string]interface{}{"quota_5h_used_usd": 0, "quota_5h_window_start": 0}
	case "daily":
		return map[string]interface{}{"quota_daily_used_usd": 0, "quota_daily_window_start": 0}
	case "weekly":
		return map[string]interface{}{"quota_weekly_used_usd": 0, "quota_weekly_window_start": 0}
	case "all":
		return map[string]interface{}{
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

func subscriptionAccountQuotaResetUpdatesToWindow(scope string, windowStart int64) map[string]interface{} {
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

func strPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64 { return &i }
func uintPtr(i uint32) *uint {
	v := uint(i)
	return &v
}
func stringPtr(s string) *string { return &s }
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

func setSubscriptionAccountMetadataValue(raw, key, value string) string {
	values := subscriptionAccountMetadata(raw)
	if values == nil {
		values = make(map[string]interface{})
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
	b, err := json.Marshal(values)
	if err != nil {
		return raw
	}
	return string(b)
}

func subscriptionAccountMetadata(raw string) map[string]interface{} {
	if strings.TrimSpace(raw) == "" {
		return map[string]interface{}{}
	}
	values := make(map[string]interface{})
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil
	}
	return values
}

func applyHealthEvent(channel *biz.Channel, event biz.ChannelHealthEvent, threshold int32, cooldown time.Duration) {
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
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
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
	resetSubscriptionAccountQuotaToWindow(account, run.Scope, run.WindowStart)
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
		updates := subscriptionAccountQuotaResetUpdatesToWindow(run.Scope, run.WindowStart)
		if updates == nil {
			return nil
		}
		updates["updated_at"] = time.Now().Unix()
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
	account.UpdatedAt = time.Now().Unix()
	return nil
}

func (r *Repository) ClearRecoveryMarkers(ctx context.Context, accountID int64, clearTemp, clearError, clearMeta bool) error {
	if accountID <= 0 {
		return biz.ErrSubscriptionAccountNotFound
	}
	if r.db != nil {
		return r.clearRecoveryMarkersDB(ctx, accountID, clearTemp, clearError, clearMeta)
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	account, ok := r.subAccounts[accountID]
	if !ok {
		return biz.ErrSubscriptionAccountNotFound
	}
	if clearTemp {
		account.RateLimitedUntil = 0
	}
	if clearError {
		account.LastError = ""
		account.Metadata = setSubscriptionAccountMetadataValue(account.Metadata, "last_error", "")
	}
	if clearMeta {
		account.Metadata = clearSubscriptionAccountRecoveryMetadata(account.Metadata)
	}
	account.UpdatedAt = time.Now().Unix()
	return nil
}

func (r *Repository) clearRecoveryMarkersDB(ctx context.Context, accountID int64, clearTemp, clearError, clearMeta bool) error {
	account, err := r.findSubscriptionAccountByIDDB(ctx, accountID)
	if err != nil {
		return err
	}
	metadata := account.Metadata
	updates := map[string]interface{}{
		"updated_at": time.Now().Unix(),
	}
	if clearTemp {
		updates["rate_limited_until"] = 0
	}
	if clearError {
		metadata = setSubscriptionAccountMetadataValue(metadata, "last_error", "")
	}
	if clearMeta {
		metadata = clearSubscriptionAccountRecoveryMetadata(metadata)
	}
	updates["metadata"] = stringPtr(metadata)
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(updates).Error; err != nil {
			return err
		}
		if clearTemp {
			if err := tx.Model(&subscriptionAccountAbilityModel{}).Where("account_id = ?", accountID).Update("enabled", true).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (r *Repository) clearRecoveryMetadataDB(ctx context.Context, accountID int64) error {
	account, err := r.findSubscriptionAccountByIDDB(ctx, accountID)
	if err != nil {
		return err
	}
	metadata := clearSubscriptionAccountRecoveryMetadata(account.Metadata)
	return r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]interface{}{
		"metadata":   stringPtr(metadata),
		"updated_at": time.Now().Unix(),
	}).Error
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
	delete(values, "unschedulable_reason")
	delete(values, "unschedulable_since")
	delete(values, "unschedulable_until")
	delete(values, "expected_recovery_at")
	if len(values) == 0 {
		return ""
	}
	b, err := json.Marshal(values)
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
		values = make(map[string]interface{})
	}
	values["last_error"] = reason
	values["unschedulable_reason"] = reason
	values["unschedulable_since"] = nowUnix
	values["unschedulable_until"] = untilUnix
	policy := recoveryPolicyForReason(reason)
	values["recovery_policy"] = policy
	if policy == biz.RecoveryPolicyAuto && untilUnix > 0 {
		values["expected_recovery_at"] = untilUnix
	} else {
		delete(values, "expected_recovery_at")
	}
	b, err := json.Marshal(values)
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
	case strings.Contains(lower, "401"), strings.Contains(lower, "403"), strings.Contains(lower, "unauthorized"), strings.Contains(lower, "forbidden"):
		return biz.RecoveryPolicyManual
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
	account.UpdatedAt = time.Now().Unix()
	return nil
}

func (r *Repository) stampQuotaAlertMetadataDB(ctx context.Context, accountID int64, kind string, alertAt int64) error {
	account, err := r.findSubscriptionAccountByIDDB(ctx, accountID)
	if err != nil {
		return err
	}
	metadata := setSubscriptionAccountMetadataValue(setSubscriptionAccountMetadataValue(account.Metadata, "last_quota_alert_kind", kind), "last_quota_alert_at", strconv.FormatInt(alertAt, 10))
	return r.db.WithContext(ctx).Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]interface{}{
		"metadata":   stringPtr(metadata),
		"updated_at": time.Now().Unix(),
	}).Error
}
