package biz

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"micro-one-api/internal/pkg/events"

	commonv1 "micro-one-api/api/common/v1"

	"github.com/bytedance/sonic"
)

const (
	ChannelStatusEnabled  = 1
	ChannelStatusDisabled = 2

	ChannelHealthHealthy     = "healthy"
	ChannelHealthDegraded    = "degraded"
	ChannelHealthUnavailable = "unavailable"

	defaultHealthFailureThreshold = 3
	defaultHealthCooldown         = 5 * time.Minute
	defaultHealthAlertNotifyType  = "event"
	defaultHealthAlertTimeout     = 5 * time.Second
)

var ErrChannelNotFound = errors.New("channel not found")
var ErrSubscriptionAccountNotFound = errors.New("subscription account not found")

type ChannelConfig struct {
	APIVersion        string
	Region            string
	LibraryID         string
	Plugin            string
	VertexAIProjectID string
}

// Channel describes the channel snapshot selected for relay.
type Channel struct {
	ID                                int64
	Type                              int32
	Name                              string
	Status                            int32
	BaseURL                           string
	Group                             string
	Models                            []string
	Priority                          int64
	Key                               string
	Weight                            uint32
	CreatedTime                       int64
	TestTime                          int64
	ResponseTime                      int64
	Balance                           float64
	BalanceUpdatedTime                int64
	BalanceRefreshLastError           string
	BalanceRefreshLastSuccessTime     int64
	ConsecutiveBalanceRefreshFailures int32
	HealthStatus                      string
	HealthLastError                   string
	HealthLastSuccessTime             int64
	HealthLastFailureTime             int64
	HealthConsecutiveFailures         int32
	CircuitOpenedUntil                int64
	UsedQuota                         int64
	ModelMapping                      string
	SystemPrompt                      string
	Config                            ChannelConfig
}

// SubscriptionAccount describes an OAuth-backed upstream subscription account.
// It is selected separately from API-key channels but uses the same group,
// model and priority semantics for routing.
type SubscriptionAccount struct {
	ID           int64
	Name         string
	Platform     string
	AccountType  string
	Status       int32
	Group        string
	Models       []string
	Priority     int64
	BaseURL      string
	AccessToken  string
	RefreshToken string
	ExpiresAt    int64
	AccountID    string
	Fingerprint  string
	Metadata     string
	CreatedAt    int64
	UpdatedAt    int64

	// Lifecycle fields (migration 035): token refresh health, rate-limit
	// cooldown and upstream subscription quota-window tracking. Surfaced on
	// the summary so the admin health page can render subscription-account
	// status without a separate query.
	LastUsedAt       int64
	RateLimitedUntil int64
	QuotaUsedPercent float32
	QuotaResetAt     int64
	Concurrency      int32
	LastError        string

	QuotaLimitUSD          float64
	QuotaUsedUSD           float64
	QuotaDailyLimitUSD     float64
	QuotaDailyUsedUSD      float64
	QuotaDailyWindowStart  int64
	QuotaWeeklyLimitUSD    float64
	QuotaWeeklyUsedUSD     float64
	QuotaWeeklyWindowStart int64
	RateMultiplier         float64

	PrimaryQuotaUsedPercent         *float64
	PrimaryQuotaResetAfterSeconds   *int32
	PrimaryQuotaWindowMinutes       *int32
	SecondaryQuotaUsedPercent       *float64
	SecondaryQuotaResetAfterSeconds *int32
	SecondaryQuotaWindowMinutes     *int32
	PrimaryOverSecondaryPercent     *float64
	QuotaSnapshotUpdatedAt          int64
	QuotaSnapshotPaused             bool
}

type AccountQuotaSnapshot struct {
	AccountID                   int64
	PrimaryUsedPercent          *float64
	PrimaryResetAfterSeconds    *int32
	PrimaryWindowMinutes        *int32
	SecondaryUsedPercent        *float64
	SecondaryResetAfterSeconds  *int32
	SecondaryWindowMinutes      *int32
	PrimaryOverSecondaryPercent *float64
	UpdatedAt                   time.Time
	SnapshotPaused              bool
}

type Ability struct {
	Group     string
	Model     string
	ChannelID int64
	Enabled   bool
	Priority  int64
}

type SubscriptionAccountAbility struct {
	Group     string
	Model     string
	Platform  string
	AccountID int64
	Enabled   bool
	Priority  int64
}

type ChannelHealthEvent struct {
	ChannelID    int64
	Success      bool
	Error        string
	ResponseTime int64
	CheckedAt    time.Time
}

type Notifier interface {
	CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) error
}

type HealthAlertConfig struct {
	Enabled    bool
	NotifyType string
	Recipients []string
}

type ChannelRepo interface {
	FindByID(ctx context.Context, channelID int64) (*Channel, error)
	ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]Ability, error)
	FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*SubscriptionAccount, error)
	ListSubscriptionAccountAbilities(ctx context.Context, group, model, platform string) ([]SubscriptionAccountAbility, error)
	ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*SubscriptionAccount, int64, error)
	ListOAuthRefreshCandidates(ctx context.Context, within time.Duration) ([]int64, error)
	CreateSubscriptionAccount(ctx context.Context, account *SubscriptionAccount) error
	UpdateSubscriptionAccount(ctx context.Context, account *SubscriptionAccount) error
	DeleteSubscriptionAccount(ctx context.Context, accountID int64) error
	ChangeSubscriptionAccountStatus(ctx context.Context, accountID int64, status int32) error
	SetSubscriptionAccountError(ctx context.Context, accountID int64, message string) error
	SetTempUnschedulable(ctx context.Context, accountID int64, until time.Time, reason string) error
	ClearTempUnschedulable(ctx context.Context, accountID int64) error
	RecordAccountQuotaSnapshot(ctx context.Context, snapshot *AccountQuotaSnapshot) error
	GetAccountQuotaSnapshot(ctx context.Context, accountID int64) (*AccountQuotaSnapshot, error)
	RecordSubscriptionAccountQuotaUsage(ctx context.Context, accountID int64, costUSD float64, occurredAt time.Time) error
	ResetSubscriptionAccountQuota(ctx context.Context, accountID int64, scope string) error
	AutoPauseAccount(ctx context.Context, accountID int64, reason string) error
	ListAvailableModels(ctx context.Context, group string) ([]string, error)
	ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*Channel, int64, error)
	CreateChannel(ctx context.Context, channel *Channel) error
	UpdateChannel(ctx context.Context, channel *Channel) error
	RecordUsage(ctx context.Context, channelID int64, quota int64) error
	RecordHealth(ctx context.Context, event ChannelHealthEvent, threshold int32, cooldown time.Duration) (*Channel, error)
	DeleteChannel(ctx context.Context, channelID int64) error
	ChangeStatus(ctx context.Context, channelID int64, status int32) error
}

type ChannelUsecase struct {
	repo                   ChannelRepo
	eventBus               events.EventBus
	now                    func() time.Time
	healthFailureThreshold int32
	healthCooldown         time.Duration
	notifier               Notifier
	healthAlert            HealthAlertConfig
	selector               *WeightedSelector
}

func NewChannelUsecase(repo ChannelRepo, eventBus events.EventBus) *ChannelUsecase {
	if eventBus == nil {
		eventBus = events.NewMemoryEventBus()
	}
	return &ChannelUsecase{
		repo:                   repo,
		eventBus:               eventBus,
		now:                    time.Now,
		healthFailureThreshold: healthFailureThresholdFromEnv(),
		healthCooldown:         healthCooldownFromEnv(),
		selector:               NewWeightedSelector(),
	}
}

func (uc *ChannelUsecase) ConfigureHealthAlert(notifier Notifier, cfg HealthAlertConfig) {
	uc.notifier = notifier
	uc.healthAlert = cfg
	if uc.healthAlert.NotifyType == "" {
		uc.healthAlert.NotifyType = defaultHealthAlertNotifyType
	}
	if len(uc.healthAlert.Recipients) == 0 {
		uc.healthAlert.Recipients = []string{""}
	}
}

func (uc *ChannelUsecase) SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*Channel, error) {
	abilities, err := uc.repo.ListAbilitiesByGroupAndModel(ctx, group, model)
	if err != nil {
		return nil, err
	}
	if len(abilities) == 0 {
		return nil, ErrChannelNotFound
	}
	sort.Slice(abilities, func(i, j int) bool {
		return abilities[i].Priority > abilities[j].Priority
	})

	skipPriority := int64(0)
	if excludeFirstPriority {
		skipPriority = abilities[0].Priority
	}
	for i := 0; i < len(abilities); {
		priority := abilities[i].Priority
		tier := make([]*Channel, 0)
		for i < len(abilities) && abilities[i].Priority == priority {
			ability := abilities[i]
			i++
			if excludeFirstPriority && priority == skipPriority {
				continue
			}
			channel, err := uc.repo.FindByID(ctx, ability.ChannelID)
			if err != nil {
				continue
			}
			if channel.SelectableAt(uc.now()) {
				tier = append(tier, channel)
			}
		}
		if len(tier) == 0 {
			continue
		}
		selected, err := uc.selector.Select(ctx, group, channelsToSelectorCandidates(tier))
		if err != nil {
			return nil, err
		}
		for _, channel := range tier {
			if channel.ID == selected.Id {
				return channel, nil
			}
		}
	}

	return nil, ErrChannelNotFound
}

func (uc *ChannelUsecase) GetChannel(ctx context.Context, channelID int64) (*Channel, error) {
	return uc.repo.FindByID(ctx, channelID)
}

func (uc *ChannelUsecase) SelectSubscriptionAccount(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*SubscriptionAccount, error) {
	abilities, err := uc.repo.ListSubscriptionAccountAbilities(ctx, group, model, platform)
	if err != nil {
		return nil, err
	}
	if len(abilities) == 0 {
		return nil, ErrSubscriptionAccountNotFound
	}
	sort.Slice(abilities, func(i, j int) bool {
		return abilities[i].Priority > abilities[j].Priority
	})

	skipPriority := int64(0)
	if excludeFirstPriority {
		skipPriority = abilities[0].Priority
	}
	for i := 0; i < len(abilities); {
		priority := abilities[i].Priority
		tier := make([]*SubscriptionAccount, 0)
		for i < len(abilities) && abilities[i].Priority == priority {
			ability := abilities[i]
			i++
			if excludeFirstPriority && priority == skipPriority {
				continue
			}
			account, err := uc.repo.FindSubscriptionAccountByID(ctx, ability.AccountID)
			if err != nil {
				continue
			}
			if account.Status == ChannelStatusEnabled && account.IsSchedulableAt(uc.now()) {
				tier = append(tier, account)
			}
		}
		if len(tier) == 0 {
			continue
		}
		nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(tier))))
		if err != nil {
			return nil, err
		}
		return tier[nBig.Int64()], nil
	}

	return nil, ErrSubscriptionAccountNotFound
}

func (uc *ChannelUsecase) GetSubscriptionAccount(ctx context.Context, accountID int64) (*SubscriptionAccount, error) {
	return uc.repo.FindSubscriptionAccountByID(ctx, accountID)
}

func (uc *ChannelUsecase) ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*SubscriptionAccount, int64, error) {
	return uc.repo.ListSubscriptionAccounts(ctx, page, pageSize, keyword, group, status, platform)
}

func (uc *ChannelUsecase) ListOAuthRefreshCandidates(ctx context.Context, within time.Duration) ([]int64, error) {
	return uc.repo.ListOAuthRefreshCandidates(ctx, within)
}

func (uc *ChannelUsecase) SetSubscriptionAccountError(ctx context.Context, accountID int64, message string) error {
	return uc.repo.SetSubscriptionAccountError(ctx, accountID, message)
}

func (uc *ChannelUsecase) ClearSubscriptionAccountError(ctx context.Context, accountID int64) error {
	return uc.repo.SetSubscriptionAccountError(ctx, accountID, "")
}

func (uc *ChannelUsecase) SetTempUnschedulable(ctx context.Context, accountID int64, until time.Time, reason string) error {
	return uc.repo.SetTempUnschedulable(ctx, accountID, until, reason)
}

func (uc *ChannelUsecase) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	return uc.repo.ClearTempUnschedulable(ctx, accountID)
}

func (uc *ChannelUsecase) RecordAccountQuotaSnapshot(ctx context.Context, snapshot *AccountQuotaSnapshot) error {
	if snapshot == nil || snapshot.AccountID <= 0 {
		return ErrSubscriptionAccountNotFound
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = uc.now()
	}
	return uc.repo.RecordAccountQuotaSnapshot(ctx, snapshot)
}

func (uc *ChannelUsecase) GetAccountQuotaSnapshot(ctx context.Context, accountID int64) (*AccountQuotaSnapshot, error) {
	return uc.repo.GetAccountQuotaSnapshot(ctx, accountID)
}

func (uc *ChannelUsecase) RecordSubscriptionAccountQuotaUsage(ctx context.Context, accountID int64, costUSD float64, occurredAt time.Time) error {
	if accountID <= 0 {
		return ErrSubscriptionAccountNotFound
	}
	if costUSD <= 0 {
		return nil
	}
	if occurredAt.IsZero() {
		occurredAt = uc.now()
	}
	return uc.repo.RecordSubscriptionAccountQuotaUsage(ctx, accountID, costUSD, occurredAt)
}

func (uc *ChannelUsecase) ResetSubscriptionAccountQuota(ctx context.Context, accountID int64, scope string) error {
	if accountID <= 0 {
		return ErrSubscriptionAccountNotFound
	}
	scope = strings.TrimSpace(strings.ToLower(scope))
	if scope == "" {
		scope = "all"
	}
	return uc.repo.ResetSubscriptionAccountQuota(ctx, accountID, scope)
}

func (uc *ChannelUsecase) AutoPauseAccount(ctx context.Context, accountID int64, reason string) error {
	if err := uc.repo.AutoPauseAccount(ctx, accountID, reason); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, &SubscriptionAccount{ID: accountID, Status: ChannelStatusDisabled})
	return nil
}

func (uc *ChannelUsecase) CreateSubscriptionAccount(ctx context.Context, account *SubscriptionAccount) error {
	if err := uc.repo.CreateSubscriptionAccount(ctx, account); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, account)
	return nil
}

func (uc *ChannelUsecase) UpdateSubscriptionAccount(ctx context.Context, account *SubscriptionAccount) error {
	if err := uc.repo.UpdateSubscriptionAccount(ctx, account); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, account)
	return nil
}

func (uc *ChannelUsecase) DeleteSubscriptionAccount(ctx context.Context, accountID int64) error {
	if err := uc.repo.DeleteSubscriptionAccount(ctx, accountID); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, &SubscriptionAccount{ID: accountID})
	return nil
}

func (uc *ChannelUsecase) ChangeSubscriptionAccountStatus(ctx context.Context, accountID int64, status int32) error {
	if err := uc.repo.ChangeSubscriptionAccountStatus(ctx, accountID, status); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, &SubscriptionAccount{ID: accountID, Status: status})
	return nil
}

func (uc *ChannelUsecase) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	return uc.repo.ListAvailableModels(ctx, group)
}

func (uc *ChannelUsecase) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*Channel, int64, error) {
	return uc.repo.ListChannels(ctx, page, pageSize, keyword, group, status, chType)
}

func (uc *ChannelUsecase) CreateChannel(ctx context.Context, channel *Channel) error {
	if err := uc.repo.CreateChannel(ctx, channel); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, channel)
	return nil
}

func (uc *ChannelUsecase) UpdateChannel(ctx context.Context, channel *Channel) error {
	if err := uc.repo.UpdateChannel(ctx, channel); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, channel)
	return nil
}

func (uc *ChannelUsecase) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	if quota <= 0 {
		return nil
	}
	if err := uc.repo.RecordUsage(ctx, channelID, quota); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, &Channel{ID: channelID})
	return nil
}

func (uc *ChannelUsecase) RecordHealth(ctx context.Context, event ChannelHealthEvent) error {
	if event.ChannelID <= 0 {
		return ErrChannelNotFound
	}
	if event.CheckedAt.IsZero() {
		event.CheckedAt = uc.now()
	}
	previous, err := uc.repo.FindByID(ctx, event.ChannelID)
	if err != nil {
		return err
	}
	previousSnapshot := *previous
	previousSnapshot.Models = append([]string(nil), previous.Models...)
	channel, err := uc.repo.RecordHealth(ctx, event, uc.healthFailureThreshold, uc.healthCooldown)
	if err != nil {
		return err
	}
	if uc.selector != nil {
		uc.selector.RecordHealth(event.ChannelID, event.Success, event.ResponseTime, event.Error)
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, channel)
	uc.notifyUnavailable(ctx, &previousSnapshot, channel, event)
	return nil
}

func channelsToSelectorCandidates(channels []*Channel) []*commonv1.ChannelInfo {
	candidates := make([]*commonv1.ChannelInfo, 0, len(channels))
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		candidates = append(candidates, &commonv1.ChannelInfo{
			Id:                    ch.ID,
			Type:                  ch.Type,
			Name:                  ch.Name,
			Status:                ch.Status,
			BaseUrl:               ch.BaseURL,
			Group:                 ch.Group,
			Models:                strings.Join(ch.Models, ","),
			Priority:              ch.Priority,
			Key:                   ch.Key,
			Weight:                ch.Weight,
			ResponseTime:          ch.ResponseTime,
			HealthStatus:          ch.HealthStatus,
			HealthLastError:       ch.HealthLastError,
			CircuitOpenedUntil:    ch.CircuitOpenedUntil,
			HealthLastFailureTime: ch.HealthLastFailureTime,
		})
	}
	return candidates
}

func (uc *ChannelUsecase) DeleteChannel(ctx context.Context, channelID int64) error {
	if err := uc.repo.DeleteChannel(ctx, channelID); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, &Channel{ID: channelID})
	return nil
}

func (uc *ChannelUsecase) ChangeChannelStatus(ctx context.Context, channelID int64, status int32) error {
	if err := uc.repo.ChangeStatus(ctx, channelID, status); err != nil {
		return err
	}
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, &Channel{ID: channelID, Status: status})
	return nil
}

func SplitCSV(input string) []string {
	raw := strings.Split(input, ",")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		trimmed := strings.TrimSpace(item)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func DecodeChannelConfig(input string) ChannelConfig {
	if input == "" {
		return ChannelConfig{}
	}
	var cfg ChannelConfig
	_ = sonic.Unmarshal([]byte(input), &cfg)
	return cfg
}

func (c *Channel) ModelsCSV() string {
	return strings.Join(c.Models, ",")
}

func (a *SubscriptionAccount) ModelsCSV() string {
	return strings.Join(a.Models, ",")
}

func (c *Channel) EffectiveHealthStatus() string {
	if strings.TrimSpace(c.HealthStatus) == "" {
		return ChannelHealthHealthy
	}
	return c.HealthStatus
}

func (c *Channel) SelectableAt(now time.Time) bool {
	if c == nil || c.Status != ChannelStatusEnabled {
		return false
	}
	if c.EffectiveHealthStatus() != ChannelHealthUnavailable {
		return true
	}
	return c.CircuitOpenedUntil > 0 && now.Unix() >= c.CircuitOpenedUntil
}

func (a *SubscriptionAccount) IsSchedulableAt(now time.Time) bool {
	if a == nil || a.Status != ChannelStatusEnabled {
		return false
	}
	if a.RateLimitedUntil > 0 && now.Unix() < a.RateLimitedUntil {
		return false
	}
	return !a.LocalQuotaExceededAt(now)
}

func (a *SubscriptionAccount) LocalQuotaExceededAt(now time.Time) bool {
	if a == nil {
		return true
	}
	nowUnix := now.Unix()
	if a.QuotaLimitUSD > 0 && a.QuotaUsedUSD >= a.QuotaLimitUSD {
		return true
	}
	if a.QuotaDailyLimitUSD > 0 && effectiveWindowUsedUSD(a.QuotaDailyUsedUSD, a.QuotaDailyWindowStart, nowUnix, 24*time.Hour) >= a.QuotaDailyLimitUSD {
		return true
	}
	if a.QuotaWeeklyLimitUSD > 0 && effectiveWindowUsedUSD(a.QuotaWeeklyUsedUSD, a.QuotaWeeklyWindowStart, nowUnix, 7*24*time.Hour) >= a.QuotaWeeklyLimitUSD {
		return true
	}
	return false
}

func (a *SubscriptionAccount) EffectiveRateMultiplier() float64 {
	if a == nil || a.RateMultiplier <= 0 {
		return 1
	}
	return a.RateMultiplier
}

func effectiveWindowUsedUSD(used float64, windowStart int64, nowUnix int64, window time.Duration) float64 {
	if windowStart <= 0 || nowUnix-windowStart >= int64(window.Seconds()) {
		return 0
	}
	return used
}

func (uc *ChannelUsecase) notifyUnavailable(ctx context.Context, previous, current *Channel, event ChannelHealthEvent) {
	if uc.notifier == nil || !uc.healthAlert.Enabled || current == nil {
		return
	}
	if previous != nil && previous.EffectiveHealthStatus() == ChannelHealthUnavailable {
		return
	}
	if current.EffectiveHealthStatus() != ChannelHealthUnavailable {
		return
	}
	notifyType := uc.healthAlert.NotifyType
	if notifyType == "" {
		notifyType = defaultHealthAlertNotifyType
	}
	recipients := uc.healthAlert.Recipients
	if len(recipients) == 0 {
		recipients = []string{""}
	}
	subject := fmt.Sprintf("Channel unavailable: %s", current.Name)
	content := channelUnavailableAlertContent(current, event)
	notifyCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), defaultHealthAlertTimeout)
	defer cancel()
	for _, recipient := range recipients {
		_ = uc.notifier.CreateNotification(notifyCtx, notifyType, recipient, subject, content)
	}
}

func channelUnavailableAlertContent(channel *Channel, event ChannelHealthEvent) string {
	var b strings.Builder
	b.WriteString("A channel has become unavailable.\n")
	b.WriteString(fmt.Sprintf("Channel: %s (ID: %d)\n", channel.Name, channel.ID))
	b.WriteString(fmt.Sprintf("Group: %s\n", channel.Group))
	b.WriteString(fmt.Sprintf("Models: %s\n", channel.ModelsCSV()))
	b.WriteString(fmt.Sprintf("Consecutive failures: %d\n", channel.HealthConsecutiveFailures))
	if channel.CircuitOpenedUntil > 0 {
		b.WriteString(fmt.Sprintf("Circuit opened until: %s\n", time.Unix(channel.CircuitOpenedUntil, 0).Format(time.RFC3339)))
	}
	if event.ResponseTime > 0 {
		b.WriteString(fmt.Sprintf("Response time: %dms\n", event.ResponseTime))
	}
	if strings.TrimSpace(event.Error) != "" {
		b.WriteString(fmt.Sprintf("Last error: %s\n", event.Error))
	}
	return b.String()
}

func healthFailureThresholdFromEnv() int32 {
	if v := strings.TrimSpace(os.Getenv("CHANNEL_HEALTH_FAILURE_THRESHOLD")); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err == nil && n > 0 {
			return int32(n)
		}
	}
	return defaultHealthFailureThreshold
}

func healthCooldownFromEnv() time.Duration {
	if v := strings.TrimSpace(os.Getenv("CHANNEL_HEALTH_COOLDOWN")); v != "" {
		duration, err := time.ParseDuration(v)
		if err == nil && duration > 0 {
			return duration
		}
	}
	return defaultHealthCooldown
}
