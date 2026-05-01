package biz

import (
	"context"
)

type IdentityClient interface {
	GetAuthSnapshot(ctx context.Context, token string) (*AuthSnapshot, error)
}

type ChannelClient interface {
	SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*Channel, error)
}

type RelayRequest struct {
	Token string
	Model string
}

type AuthSnapshot struct {
	UserID        int64
	TokenID       int64
	Group         string
	AllowedModels []string
	UserEnabled   bool
	TokenEnabled  bool
}

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
}

type RelayPlan struct {
	Auth    *AuthSnapshot
	Channel *Channel
}

// RelayUsecase is the phase-one relay orchestration boundary.
type RelayUsecase struct {
	identity IdentityClient
	channel  ChannelClient
}

func NewRelayUsecase(identity IdentityClient, channel ChannelClient) *RelayUsecase {
	return &RelayUsecase{
		identity: identity,
		channel:  channel,
	}
}

func (uc *RelayUsecase) Plan(ctx context.Context, req RelayRequest) (*RelayPlan, error) {
	authSnapshot, err := uc.identity.GetAuthSnapshot(ctx, req.Token)
	if err != nil {
		return nil, err
	}
	channel, err := uc.channel.SelectChannel(ctx, authSnapshot.Group, req.Model, false)
	if err != nil {
		return nil, err
	}
	return &RelayPlan{
		Auth:    authSnapshot,
		Channel: channel,
	}, nil
}
