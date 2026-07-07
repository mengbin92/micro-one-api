package biz

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

type mockChannelRepo struct {
	channels     map[int64]*Channel
	accounts     map[int64]*SubscriptionAccount
	abilities    map[string][]Ability
	accAbilities map[string][]SubscriptionAccountAbility
	resetRuns    map[string]bool
}

type recordedNotification struct {
	notifyType string
	recipient  string
	subject    string
	content    string
}

type recordingNotifier struct {
	notifications []recordedNotification
}

func (n *recordingNotifier) CreateNotification(ctx context.Context, notifyType, recipient, subject, content string) error {
	n.notifications = append(n.notifications, recordedNotification{
		notifyType: notifyType,
		recipient:  recipient,
		subject:    subject,
		content:    content,
	})
	return nil
}

func (m *mockChannelRepo) FindByID(ctx context.Context, channelID int64) (*Channel, error) {
	channel, ok := m.channels[channelID]
	if !ok {
		return nil, ErrChannelNotFound
	}
	return channel, nil
}

func (m *mockChannelRepo) ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]Ability, error) {
	key := group + ":" + model
	abilities, ok := m.abilities[key]
	if !ok {
		return []Ability{}, nil
	}
	enabled := make([]Ability, 0, len(abilities))
	for _, ability := range abilities {
		if ability.Enabled {
			enabled = append(enabled, ability)
		}
	}
	return enabled, nil
}

func (m *mockChannelRepo) FindSubscriptionAccountByID(ctx context.Context, accountID int64) (*SubscriptionAccount, error) {
	if m.accounts == nil {
		return nil, ErrSubscriptionAccountNotFound
	}
	account, ok := m.accounts[accountID]
	if !ok {
		return nil, ErrSubscriptionAccountNotFound
	}
	return account, nil
}

func (m *mockChannelRepo) ListSubscriptionAccountAbilities(ctx context.Context, group, model, platform string) ([]SubscriptionAccountAbility, error) {
	if len(m.accAbilities) == 0 {
		return nil, nil
	}
	key := platform + ":" + group + ":" + model
	abilities, ok := m.accAbilities[key]
	if !ok {
		return nil, nil
	}
	enabled := make([]SubscriptionAccountAbility, 0, len(abilities))
	for _, ability := range abilities {
		if ability.Enabled {
			enabled = append(enabled, ability)
		}
	}
	return enabled, nil
}

func (m *mockChannelRepo) ListSubscriptionAccounts(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform string) ([]*SubscriptionAccount, int64, error) {
	var result []*SubscriptionAccount
	for _, acc := range m.accounts {
		result = append(result, acc)
	}
	return result, int64(len(result)), nil
}

func (m *mockChannelRepo) ListOAuthRefreshCandidates(ctx context.Context, within time.Duration) ([]int64, error) {
	threshold := time.Now().Add(within).Unix()
	var ids []int64
	for id, acc := range m.accounts {
		if acc.ExpiresAt > 0 && acc.ExpiresAt <= threshold {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (m *mockChannelRepo) CreateSubscriptionAccount(ctx context.Context, account *SubscriptionAccount) error {
	if m.accounts == nil {
		m.accounts = make(map[int64]*SubscriptionAccount)
	}
	account.ID = int64(len(m.accounts) + 1)
	m.accounts[account.ID] = account
	return nil
}

func (m *mockChannelRepo) UpdateSubscriptionAccount(ctx context.Context, account *SubscriptionAccount) error {
	if m.accounts == nil {
		m.accounts = make(map[int64]*SubscriptionAccount)
	}
	m.accounts[account.ID] = account
	return nil
}

func (m *mockChannelRepo) DeleteSubscriptionAccount(ctx context.Context, accountID int64) error {
	delete(m.accounts, accountID)
	return nil
}

func (m *mockChannelRepo) ChangeSubscriptionAccountStatus(ctx context.Context, accountID int64, status int32) error {
	if acc, ok := m.accounts[accountID]; ok {
		acc.Status = status
	}
	return nil
}

func (m *mockChannelRepo) SetSubscriptionAccountError(ctx context.Context, accountID int64, message string) error {
	if acc, ok := m.accounts[accountID]; ok {
		acc.LastError = message
	}
	return nil
}

func (m *mockChannelRepo) SetTempUnschedulable(ctx context.Context, accountID int64, until time.Time, reason string) error {
	if acc, ok := m.accounts[accountID]; ok {
		acc.RateLimitedUntil = until.Unix()
		acc.LastError = reason
	}
	return nil
}

func (m *mockChannelRepo) ClearTempUnschedulable(ctx context.Context, accountID int64) error {
	if acc, ok := m.accounts[accountID]; ok {
		acc.RateLimitedUntil = 0
	}
	return nil
}

func (m *mockChannelRepo) RecordAccountQuotaSnapshot(ctx context.Context, snapshot *AccountQuotaSnapshot) error {
	if snapshot != nil && m.accounts != nil {
		if acc, ok := m.accounts[snapshot.AccountID]; ok && snapshot.PrimaryUsedPercent != nil {
			acc.QuotaUsedPercent = float32(*snapshot.PrimaryUsedPercent)
		}
	}
	return nil
}

func (m *mockChannelRepo) GetAccountQuotaSnapshot(ctx context.Context, accountID int64) (*AccountQuotaSnapshot, error) {
	if m.accounts == nil {
		return nil, ErrSubscriptionAccountNotFound
	}
	acc, ok := m.accounts[accountID]
	if !ok {
		return nil, ErrSubscriptionAccountNotFound
	}
	used := float64(acc.QuotaUsedPercent)
	return &AccountQuotaSnapshot{AccountID: accountID, PrimaryUsedPercent: &used}, nil
}

func (m *mockChannelRepo) RecordSubscriptionAccountQuotaUsage(ctx context.Context, usage SubscriptionAccountQuotaUsage) error {
	if m.accounts == nil {
		return ErrSubscriptionAccountNotFound
	}
	acc, ok := m.accounts[usage.AccountID]
	if !ok {
		return ErrSubscriptionAccountNotFound
	}
	acc.QuotaUsedUSD += usage.CostUSD * acc.EffectiveRateMultiplier()
	acc.LastUsedAt = usage.OccurredAt.Unix()
	return nil
}

func (m *mockChannelRepo) AggregateSubscriptionAccountQuotaEvents(ctx context.Context, filter SubscriptionAccountQuotaEventFilter) ([]*SubscriptionAccountQuotaEventAggregate, error) {
	return []*SubscriptionAccountQuotaEventAggregate{}, nil
}

func (m *mockChannelRepo) ResetSubscriptionAccountQuota(ctx context.Context, accountID int64, scope string) error {
	if m.accounts == nil {
		return ErrSubscriptionAccountNotFound
	}
	acc, ok := m.accounts[accountID]
	if !ok {
		return ErrSubscriptionAccountNotFound
	}
	switch scope {
	case "total":
		acc.QuotaUsedUSD = 0
	case "5h":
		acc.Quota5hUsedUSD = 0
		acc.Quota5hWindowStart = 0
	case "daily":
		acc.QuotaDailyUsedUSD = 0
		acc.QuotaDailyWindowStart = 0
	case "weekly":
		acc.QuotaWeeklyUsedUSD = 0
		acc.QuotaWeeklyWindowStart = 0
	case "all":
		acc.QuotaUsedUSD = 0
		acc.Quota5hUsedUSD = 0
		acc.Quota5hWindowStart = 0
		acc.QuotaDailyUsedUSD = 0
		acc.QuotaDailyWindowStart = 0
		acc.QuotaWeeklyUsedUSD = 0
		acc.QuotaWeeklyWindowStart = 0
	}
	return nil
}

func (m *mockChannelRepo) AutoPauseAccount(ctx context.Context, accountID int64, reason string) error {
	if acc, ok := m.accounts[accountID]; ok {
		acc.Status = ChannelStatusDisabled
		acc.LastError = reason
	}
	return nil
}

func (m *mockChannelRepo) ClearRecoveryMetadata(ctx context.Context, accountID int64) error {
	if acc, ok := m.accounts[accountID]; ok {
		acc.Metadata = ""
		acc.LastError = ""
	}
	return nil
}

func (m *mockChannelRepo) RecordQuotaResetRun(ctx context.Context, run *SubscriptionAccountQuotaResetRun) error {
	if run == nil || run.AccountID <= 0 {
		return ErrSubscriptionAccountNotFound
	}
	if m.resetRuns == nil {
		m.resetRuns = make(map[string]bool)
	}
	key := resetRunKey(run.AccountID, run.Scope, run.WindowStart)
	if m.resetRuns[key] {
		return ErrQuotaResetRunDuplicate
	}
	m.resetRuns[key] = true
	return nil
}

func (m *mockChannelRepo) StampQuotaAlertMetadata(ctx context.Context, accountID int64, kind string, alertAt int64) error {
	return nil
}

func resetRunKey(accountID int64, scope string, windowStart int64) string {
	return strconv.FormatInt(accountID, 10) + "\x00" + scope + "\x00" + strconv.FormatInt(windowStart, 10)
}

func (m *mockChannelRepo) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	uniqueModels := make(map[string]bool)
	for key, abilities := range m.abilities {
		if len(key) > len(group)+1 && key[:len(group)+1] == group+":" {
			for _, ability := range abilities {
				if ability.Enabled {
					uniqueModels[ability.Model] = true
				}
			}
		}
	}
	models := make([]string, 0, len(uniqueModels))
	for model := range uniqueModels {
		models = append(models, model)
	}
	return models, nil
}

func (m *mockChannelRepo) ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*Channel, int64, error) {
	var result []*Channel
	for _, ch := range m.channels {
		result = append(result, ch)
	}
	return result, int64(len(result)), nil
}

func (m *mockChannelRepo) CreateChannel(ctx context.Context, channel *Channel) error {
	channel.ID = int64(len(m.channels) + 1)
	m.channels[channel.ID] = channel
	return nil
}

func (m *mockChannelRepo) UpdateChannel(ctx context.Context, channel *Channel) error {
	m.channels[channel.ID] = channel
	return nil
}

func (m *mockChannelRepo) RecordUsage(ctx context.Context, channelID int64, quota int64) error {
	if ch, ok := m.channels[channelID]; ok {
		ch.UsedQuota += quota
	}
	return nil
}

func (m *mockChannelRepo) RecordHealth(ctx context.Context, event ChannelHealthEvent, threshold int32, cooldown time.Duration) (*Channel, error) {
	ch, ok := m.channels[event.ChannelID]
	if !ok {
		return nil, ErrChannelNotFound
	}
	if event.Success {
		ch.HealthStatus = ChannelHealthHealthy
		ch.HealthLastError = ""
		ch.HealthConsecutiveFailures = 0
		ch.CircuitOpenedUntil = 0
	} else {
		ch.HealthLastError = event.Error
		ch.HealthConsecutiveFailures++
		if ch.HealthConsecutiveFailures >= threshold {
			ch.HealthStatus = ChannelHealthUnavailable
			ch.CircuitOpenedUntil = event.CheckedAt.Add(cooldown).Unix()
		} else {
			ch.HealthStatus = ChannelHealthDegraded
		}
	}
	return ch, nil
}

func (m *mockChannelRepo) DeleteChannel(ctx context.Context, channelID int64) error {
	delete(m.channels, channelID)
	return nil
}

func (m *mockChannelRepo) ChangeStatus(ctx context.Context, channelID int64, status int32) error {
	if ch, ok := m.channels[channelID]; ok {
		ch.Status = status
	}
	return nil
}

func TestChannelUsecase_SelectChannel_SingleChannel(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:       1,
				Type:     1,
				Name:     "test-channel",
				Status:   ChannelStatusEnabled,
				BaseURL:  "https://api.openai.com/v1",
				Group:    "default",
				Models:   []string{"gpt-4o-mini"},
				Priority: 10,
			},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{
					Group:     "default",
					Model:     "gpt-4o-mini",
					ChannelID: 1,
					Enabled:   true,
					Priority:  10,
				},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("unexpected channel ID: %d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_NoAvailableChannels(t *testing.T) {
	repo := &mockChannelRepo{
		channels:  map[int64]*Channel{},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	_, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != ErrChannelNotFound {
		t.Fatalf("expected ErrChannelNotFound, got: %v", err)
	}
}

func TestChannelUsecase_SelectSubscriptionAccount_SkipsTempUnschedulable(t *testing.T) {
	now := time.Unix(1710000000, 0)
	repo := &mockChannelRepo{
		accounts: map[int64]*SubscriptionAccount{
			1: {
				ID:               1,
				Name:             "cooling",
				Status:           ChannelStatusEnabled,
				Platform:         "codex",
				RateLimitedUntil: now.Add(time.Minute).Unix(),
				Priority:         10,
			},
			2: {
				ID:       2,
				Name:     "ready",
				Status:   ChannelStatusEnabled,
				Platform: "codex",
				Priority: 10,
			},
		},
		accAbilities: map[string][]SubscriptionAccountAbility{
			"codex:default:gpt-5": {
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 2, Enabled: true, Priority: 10},
			},
		},
	}
	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }

	account, err := uc.SelectSubscriptionAccount(context.Background(), "default", "gpt-5", "codex", false)
	if err != nil {
		t.Fatalf("SelectSubscriptionAccount() error = %v", err)
	}
	if account.ID != 2 {
		t.Fatalf("selected account = %d, want 2", account.ID)
	}
}

func TestChannelUsecase_SelectSubscriptionAccount_SkipsLocalQuotaExceeded(t *testing.T) {
	now := time.Unix(1710000000, 0)
	repo := &mockChannelRepo{
		accounts: map[int64]*SubscriptionAccount{
			1: {
				ID:                    1,
				Name:                  "spent",
				Status:                ChannelStatusEnabled,
				Platform:              "codex",
				Priority:              10,
				QuotaDailyLimitUSD:    1,
				QuotaDailyUsedUSD:     1,
				QuotaDailyWindowStart: now.Add(-time.Hour).Unix(),
			},
			2: {
				ID:       2,
				Name:     "ready",
				Status:   ChannelStatusEnabled,
				Platform: "codex",
				Priority: 10,
			},
		},
		accAbilities: map[string][]SubscriptionAccountAbility{
			"codex:default:gpt-5": {
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 2, Enabled: true, Priority: 10},
			},
		},
	}
	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }

	account, err := uc.SelectSubscriptionAccount(context.Background(), "default", "gpt-5", "codex", false)
	if err != nil {
		t.Fatalf("SelectSubscriptionAccount() error = %v", err)
	}
	if account.ID != 2 {
		t.Fatalf("selected account = %d, want 2", account.ID)
	}
}

func TestSubscriptionAccount_LocalQuotaDailyWindowExpires(t *testing.T) {
	now := time.Unix(1710000000, 0)
	account := &SubscriptionAccount{
		Status:                ChannelStatusEnabled,
		QuotaDailyLimitUSD:    1,
		QuotaDailyUsedUSD:     1,
		QuotaDailyWindowStart: now.Add(-25 * time.Hour).Unix(),
	}
	if !account.IsSchedulableAt(now) {
		t.Fatal("account with expired daily window should be schedulable")
	}
}

func TestSubscriptionAccount_LocalQuotaFixedDailyWindowExpiresAtTimezoneMidnight(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 2, 1, 0, 0, 0, loc)
	account := &SubscriptionAccount{
		Status:                ChannelStatusEnabled,
		QuotaDailyLimitUSD:    1,
		QuotaDailyUsedUSD:     1,
		QuotaDailyWindowStart: time.Date(2026, 7, 1, 23, 0, 0, 0, loc).Unix(),
		QuotaResetStrategy:    QuotaResetStrategyFixed,
		QuotaTimezone:         "Asia/Shanghai",
	}
	if !account.IsSchedulableAt(now) {
		t.Fatal("account with previous fixed daily window should be schedulable")
	}
	account.QuotaDailyWindowStart = time.Date(2026, 7, 2, 0, 0, 0, 0, loc).Unix()
	if account.IsSchedulableAt(now) {
		t.Fatal("account with exhausted current fixed daily window should not be schedulable")
	}
}

func TestSubscriptionAccount_LocalQuotaFixedWeeklyWindowStartsMonday(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, loc)
	account := &SubscriptionAccount{
		Status:                 ChannelStatusEnabled,
		QuotaWeeklyLimitUSD:    1,
		QuotaWeeklyUsedUSD:     1,
		QuotaWeeklyWindowStart: time.Date(2026, 7, 5, 23, 0, 0, 0, loc).Unix(),
		QuotaResetStrategy:     QuotaResetStrategyFixed,
		QuotaTimezone:          "Asia/Shanghai",
	}
	if !account.IsSchedulableAt(now) {
		t.Fatal("account with previous fixed weekly window should be schedulable")
	}
	account.QuotaWeeklyWindowStart = time.Date(2026, 7, 6, 0, 0, 0, 0, loc).Unix()
	if account.IsSchedulableAt(now) {
		t.Fatal("account with exhausted current fixed weekly window should not be schedulable")
	}
}

func TestSubscriptionAccount_InvalidQuotaTimezoneFallsBackToUTC(t *testing.T) {
	account := &SubscriptionAccount{
		QuotaResetStrategy: QuotaResetStrategyFixed,
		QuotaTimezone:      "not/a-zone",
	}
	if got := account.EffectiveQuotaTimezone(); got != DefaultQuotaTimezone {
		t.Fatalf("EffectiveQuotaTimezone() = %q, want %q", got, DefaultQuotaTimezone)
	}
	now := time.Date(2026, 7, 2, 1, 30, 0, 0, time.UTC)
	if got, want := account.FixedQuotaWindowStart(now, "daily"), time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC).Unix(); got != want {
		t.Fatalf("FixedQuotaWindowStart() = %d, want %d", got, want)
	}
}

func TestChannelUsecase_SelectSubscriptionAccount_SkipsLocalQuota5hExceeded(t *testing.T) {
	now := time.Unix(1710000000, 0)
	repo := &mockChannelRepo{
		accounts: map[int64]*SubscriptionAccount{
			1: {
				ID:                 1,
				Name:               "spent-5h",
				Status:             ChannelStatusEnabled,
				Platform:           "codex",
				Priority:           10,
				Quota5hLimitUSD:    1,
				Quota5hUsedUSD:     1,
				Quota5hWindowStart: now.Add(-time.Hour).Unix(),
			},
			2: {
				ID:       2,
				Name:     "ready",
				Status:   ChannelStatusEnabled,
				Platform: "codex",
				Priority: 10,
			},
		},
		accAbilities: map[string][]SubscriptionAccountAbility{
			"codex:default:gpt-5": {
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-5", Platform: "codex", AccountID: 2, Enabled: true, Priority: 10},
			},
		},
	}
	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }

	account, err := uc.SelectSubscriptionAccount(context.Background(), "default", "gpt-5", "codex", false)
	if err != nil {
		t.Fatalf("SelectSubscriptionAccount() error = %v", err)
	}
	if account.ID != 2 {
		t.Fatalf("selected account = %d, want 2", account.ID)
	}
}

func TestSubscriptionAccount_LocalQuota5hWindowExpires(t *testing.T) {
	now := time.Unix(1710000000, 0)
	account := &SubscriptionAccount{
		Status:             ChannelStatusEnabled,
		Quota5hLimitUSD:    1,
		Quota5hUsedUSD:     1,
		Quota5hWindowStart: now.Add(-6 * time.Hour).Unix(),
	}
	if !account.IsSchedulableAt(now) {
		t.Fatal("account with expired 5h window should be schedulable")
	}
}

func TestChannelUsecase_SelectChannel_PriorityOrdering(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 20},
			3: {ID: 3, Name: "channel-3", Status: ChannelStatusEnabled, Priority: 5},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 20},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 3, Enabled: true, Priority: 5},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 2 {
		t.Fatalf("expected highest priority channel (ID=2), got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_SkipsOpenCircuit(t *testing.T) {
	now := time.Unix(1000, 0)
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "open-circuit", Status: ChannelStatusEnabled, Priority: 20, HealthStatus: ChannelHealthUnavailable, CircuitOpenedUntil: now.Add(time.Minute).Unix()},
			2: {ID: 2, Name: "healthy", Status: ChannelStatusEnabled, Priority: 10, HealthStatus: ChannelHealthHealthy},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 20},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 2 {
		t.Fatalf("expected healthy fallback channel, got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_AllowsHalfOpenAfterCooldown(t *testing.T) {
	now := time.Unix(1000, 0)
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "cooldown-ended", Status: ChannelStatusEnabled, Priority: 20, HealthStatus: ChannelHealthUnavailable, CircuitOpenedUntil: now.Add(-time.Second).Unix()},
			2: {ID: 2, Name: "healthy", Status: ChannelStatusEnabled, Priority: 10, HealthStatus: ChannelHealthHealthy},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 20},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	uc.now = func() time.Time { return now }
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("expected half-open channel, got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_SamePriorityRandom(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 10},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	results := make(map[int64]int)

	for i := 0; i < 50; i++ {
		channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
		if err != nil {
			t.Fatalf("SelectChannel() error = %v", err)
		}
		results[channel.ID]++
	}

	if len(results) != 2 {
		t.Fatalf("expected both channels to be selected, got: %v", results)
	}
	if results[1] == 0 || results[2] == 0 {
		t.Fatalf("expected both channels to be selected multiple times, got: %v", results)
	}
}

func TestChannelUsecase_SelectChannel_ExcludeFirstPriority(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 10},
			3: {ID: 3, Name: "channel-3", Status: ChannelStatusEnabled, Priority: 5},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 3, Enabled: true, Priority: 5},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)

	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", true)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID == 1 || channel.ID == 2 {
		t.Fatalf("expected lower priority channel, got ID=%d", channel.ID)
	}
	if channel.ID != 3 {
		t.Fatalf("expected channel-3 (ID=3), got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_FilterDisabled(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled, Priority: 20},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 2, Enabled: false, Priority: 20},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != nil {
		t.Fatalf("SelectChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("expected enabled channel (ID=1), got ID=%d", channel.ID)
	}
}

func TestChannelUsecase_SelectChannel_AllDisabled(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled, Priority: 10},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: false, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	_, err := uc.SelectChannel(context.Background(), "default", "gpt-4o-mini", false)
	if err != ErrChannelNotFound {
		t.Fatalf("expected ErrChannelNotFound, got: %v", err)
	}
}

func TestChannelUsecase_RecordHealth_NotifiesWhenChannelBecomesUnavailable(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:                        1,
				Name:                      "primary-openai",
				Status:                    ChannelStatusEnabled,
				Group:                     "default",
				Models:                    []string{"gpt-4o"},
				HealthStatus:              ChannelHealthDegraded,
				HealthConsecutiveFailures: 1,
			},
		},
		abilities: map[string][]Ability{},
	}
	notifier := &recordingNotifier{}
	uc := NewChannelUsecase(repo, nil)
	uc.healthFailureThreshold = 2
	uc.healthCooldown = time.Minute
	uc.ConfigureHealthAlert(notifier, HealthAlertConfig{
		Enabled:    true,
		NotifyType: "webhook",
		Recipients: []string{"https://hooks.example.com/ops"},
	})

	err := uc.RecordHealth(context.Background(), ChannelHealthEvent{
		ChannelID:    1,
		Success:      false,
		Error:        "status=502",
		ResponseTime: 321,
		CheckedAt:    time.Unix(1000, 0),
	})
	if err != nil {
		t.Fatalf("RecordHealth() error = %v", err)
	}
	if len(notifier.notifications) != 1 {
		t.Fatalf("expected one notification, got %d", len(notifier.notifications))
	}
	got := notifier.notifications[0]
	if got.notifyType != "webhook" || got.recipient != "https://hooks.example.com/ops" {
		t.Fatalf("unexpected notification target: %+v", got)
	}
	if got.subject != "Channel unavailable: primary-openai" {
		t.Fatalf("unexpected subject: %q", got.subject)
	}
	if !strings.Contains(got.content, "Channel: primary-openai (ID: 1)") || !strings.Contains(got.content, "Last error: status=502") {
		t.Fatalf("unexpected content: %q", got.content)
	}
}

func TestChannelUsecase_RecordHealth_DoesNotRepeatUnavailableNotification(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:                        1,
				Name:                      "primary-openai",
				Status:                    ChannelStatusEnabled,
				Group:                     "default",
				Models:                    []string{"gpt-4o"},
				HealthStatus:              ChannelHealthUnavailable,
				HealthConsecutiveFailures: 3,
				CircuitOpenedUntil:        time.Unix(1000, 0).Add(time.Minute).Unix(),
			},
		},
		abilities: map[string][]Ability{},
	}
	notifier := &recordingNotifier{}
	uc := NewChannelUsecase(repo, nil)
	uc.healthFailureThreshold = 2
	uc.healthCooldown = time.Minute
	uc.ConfigureHealthAlert(notifier, HealthAlertConfig{Enabled: true})

	err := uc.RecordHealth(context.Background(), ChannelHealthEvent{
		ChannelID: 1,
		Success:   false,
		Error:     "status=503",
		CheckedAt: time.Unix(1001, 0),
	})
	if err != nil {
		t.Fatalf("RecordHealth() error = %v", err)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("expected no repeated notifications, got %d", len(notifier.notifications))
	}
}

func TestChannelUsecase_RecordHealth_DoesNotNotifyWhenAlertDisabled(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:                        1,
				Name:                      "primary-openai",
				Status:                    ChannelStatusEnabled,
				Group:                     "default",
				Models:                    []string{"gpt-4o"},
				HealthStatus:              ChannelHealthDegraded,
				HealthConsecutiveFailures: 1,
			},
		},
		abilities: map[string][]Ability{},
	}
	notifier := &recordingNotifier{}
	uc := NewChannelUsecase(repo, nil)
	uc.healthFailureThreshold = 2
	uc.healthCooldown = time.Minute
	uc.ConfigureHealthAlert(notifier, HealthAlertConfig{Enabled: false})

	err := uc.RecordHealth(context.Background(), ChannelHealthEvent{
		ChannelID: 1,
		Success:   false,
		Error:     "status=502",
		CheckedAt: time.Unix(1000, 0),
	})
	if err != nil {
		t.Fatalf("RecordHealth() error = %v", err)
	}
	if len(notifier.notifications) != 0 {
		t.Fatalf("expected no notifications when alerts disabled, got %d", len(notifier.notifications))
	}
}

func TestChannelUsecase_RecordHealth_DoesNotNotifyWhenNoNotifier(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:                        1,
				Name:                      "primary-openai",
				Status:                    ChannelStatusEnabled,
				Group:                     "default",
				Models:                    []string{"gpt-4o"},
				HealthStatus:              ChannelHealthDegraded,
				HealthConsecutiveFailures: 1,
			},
		},
		abilities: map[string][]Ability{},
	}
	uc := NewChannelUsecase(repo, nil)
	uc.healthFailureThreshold = 2
	uc.healthCooldown = time.Minute
	uc.ConfigureHealthAlert(nil, HealthAlertConfig{Enabled: true})

	err := uc.RecordHealth(context.Background(), ChannelHealthEvent{
		ChannelID: 1,
		Success:   false,
		Error:     "status=502",
		CheckedAt: time.Unix(1000, 0),
	})
	if err != nil {
		t.Fatalf("RecordHealth() error = %v", err)
	}
}

func TestChannelUsecase_RecordHealth_NotifiesMultipleRecipients(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:                        1,
				Name:                      "primary-openai",
				Status:                    ChannelStatusEnabled,
				Group:                     "default",
				Models:                    []string{"gpt-4o"},
				HealthStatus:              ChannelHealthDegraded,
				HealthConsecutiveFailures: 1,
			},
		},
		abilities: map[string][]Ability{},
	}
	notifier := &recordingNotifier{}
	uc := NewChannelUsecase(repo, nil)
	uc.healthFailureThreshold = 2
	uc.healthCooldown = time.Minute
	uc.ConfigureHealthAlert(notifier, HealthAlertConfig{
		Enabled:    true,
		NotifyType: "webhook",
		Recipients: []string{
			"https://hooks.example.com/ops",
			"https://hooks.example.com/oncall",
			"admin@example.com",
		},
	})

	err := uc.RecordHealth(context.Background(), ChannelHealthEvent{
		ChannelID: 1,
		Success:   false,
		Error:     "status=502",
		CheckedAt: time.Unix(1000, 0),
	})
	if err != nil {
		t.Fatalf("RecordHealth() error = %v", err)
	}
	if len(notifier.notifications) != 3 {
		t.Fatalf("expected three notifications (one per recipient), got %d", len(notifier.notifications))
	}

	expectedRecipients := map[string]bool{
		"https://hooks.example.com/ops":    false,
		"https://hooks.example.com/oncall": false,
		"admin@example.com":                false,
	}
	for _, notif := range notifier.notifications {
		if _, ok := expectedRecipients[notif.recipient]; !ok {
			t.Fatalf("unexpected recipient: %s", notif.recipient)
		}
		expectedRecipients[notif.recipient] = true
		if notif.subject != "Channel unavailable: primary-openai" {
			t.Fatalf("unexpected subject: %q", notif.subject)
		}
		if notif.notifyType != "webhook" {
			t.Fatalf("unexpected notifyType: %q", notif.notifyType)
		}
	}
	for recipient, found := range expectedRecipients {
		if !found {
			t.Fatalf("missing notification for recipient: %s", recipient)
		}
	}
}

func TestChannelUsecase_RecordHealth_NotifyOnceWhenTransitioningFromHealthy(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:                        1,
				Name:                      "primary-openai",
				Status:                    ChannelStatusEnabled,
				Group:                     "default",
				Models:                    []string{"gpt-4o"},
				HealthStatus:              ChannelHealthHealthy,
				HealthConsecutiveFailures: 0,
			},
		},
		abilities: map[string][]Ability{},
	}
	notifier := &recordingNotifier{}
	uc := NewChannelUsecase(repo, nil)
	uc.healthFailureThreshold = 3
	uc.healthCooldown = time.Minute
	uc.ConfigureHealthAlert(notifier, HealthAlertConfig{
		Enabled:    true,
		NotifyType: "webhook",
		Recipients: []string{"https://hooks.example.com/ops"},
	})

	for i := int32(0); i < 4; i++ {
		err := uc.RecordHealth(context.Background(), ChannelHealthEvent{
			ChannelID: 1,
			Success:   false,
			Error:     fmt.Sprintf("failure #%d", i+1),
			CheckedAt: time.Unix(1000+int64(i), 0),
		})
		if err != nil {
			t.Fatalf("RecordHealth() iteration %d error = %v", i+1, err)
		}
	}

	if len(notifier.notifications) != 1 {
		t.Fatalf("expected exactly one notification on transition to unavailable, got %d", len(notifier.notifications))
	}
}

func TestChannelUsecase_GetChannel(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {
				ID:       1,
				Type:     1,
				Name:     "test-channel",
				Status:   ChannelStatusEnabled,
				BaseURL:  "https://api.openai.com/v1",
				Group:    "default",
				Models:   []string{"gpt-4o-mini"},
				Priority: 10,
				Key:      "sk-test",
			},
		},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	channel, err := uc.GetChannel(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetChannel() error = %v", err)
	}
	if channel.ID != 1 {
		t.Fatalf("unexpected channel ID: %d", channel.ID)
	}
	if channel.Name != "test-channel" {
		t.Fatalf("unexpected channel name: %s", channel.Name)
	}
	if channel.Key != "sk-test" {
		t.Fatalf("unexpected channel key: %s", channel.Key)
	}
}

func TestChannelUsecase_GetChannel_NotFound(t *testing.T) {
	repo := &mockChannelRepo{
		channels:  map[int64]*Channel{},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	_, err := uc.GetChannel(context.Background(), 999)
	if err != ErrChannelNotFound {
		t.Fatalf("expected ErrChannelNotFound, got: %v", err)
	}
}

func TestChannelUsecase_ListAvailableModels(t *testing.T) {
	repo := &mockChannelRepo{
		channels: map[int64]*Channel{
			1: {ID: 1, Name: "channel-1", Status: ChannelStatusEnabled},
			2: {ID: 2, Name: "channel-2", Status: ChannelStatusEnabled},
		},
		abilities: map[string][]Ability{
			"default:gpt-4o-mini": {
				{Group: "default", Model: "gpt-4o-mini", ChannelID: 1, Enabled: true, Priority: 10},
			},
			"default:gpt-4o": {
				{Group: "default", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
				{Group: "default", Model: "gpt-4o", ChannelID: 2, Enabled: true, Priority: 10},
			},
			"premium:gpt-4o": {
				{Group: "premium", Model: "gpt-4o", ChannelID: 1, Enabled: true, Priority: 10},
			},
		},
	}

	uc := NewChannelUsecase(repo, nil)
	models, err := uc.ListAvailableModels(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListAvailableModels() error = %v", err)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got: %d", len(models))
	}

	modelSet := make(map[string]bool)
	for _, model := range models {
		modelSet[model] = true
	}
	if !modelSet["gpt-4o-mini"] || !modelSet["gpt-4o"] {
		t.Fatalf("expected gpt-4o-mini and gpt-4o, got: %v", models)
	}
}

func TestChannelUsecase_ListAvailableModels_NoChannels(t *testing.T) {
	repo := &mockChannelRepo{
		channels:  map[int64]*Channel{},
		abilities: map[string][]Ability{},
	}

	uc := NewChannelUsecase(repo, nil)
	models, err := uc.ListAvailableModels(context.Background(), "default")
	if err != nil {
		t.Fatalf("ListAvailableModels() error = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("expected no models, got: %v", models)
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty string", "", []string{}},
		{"single item", "gpt-4o-mini", []string{"gpt-4o-mini"}},
		{"multiple items", "gpt-4o-mini,gpt-4o", []string{"gpt-4o-mini", "gpt-4o"}},
		{"items with spaces", "gpt-4o-mini, gpt-4o, gpt-3.5-turbo", []string{"gpt-4o-mini", "gpt-4o", "gpt-3.5-turbo"}},
		{"items with extra spaces", "  gpt-4o-mini  ,  gpt-4o  ", []string{"gpt-4o-mini", "gpt-4o"}},
		{"empty items", "gpt-4o-mini,,gpt-4o", []string{"gpt-4o-mini", "gpt-4o"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitCSV(tt.input)
			if !equalStringSlices(result, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestDecodeChannelConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ChannelConfig
	}{
		{"empty string", "", ChannelConfig{}},
		{"valid json", `{"APIVersion":"v1","Region":"us-east-1"}`, ChannelConfig{APIVersion: "v1", Region: "us-east-1"}},
		{"invalid json", `{invalid json}`, ChannelConfig{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := DecodeChannelConfig(tt.input)
			if result != tt.expected {
				t.Fatalf("expected %+v, got %+v", tt.expected, result)
			}
		})
	}
}

func TestChannel_ModelsCSV(t *testing.T) {
	tests := []struct {
		name     string
		models   []string
		expected string
	}{
		{"empty", []string{}, ""},
		{"single model", []string{"gpt-4o-mini"}, "gpt-4o-mini"},
		{"multiple models", []string{"gpt-4o-mini", "gpt-4o"}, "gpt-4o-mini,gpt-4o"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			channel := &Channel{Models: tt.models}
			result := channel.ModelsCSV()
			if result != tt.expected {
				t.Fatalf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
