package biz

import (
	"context"
	"fmt"
	"strings"

	relayprovider "micro-one-api/internal/relay/provider"
)

type IdentityClient interface {
	GetAuthSnapshot(ctx context.Context, token string) (*AuthSnapshot, error)
}

type ChannelClient interface {
	SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*Channel, error)
	RecordChannelHealth(ctx context.Context, channelID int64, success bool, err string, responseTime int64) error
}

type SubscriptionAccountClient interface {
	SelectSubscriptionAccount(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*SubscriptionAccount, error)
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

type SubscriptionAccount struct {
	ID          int64
	Name        string
	Platform    string
	AccountType string
	Status      int32
	BaseURL     string
	Group       string
	Models      []string
	Priority    int64
	AccessToken string
	AccountID   string
	Fingerprint string
}

// RelayPlan is the result of relay planning, containing all resolved
// information needed to execute an upstream provider call.
//
// For API-key channels only Channel is set. For subscription accounts the
// account is selected as a first-class entity and exposed on Account (NOT
// projected onto Channel): the selected Channel is a thin view carrying only
// the channel type + base URL + models, while the real account identity
// (access token, upstream account id, fingerprint) lives on Account. This
// keeps the access token out of Channel.Key, where it could otherwise leak
// through logging, health reporting or the OneAPI-compatible admin API.
type RelayPlan struct {
	Auth          *AuthSnapshot
	Channel       *Channel
	Account       *SubscriptionAccount
	ResolvedModel string
}

// RelayUsecase orchestrates the relay planning flow:
// model mapping → auth → model validation → channel selection.
type RelayUsecase struct {
	identity     IdentityClient
	channel      ChannelClient
	subscription SubscriptionAccountClient
	modelMapper  *ModelMapper
	retryPolicy  *RetryPolicy
}

// NewRelayUsecase creates a RelayUsecase with the given dependencies.
// modelMapper and retryPolicy may be nil (model mapping / retry disabled).
func NewRelayUsecase(identity IdentityClient, channel ChannelClient, modelMapper *ModelMapper, retryPolicy *RetryPolicy) *RelayUsecase {
	if retryPolicy == nil {
		retryPolicy = DefaultRetryPolicy()
	}
	var subscription SubscriptionAccountClient
	if selector, ok := channel.(SubscriptionAccountClient); ok {
		subscription = selector
	}
	return &RelayUsecase{
		identity:     identity,
		channel:      channel,
		subscription: subscription,
		modelMapper:  modelMapper,
		retryPolicy:  retryPolicy,
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
		channelErr := err
		if resolvedModel != req.Model {
			channel, err = uc.channel.SelectChannel(ctx, authSnapshot.Group, resolvedModel, false)
			if err == nil {
				return &RelayPlan{
					Auth:          authSnapshot,
					Channel:       channel,
					ResolvedModel: resolvedModel,
				}, nil
			}
			channelErr = err
		}
		subChannel, subAccount, subErr := uc.selectSubscriptionChannel(ctx, authSnapshot.Group, req.Model, resolvedModel)
		if subErr != nil {
			if uc.subscription == nil {
				return nil, channelErr
			}
			return nil, subErr
		}
		channel = subChannel
		return &RelayPlan{
			Auth:          authSnapshot,
			Channel:       channel,
			Account:       subAccount,
			ResolvedModel: resolvedModel,
		}, nil
	}

	return &RelayPlan{
		Auth:          authSnapshot,
		Channel:       channel,
		ResolvedModel: resolvedModel,
	}, nil
}

func (uc *RelayUsecase) selectSubscriptionChannel(ctx context.Context, group, clientModel, resolvedModel string) (*Channel, *SubscriptionAccount, error) {
	if uc.subscription == nil {
		return nil, nil, fmt.Errorf("subscription account selector is not configured")
	}
	account, err := uc.selectSubscriptionAccountForModel(ctx, group, clientModel)
	if err != nil && resolvedModel != clientModel {
		account, err = uc.selectSubscriptionAccountForModel(ctx, group, resolvedModel)
	}
	if err != nil {
		return nil, nil, err
	}
	ch, err := subscriptionAccountToChannel(account)
	if err != nil {
		return nil, nil, err
	}
	return ch, account, nil
}

func (uc *RelayUsecase) selectSubscriptionAccountForModel(ctx context.Context, group, model string) (*SubscriptionAccount, error) {
	var lastErr error
	for _, platform := range subscriptionPlatformsForModel(model) {
		account, err := uc.subscription.SelectSubscriptionAccount(ctx, group, model, platform, false)
		if err == nil {
			return account, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("subscription account platform cannot be inferred for model %q", model)
}

func subscriptionPlatformsForModel(model string) []string {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(lower, "claude-"):
		return []string{"claude"}
	case strings.HasPrefix(lower, "gpt-"), strings.HasPrefix(lower, "codex-"), strings.HasPrefix(lower, "o1"), strings.HasPrefix(lower, "o3"), strings.HasPrefix(lower, "o4"):
		return []string{"codex"}
	default:
		return []string{"codex", "claude"}
	}
}

func subscriptionAccountToChannel(account *SubscriptionAccount) (*Channel, error) {
	if account == nil {
		return nil, fmt.Errorf("subscription account is nil")
	}
	channelType := subscriptionPlatformChannelType(account.Platform)
	if channelType == 0 {
		return nil, fmt.Errorf("unsupported subscription platform %q", account.Platform)
	}
	return &Channel{
		ID:       account.ID,
		Type:     channelType,
		Name:     account.Name,
		Status:   account.Status,
		BaseURL:  account.BaseURL,
		Group:    account.Group,
		Models:   append([]string(nil), account.Models...),
		Priority: account.Priority,
		// Key intentionally left empty: the access token is NOT projected onto
		// the generic Channel.Key field. The server layer resolves it via the
		// SubscriptionAccountResolver (plan.Account) / credential store so it
		// cannot leak through code paths that treat Channel.Key as a plain
		// API key.
	}, nil
}

func subscriptionPlatformChannelType(platform string) int32 {
	switch platform {
	case "codex":
		return relayprovider.ChannelTypeCodexOAuth
	case "claude":
		return relayprovider.ChannelTypeClaudeOAuth
	default:
		return 0
	}
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
