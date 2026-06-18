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

	"github.com/bytedance/sonic"
)

const (
	ChannelStatusEnabled = 1

	ChannelHealthHealthy     = "healthy"
	ChannelHealthDegraded    = "degraded"
	ChannelHealthUnavailable = "unavailable"

	defaultHealthFailureThreshold = 3
	defaultHealthCooldown         = 5 * time.Minute
	defaultHealthAlertNotifyType  = "event"
	defaultHealthAlertTimeout     = 5 * time.Second
)

var ErrChannelNotFound = errors.New("channel not found")

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

type Ability struct {
	Group     string
	Model     string
	ChannelID int64
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
		nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(tier))))
		if err != nil {
			return nil, err
		}
		return tier[nBig.Int64()], nil
	}

	return nil, ErrChannelNotFound
}

func (uc *ChannelUsecase) GetChannel(ctx context.Context, channelID int64) (*Channel, error) {
	return uc.repo.FindByID(ctx, channelID)
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
	_ = uc.eventBus.Publish(ctx, events.TopicChannelChanged, channel)
	uc.notifyUnavailable(ctx, &previousSnapshot, channel, event)
	return nil
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
