package biz

import (
	"context"
	"fmt"
	"strings"
	"time"

	"micro-one-api/platform/metrics"
	relayprovider "micro-one-api/domain/upstream/provider"
)

// subscriptionAccountStatusEnabled mirrors channel biz ChannelStatusEnabled: a
// subscription account is only reusable via session stickiness when enabled.
const subscriptionAccountStatusEnabled int32 = 1

type IdentityClient interface {
	GetAuthSnapshot(ctx context.Context, token string) (*AuthSnapshot, error)
}

type ChannelClient interface {
	SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*Channel, error)
	RecordChannelHealth(ctx context.Context, channelID int64, success bool, err string, responseTime int64) error
}

type SubscriptionAccountClient interface {
	SelectSubscriptionAccount(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*SubscriptionAccount, error)
	// GetSubscriptionAccountByID materializes a single subscription account by
	// its id (with secrets) for session-stickiness reuse. Returns a nil account
	// (no error) when the id is unknown.
	GetSubscriptionAccountByID(ctx context.Context, accountID int64) (*SubscriptionAccount, error)
}

// SessionAccountStore resolves and refreshes the session -> subscription-account
// binding used for cross-session account stickiness (docs #7). It is satisfied
// by the server-layer sticky store (openAIWSStickyStore), which stores an int64
// account id keyed by group+sessionHash with a local hot cache + Redis. Lookup
// returns 0 on miss or backend error, so a Redis outage degrades to a normal
// (non-sticky) selection rather than failing the request.
type SessionAccountStore interface {
	LookupSessionChannel(ctx context.Context, group, sessionHash string) int64
	RefreshSessionTTL(ctx context.Context, group, sessionHash string, ttl time.Duration) bool
}

type RelayRequest struct {
	Token string
	Model string
	// SessionHash, when set and session stickiness is enabled, binds this
	// conversation to the subscription account that serves it so subsequent
	// turns reuse the same upstream account (prompt-cache reuse, docs #7).
	SessionHash string
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
	// Concurrency is the maximum number of in-flight relay requests this account
	// will serve at once. 0 means unlimited. Enforced by the relay gateway
	// (memory or Redis-backed AccountConcurrencyLimiter) so a single
	// subscription account is not saturated into upstream 429s.
	Concurrency int32
	// RPMLimit is the maximum number of relay dispatch attempts this account
	// will serve per rolling minute. 0 means unlimited.
	RPMLimit              int32
	SessionWindowLimitUSD float64
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
	blocker      RuntimeBlocker
	accountPool  *AccountPool
	now          func() time.Time

	// Session -> subscription-account stickiness (docs #7). All nil/false by
	// default: unless SetSessionAccountStore enables it, Plan behaves exactly as
	// before.
	sessionStore  SessionAccountStore
	stickyTTL     time.Duration
	stickyEnabled bool
}

// SetSessionAccountStore wires cross-session subscription-account stickiness.
// When enabled with a non-nil store, Plan tries to reuse the account bound to
// the request's SessionHash before falling back to normal priority selection.
// ttl refreshes the binding on a sticky hit (see openAIWSConfig.StickyTTL).
func (uc *RelayUsecase) SetSessionAccountStore(store SessionAccountStore, ttl time.Duration, enabled bool) {
	if uc == nil {
		return
	}
	uc.sessionStore = store
	uc.stickyTTL = ttl
	uc.stickyEnabled = enabled && store != nil
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
		blocker:      NoopRuntimeBlocker{},
		accountPool:  NewAccountPool(NoopRuntimeBlocker{}),
		now:          time.Now,
	}
}

func (uc *RelayUsecase) SetRuntimeBlocker(blocker RuntimeBlocker) {
	if uc == nil {
		return
	}
	if blocker == nil {
		blocker = NoopRuntimeBlocker{}
	}
	uc.blocker = blocker
	uc.accountPool = NewAccountPool(blocker)
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
			if strings.EqualFold(m, req.Model) {
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
		// Session stickiness: prefer the subscription account this conversation
		// was previously bound to (prompt-cache reuse) before normal selection.
		if ch, acct, ok := uc.trySubscriptionSticky(ctx, authSnapshot.Group, req.SessionHash, req.Model, resolvedModel); ok {
			return &RelayPlan{
				Auth:          authSnapshot,
				Channel:       ch,
				Account:       acct,
				ResolvedModel: resolvedModel,
			}, nil
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
	account, err := uc.selectSubscriptionAccountForModel(ctx, group, clientModel, nil)
	if err != nil && resolvedModel != clientModel {
		account, err = uc.selectSubscriptionAccountForModel(ctx, group, resolvedModel, nil)
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

// trySubscriptionSticky returns the subscription account previously bound to
// this session (and a thin channel view) when stickiness is enabled and the
// bound account is still a valid, schedulable candidate for the requested
// model. On any miss it returns ok=false so the caller falls back to normal
// priority selection. Selection-time outcomes ("miss", "reused_unschedulable")
// are recorded here; the authoritative "hit"/"rebind" is recorded by the server
// loop at bind time, since a selected sticky account may still be
// concurrency-full when it actually runs.
func (uc *RelayUsecase) trySubscriptionSticky(ctx context.Context, group, sessionHash, clientModel, resolvedModel string) (*Channel, *SubscriptionAccount, bool) {
	if !uc.stickyEnabled || uc.sessionStore == nil || uc.subscription == nil {
		return nil, nil, false
	}
	sessionHash = strings.TrimSpace(sessionHash)
	if sessionHash == "" {
		return nil, nil, false
	}
	stickyID := uc.sessionStore.LookupSessionChannel(ctx, group, sessionHash)
	if stickyID <= 0 {
		metrics.RelaySubscriptionStickyTotal.WithLabelValues("miss", "unknown").Inc()
		return nil, nil, false
	}
	account, ok := uc.selectStickySubscriptionAccount(ctx, group, clientModel, resolvedModel, stickyID)
	if !ok {
		return nil, nil, false
	}
	ch, err := subscriptionAccountToChannel(account)
	if err != nil {
		metrics.RelaySubscriptionStickyTotal.WithLabelValues("reused_unschedulable", platformOrUnknown(account.Platform)).Inc()
		return nil, nil, false
	}
	if uc.stickyTTL > 0 {
		uc.sessionStore.RefreshSessionTTL(ctx, group, sessionHash, uc.stickyTTL)
	}
	return ch, account, true
}

// selectStickySubscriptionAccount materializes the bound account by id and
// validates it is still reusable for this request. Concurrency is deliberately
// NOT checked here: the selection-time slot count is stale by the time the
// request runs, so the authoritative check is the in-loop TryAcquire in the
// server (a full account fails over without cooldown and rebinds).
func (uc *RelayUsecase) selectStickySubscriptionAccount(ctx context.Context, group, clientModel, resolvedModel string, stickyID int64) (*SubscriptionAccount, bool) {
	account, err := uc.subscription.GetSubscriptionAccountByID(ctx, stickyID)
	if err != nil || account == nil || account.ID <= 0 {
		metrics.RelaySubscriptionStickyTotal.WithLabelValues("reused_unschedulable", "unknown").Inc()
		return nil, false
	}
	if !uc.stickySubscriptionAccountValid(ctx, account, group, clientModel, resolvedModel) {
		metrics.RelaySubscriptionStickyTotal.WithLabelValues("reused_unschedulable", platformOrUnknown(account.Platform)).Inc()
		return nil, false
	}
	return account, true
}

// stickySubscriptionAccountValid reports whether a bound account may still serve
// this request: enabled, same tenancy group, platform+model still match, and not
// runtime-blocked/paused.
func (uc *RelayUsecase) stickySubscriptionAccountValid(ctx context.Context, account *SubscriptionAccount, group, clientModel, resolvedModel string) bool {
	if account.Status != subscriptionAccountStatusEnabled {
		return false
	}
	// Group is the subscription-account tenancy boundary: never reuse a binding
	// across groups.
	if account.Group != group {
		return false
	}
	// The bound account's platform must still serve the requested model (guards
	// mid-session model switches, e.g. claude -> gpt).
	if !platformServesModel(account.Platform, clientModel) && !platformServesModel(account.Platform, resolvedModel) {
		return false
	}
	if !accountServesModel(account, clientModel, resolvedModel) {
		return false
	}
	return uc.isSubscriptionAccountSchedulable(ctx, account)
}

func (uc *RelayUsecase) SelectSubscriptionFailover(ctx context.Context, group, clientModel, resolvedModel string, failedAccountIDs map[int64]bool) (*RelayPlan, error) {
	if uc == nil {
		return nil, fmt.Errorf("relay usecase unavailable")
	}
	if uc.subscription == nil {
		return nil, fmt.Errorf("subscription account selector is not configured")
	}
	account, err := uc.selectSubscriptionAccountForModel(ctx, group, clientModel, failedAccountIDs)
	if err != nil && resolvedModel != clientModel {
		account, err = uc.selectSubscriptionAccountForModel(ctx, group, resolvedModel, failedAccountIDs)
	}
	if err != nil {
		return nil, err
	}
	ch, err := subscriptionAccountToChannel(account)
	if err != nil {
		return nil, err
	}
	return &RelayPlan{
		Channel:       ch,
		Account:       account,
		ResolvedModel: resolvedModel,
	}, nil
}

func (uc *RelayUsecase) selectSubscriptionAccountForModel(ctx context.Context, group, model string, exclude map[int64]bool) (*SubscriptionAccount, error) {
	var lastErr error
	for _, platform := range subscriptionPlatformsForModel(model) {
		account, err := uc.selectSchedulableSubscriptionAccount(ctx, group, model, platform, exclude)
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

func (uc *RelayUsecase) selectSchedulableSubscriptionAccount(ctx context.Context, group, model, platform string, exclude map[int64]bool) (*SubscriptionAccount, error) {
	if uc.subscription == nil {
		return nil, fmt.Errorf("subscription account selector is not configured")
	}
	const maxAttempts = 8
	excludedPriority := false
	localExclude := make(map[int64]bool, len(exclude)+maxAttempts)
	for id, blocked := range exclude {
		if blocked {
			localExclude[id] = true
		}
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		account, err := uc.subscription.SelectSubscriptionAccount(ctx, group, model, platform, excludedPriority)
		if err != nil {
			return nil, err
		}
		if account == nil || account.ID <= 0 {
			return nil, fmt.Errorf("subscription account not found")
		}
		if localExclude[account.ID] {
			lastErr = fmt.Errorf("subscription account %d excluded", account.ID)
			excludedPriority = true
			continue
		}
		if !uc.isSubscriptionAccountSchedulable(ctx, account) {
			localExclude[account.ID] = true
			lastErr = fmt.Errorf("subscription account %d runtime blocked", account.ID)
			excludedPriority = true
			continue
		}
		return account, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("subscription account not found")
}

func (uc *RelayUsecase) isSubscriptionAccountSchedulable(ctx context.Context, account *SubscriptionAccount) bool {
	now := time.Now()
	if uc.now != nil {
		now = uc.now()
	}
	if uc.accountPool == nil {
		return true
	}
	return uc.accountPool.IsSchedulable(ctx, account, now)
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

// platformServesModel reports whether platform is a candidate platform for the
// given client-facing model, reusing the model->platform inference.
func platformServesModel(platform, model string) bool {
	if strings.TrimSpace(model) == "" {
		return false
	}
	for _, p := range subscriptionPlatformsForModel(model) {
		if p == platform {
			return true
		}
	}
	return false
}

// accountServesModel reports whether the account exposes the requested model.
// When the account carries no explicit model list we defer to the platform
// match (see platformServesModel) rather than rejecting the reuse.
func accountServesModel(account *SubscriptionAccount, clientModel, resolvedModel string) bool {
	if account == nil || len(account.Models) == 0 {
		return true
	}
	client := strings.TrimSpace(clientModel)
	resolved := strings.TrimSpace(resolvedModel)
	for _, m := range account.Models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if m == client || (resolved != "" && m == resolved) {
			return true
		}
	}
	return false
}

func platformOrUnknown(platform string) string {
	if strings.TrimSpace(platform) == "" {
		return "unknown"
	}
	return platform
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
	case "zhipu":
		return relayprovider.ChannelTypeZhipuPlan
	case "minimax":
		return relayprovider.ChannelTypeMinimaxPlan
	case "kimi":
		return relayprovider.ChannelTypeKimiOAuth
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
