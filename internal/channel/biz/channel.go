package biz

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sort"
	"strings"
	"time"
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
	ID       int64
	Type     int32
	Name     string
	Status   int32
	BaseURL  string
	Group    string
	Models   []string
	Priority int64
	Key      string
	Config   ChannelConfig
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
}

type ChannelUsecase struct {
	repo ChannelRepo
	rng  *rand.Rand
}

func NewChannelUsecase(repo ChannelRepo) *ChannelUsecase {
	return &ChannelUsecase{
		repo: repo,
		rng:  rand.New(rand.NewSource(time.Now().UnixNano())),
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
	candidates := abilities
	if !excludeFirstPriority {
		highest := abilities[0].Priority
		candidates = abilities[:0]
		for _, ability := range abilities {
			if ability.Priority != highest {
				break
			}
			candidates = append(candidates, ability)
		}
	}
	selected := candidates[uc.rng.Intn(len(candidates))]
	return uc.repo.FindByID(ctx, selected.ChannelID)
}

func (uc *ChannelUsecase) GetChannel(ctx context.Context, channelID int64) (*Channel, error) {
	return uc.repo.FindByID(ctx, channelID)
}

func (uc *ChannelUsecase) ListAvailableModels(ctx context.Context, group string) ([]string, error) {
	return uc.repo.ListAvailableModels(ctx, group)
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
	_ = json.Unmarshal([]byte(input), &cfg)
	return cfg
}

func (c *Channel) ModelsCSV() string {
	return strings.Join(c.Models, ",")
}
