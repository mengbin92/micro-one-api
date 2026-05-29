package biz

import (
	"context"
	"fmt"
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
	TokenName     string
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
	Config   ChannelConfig
}

type ChannelConfig struct {
	APIVersion string
}

// RelayPlan is the result of relay planning, containing all resolved
// information needed to execute an upstream provider call.
type RelayPlan struct {
	Auth          *AuthSnapshot
	Channel       *Channel
	ResolvedModel string
}

// RelayUsecase orchestrates the relay planning flow:
// model mapping → auth → model validation → channel selection.
type RelayUsecase struct {
	identity    IdentityClient
	channel     ChannelClient
	modelMapper *ModelMapper
	retryPolicy *RetryPolicy
}

// NewRelayUsecase creates a RelayUsecase with the given dependencies.
// modelMapper and retryPolicy may be nil (model mapping / retry disabled).
func NewRelayUsecase(identity IdentityClient, channel ChannelClient, modelMapper *ModelMapper, retryPolicy *RetryPolicy) *RelayUsecase {
	if retryPolicy == nil {
		retryPolicy = DefaultRetryPolicy()
	}
	return &RelayUsecase{
		identity:    identity,
		channel:     channel,
		modelMapper: modelMapper,
		retryPolicy: retryPolicy,
	}
}

// Plan resolves the model name, authenticates the user, validates permissions,
// and selects the best channel. Returns a RelayPlan with all resolved values.
func (uc *RelayUsecase) Plan(ctx context.Context, req RelayRequest) (*RelayPlan, error) {
	// 1. Resolve model name mapping (e.g. gpt-4o -> gpt-4o-2024-08-06)
	resolvedModel := req.Model
	if uc.modelMapper != nil {
		resolvedModel = uc.modelMapper.Resolve(req.Model)
	}

	// 2. Authenticate
	authSnapshot, err := uc.identity.GetAuthSnapshot(ctx, req.Token)
	if err != nil {
		return nil, err
	}

	// 3. Validate model permission
	if len(authSnapshot.AllowedModels) > 0 {
		allowed := false
		for _, m := range authSnapshot.AllowedModels {
			if m == req.Model {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("model %q not allowed for this token", req.Model)
		}
	}

	// 4. Select channel using the client-facing model name first. Existing
	// channel abilities are commonly keyed by the exposed names. If that fails
	// and the model mapper rewrote the name, fall back to the resolved upstream
	// model so deployments can expose aliases without duplicating abilities.
	channel, err := uc.channel.SelectChannel(ctx, authSnapshot.Group, req.Model, false)
	if err != nil {
		if resolvedModel == req.Model {
			return nil, err
		}
		channel, err = uc.channel.SelectChannel(ctx, authSnapshot.Group, resolvedModel, false)
		if err != nil {
			return nil, err
		}
	}

	return &RelayPlan{
		Auth:          authSnapshot,
		Channel:       channel,
		ResolvedModel: resolvedModel,
	}, nil
}

// NewRetryExecutor creates a RetryExecutor using this use case's retry policy
// and channel selector. Callers use this to execute upstream calls with
// automatic retry and channel fallback.
func (uc *RelayUsecase) NewRetryExecutor() *RetryExecutor {
	return NewRetryExecutor(uc.retryPolicy, uc.channel)
}

// ResolveModel returns the upstream model name for the given client model name.
// Returns the original name if no mapping exists or mapper is nil.
func (uc *RelayUsecase) ResolveModel(modelName string) string {
	if uc.modelMapper == nil {
		return modelName
	}
	return uc.modelMapper.Resolve(modelName)
}

// HasCapability checks if a model has the specified capability.
func (uc *RelayUsecase) HasCapability(modelName, capability string) bool {
	if uc.modelMapper == nil {
		return false
	}
	return uc.modelMapper.HasCapability(modelName, capability)
}
