package biz

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"sort"
	"strings"

	"micro-one-api/internal/pkg/events"

	"github.com/bytedance/sonic"
)

const (
	ChannelStatusEnabled = 1
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
	ID                 int64
	Type               int32
	Name               string
	Status             int32
	BaseURL            string
	Group              string
	Models             []string
	Priority           int64
	Key                string
	Weight             uint32
	CreatedTime        int64
	TestTime           int64
	ResponseTime       int64
	Balance            float64
	BalanceUpdatedTime int64
	UsedQuota          int64
	ModelMapping       string
	SystemPrompt       string
	Config             ChannelConfig
}

type Ability struct {
	Group     string
	Model     string
	ChannelID int64
	Enabled   bool
	Priority  int64
}

type ChannelRepo interface {
	FindByID(ctx context.Context, channelID int64) (*Channel, error)
	ListAbilitiesByGroupAndModel(ctx context.Context, group, model string) ([]Ability, error)
	ListAvailableModels(ctx context.Context, group string) ([]string, error)
	ListChannels(ctx context.Context, page, pageSize int32, keyword, group string, status, chType int32) ([]*Channel, int64, error)
	CreateChannel(ctx context.Context, channel *Channel) error
	UpdateChannel(ctx context.Context, channel *Channel) error
	DeleteChannel(ctx context.Context, channelID int64) error
	ChangeStatus(ctx context.Context, channelID int64, status int32) error
}

type ChannelUsecase struct {
	repo     ChannelRepo
	eventBus events.EventBus
}

func NewChannelUsecase(repo ChannelRepo, eventBus events.EventBus) *ChannelUsecase {
	if eventBus == nil {
		eventBus = events.NewMemoryEventBus()
	}
	return &ChannelUsecase{
		repo:     repo,
		eventBus: eventBus,
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

	var candidates []Ability
	if excludeFirstPriority {
		highest := abilities[0].Priority
		for _, ability := range abilities {
			if ability.Priority != highest {
				candidates = append(candidates, ability)
			}
		}
	} else {
		highest := abilities[0].Priority
		for _, ability := range abilities {
			if ability.Priority == highest {
				candidates = append(candidates, ability)
			}
		}
	}

	if len(candidates) == 0 {
		return nil, ErrChannelNotFound
	}

	// Use crypto/rand for secure random selection
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(candidates))))
	if err != nil {
		return nil, err
	}
	selected := candidates[nBig.Int64()]
	return uc.repo.FindByID(ctx, selected.ChannelID)
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
