package biz

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	billingdomain "micro-one-api/domain/billing"
	relayprovider "micro-one-api/domain/upstream/provider"
	apperrors "micro-one-api/pkg/errors"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/pkg/wildcard"
	"micro-one-api/platform/metrics"
)

// subscriptionAccountStatusEnabled mirrors channel biz ChannelStatusEnabled: a
// subscription account is only reusable via session stickiness when enabled.
const subscriptionAccountStatusEnabled int32 = 1

// Upstream cost-source kinds (v0.11.0 Phase 2 §2.2). These are aliases of the
// shared billing contract and are the prefix of the stable
// upstream cost key (channel:<id>:<upstream_model_id> /
// subscription:<id>:<upstream_model_id>).
const (
	UpstreamSourceChannel      = billingdomain.SourceKindChannel
	UpstreamSourceSubscription = billingdomain.SourceKindSubscription
)

type IdentityClient interface {
	// GetAuthSnapshot resolves the authorization view for an API access token.
	// clientIP is the caller's remote IP (best-effort, may be empty); identity
	// uses it to enforce optional token Subnet CIDR restrictions (review M1).
	GetAuthSnapshot(ctx context.Context, token, clientIP string) (*AuthSnapshot, error)
}

type ChannelClient interface {
	SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*Channel, error)
	// SelectChannelExcluding filters the given channel IDs out of selection
	// individually (per candidate, not per tier) so request-scoped failover can
	// reach healthy channels in any tier. Mirrors sub2api's
	// SelectAccountForModelWithExclusions.
	SelectChannelExcluding(ctx context.Context, group, model string, excluded map[int64]bool) (*Channel, error)
	RecordChannelHealth(ctx context.Context, channelID int64, success bool, err string, responseTime int64) error
	// RecordSubscriptionAccountHealth feeds a real upstream outcome into the
	// subscription-account selector so health and circuit state track live
	// traffic. accountID<=0 is a no-op for ordinary API-key channels.
	RecordSubscriptionAccountHealth(ctx context.Context, accountID int64, success bool) error
}

// SubscriptionAccountSlotReporter is the optional extension of ChannelClient
// that feeds relay-local in-flight slot changes back to channel-service so the
// subscription-account selector de-rates on the per-process view (memory
// limiter / Redis-fallback scenarios where the cross-replica LoadOracle reads
// zero). It is OPTIONAL: relay asserts the interface before calling, so
// existing fakes and clients that do not implement it keep working. Calls are
// fire-and-forget best-effort.
type SubscriptionAccountSlotReporter interface {
	RecordSubscriptionAccountSlot(ctx context.Context, accountID int64, acquired bool) error
}

type SubscriptionAccountClient interface {
	SelectSubscriptionAccount(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*SubscriptionAccount, error)
	// GetSubscriptionAccountByID materializes a single subscription account by
	// its id (with secrets) for session-stickiness reuse. Returns a nil account
	// (no error) when the id is unknown.
	GetSubscriptionAccountByID(ctx context.Context, accountID int64) (*SubscriptionAccount, error)
}

// SubscriptionAccountExcludingClient is the narrow extension of
// SubscriptionAccountClient that passes the request-scoped failed-account set
// to channel-service for server-side per-candidate filtering (sub2api #2).
// It is optional: relay falls back to the local exclusion loop when the client
// does not implement it, so existing fakes keep working.
type SubscriptionAccountExcludingClient interface {
	SelectSubscriptionAccountExcluding(ctx context.Context, group, model, platform string, excluded map[int64]bool) (*SubscriptionAccount, error)
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
	// ClientIP is the caller's remote IP forwarded to identity for optional
	// token Subnet CIDR enforcement (review M1). Best-effort; empty when
	// unavailable.
	ClientIP string
	// RequestID is the correlation id for the relay request (v0.11.0 Phase 3
	// §3.4 selection/execution boundary records). Optional: empty when the
	// caller does not supply one.
	RequestID string
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
	Weight   uint32
	Key      string
	Config   ChannelConfig

	// ModelMapping is a JSON {"src":"dst"} channel-level model remap.
	ModelMapping string
	// RestrictModels is the P1 (#2) catch-all flag: false=allow unregistered
	// models to route to this channel, true=require an abilities row. Default
	// true (legacy). See docs/model-management-design.md §9.3 #2.
	RestrictModels  bool
	UpstreamModelID string
	// SubscriptionAccountID identifies channels projected from subscription
	// accounts. Their numeric IDs live in a different namespace from ordinary
	// channels and must not be compared or health-recorded as channel IDs.
	SubscriptionAccountID int64
}

// RoutingSourceIdentity is a namespace-safe routing source key. Ordinary
// channel IDs and subscription-account IDs are allocated independently, so a
// bare int64 is never sufficient when excluding failed sources during
// cross-source fallback.
type RoutingSourceIdentity struct {
	Kind UpstreamRouteKind
	ID   int64
}

// RoutingSourceIdentityForChannel returns the stable source identity carried
// by a channel or subscription-account projection.
func RoutingSourceIdentityForChannel(ch *Channel) RoutingSourceIdentity {
	if ch == nil {
		return RoutingSourceIdentity{}
	}
	if ch.SubscriptionAccountID > 0 {
		return RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: ch.SubscriptionAccountID}
	}
	return RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: ch.ID}
}

// RoutingCandidate is one selectable source for a request, precomputed once by
// Plan() and consumed by RetryExecutor / SelectFallbackRoutingSource. It
// unifies API-key channels and subscription accounts so failover walks a single
// ordered list instead of recomputing per retry.
type RoutingCandidate struct {
	Identity RoutingSourceIdentity
	Priority int64
	Weight   int64
	Channel  *Channel             // nil when Kind == subscription
	Account  *SubscriptionAccount // nil when Kind == channel
}

// RoutingCandidateList holds the request-scoped ordered source list. The list
// is sorted by priority descending, then weight descending. Failover advances
// the cursor past excluded/invalid candidates without rebuilding the list per
// retry.
type RoutingCandidateList struct {
	Group       string
	Model       string
	GlobalModel string
	Candidates  []RoutingCandidate
	Excluded    map[RoutingSourceIdentity]bool
	pos         int
}

// Exclude marks a source as failed for this request.
func (l *RoutingCandidateList) Exclude(id RoutingSourceIdentity) {
	if l == nil {
		return
	}
	if l.Excluded == nil {
		l.Excluded = make(map[RoutingSourceIdentity]bool)
	}
	l.Excluded[id] = true
}

// Next returns the next selectable candidate and advances the cursor. It skips
// excluded candidates and nil channel/account entries. Returns nil when the
// list is exhausted.
func (l *RoutingCandidateList) Next() *RoutingCandidate {
	if l == nil {
		return nil
	}
	for l.pos < len(l.Candidates) {
		c := l.Candidates[l.pos]
		l.pos++
		if l.Excluded != nil && l.Excluded[c.Identity] {
			continue
		}
		if c.Channel == nil && c.Account == nil {
			continue
		}
		return &c
	}
	return nil
}

// Peek returns the candidate at the current cursor without advancing.
func (l *RoutingCandidateList) Peek() *RoutingCandidate {
	if l == nil || l.pos >= len(l.Candidates) {
		return nil
	}
	c := l.Candidates[l.pos]
	return &c
}

// IsEmpty reports whether there are no candidates at all.
func (l *RoutingCandidateList) IsEmpty() bool {
	return l == nil || len(l.Candidates) == 0
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
	// Weight is the explicit within-tier WRR weight (v0.11.0 Phase 3 §3.1).
	// 0 = unset, falls back to priority-derived in subscriptionRouteWeight.
	Weight      int32
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

	// ModelMapping is a JSON {"src":"dst"} per-account remap applied after global ModelMapper.
	ModelMapping    string
	UpstreamModelID string
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
	Auth    *AuthSnapshot
	Channel *Channel
	Account *SubscriptionAccount
	// SelectionEvent carries the routing selection metadata (source kind,
	// provider family, priority tier, etc.) from Plan() to the execution
	// boundary so the orchestrator can finalize it with the execution result
	// (success/error/fallback + elapsed time) and emit the second half of the
	// selection observation (Phase 3 §3.4). Nil when no selection was recorded
	// (e.g. sticky hit or error path before selection).
	SelectionEvent *SelectionEvent
	// GlobalModel is the model name after the global ModelMapper but BEFORE
	// per-channel/per-account model mapping. Plan() bakes the first selected
	// channel's mapping into ResolvedModel, which breaks failover: a retry
	// that selects a different channel re-applies that channel's mapping on
	// top of ResolvedModel — i.e. channel A's mapped name — instead of the
	// globally-resolved name, so the upstream sees a model name A produced,
	// not what B's mapping expects. Server-layer retry closures now recompute
	// the upstream model from GlobalModel. GlobalModel is
	// always set when ResolvedModel is; it equals ResolvedModel when no global
	// mapping applied.
	GlobalModel string
	// ResolvedModel is the model after global + the FIRST channel/account
	// mapping. Kept for billing/log compatibility; do NOT feed it back into
	// ApplyChannelModelMapping on retry — use GlobalModel.
	ResolvedModel string
	// Candidates is the request-scoped ordered routing source list populated by
	// Plan() and consumed by RetryExecutor / SelectFallbackRoutingSource for
	// deterministic failover progression (sub2api #2).
	Candidates *RoutingCandidateList
}

// BaseModel returns the model before per-channel/per-account mapping. Older
// plans reconstructed from sticky response state may not carry GlobalModel,
// so ResolvedModel is retained as a safe compatibility fallback.
func (p *RelayPlan) BaseModel() string {
	if p == nil {
		return ""
	}
	if model := strings.TrimSpace(p.GlobalModel); model != "" {
		return model
	}
	return strings.TrimSpace(p.ResolvedModel)
}

// RelayUsecase orchestrates the relay planning flow:
// model mapping → auth → model validation → channel selection.
type RelayUsecase struct {
	identity      IdentityClient
	channel       ChannelClient
	subscription  SubscriptionAccountClient
	modelMapper   *ModelMapper
	retryPolicy   *RetryPolicy
	blocker       RuntimeBlocker
	accountPool   *AccountPool
	routeSelector *UpstreamRouteSelector
	now           func() time.Time
	// v0.11.0 Phase 3 §3.4: selection/execution boundary recorder. No-op by
	// default; SetSelectionRecorder wires the logging+metrics recorder.
	selectionRec selectionRecorderHolder

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
// RecordSubscriptionAccountHealth forwards a real upstream account outcome to
// channel-service, which owns the account selector and circuit-breaker state.
func (uc *RelayUsecase) RecordSubscriptionAccountHealth(ctx context.Context, accountID int64, success bool) error {
	if uc == nil || uc.channel == nil || accountID <= 0 {
		return nil
	}
	return uc.channel.RecordSubscriptionAccountHealth(ctx, accountID, success)
}

// RecordSubscriptionAccountSlot forwards a relay-local slot acquire/release to
// the channel selector's per-process inflight counter (weight loop closure).
// The client is optional (interface assertion): channel clients that do not
// implement the reporter simply skip the feedback, so single-replica or
// legacy setups are unaffected.
func (uc *RelayUsecase) RecordSubscriptionAccountSlot(ctx context.Context, accountID int64, acquired bool) error {
	if uc == nil || uc.channel == nil || accountID <= 0 {
		return nil
	}
	if reporter, ok := uc.channel.(SubscriptionAccountSlotReporter); ok {
		return reporter.RecordSubscriptionAccountSlot(ctx, accountID, acquired)
	}
	return nil
}

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
		identity:      identity,
		channel:       channel,
		subscription:  subscription,
		modelMapper:   modelMapper,
		retryPolicy:   retryPolicy,
		blocker:       NoopRuntimeBlocker{},
		accountPool:   NewAccountPool(NoopRuntimeBlocker{}),
		routeSelector: NewUpstreamRouteSelector(),
		now:           time.Now,
	}
}

// SetSelectionRecorder wires the v0.11.0 Phase 3 §3.4 selection/execution
// boundary recorder. nil or unset = no-op (events dropped), so existing call
// sites and tests keep working.
func (uc *RelayUsecase) SetSelectionRecorder(r SelectionRecorder) {
	if uc == nil {
		return
	}
	uc.selectionRec.set(r)
}

// GetSelectionRecorder returns the currently wired SelectionRecorder (or the
// noop recorder when none is set). Used by the orchestrator to finalize
// selection events at the execution boundary (Phase 3 §3.4).
func (uc *RelayUsecase) GetSelectionRecorder() SelectionRecorder {
	if uc == nil {
		return noopSelectionRecorder{}
	}
	return uc.selectionRec.get()
}

func (uc *RelayUsecase) recordSelection(ctx context.Context, event SelectionEvent) SelectionEvent {
	if uc == nil {
		return event
	}
	if event.At.IsZero() {
		event.At = uc.now()
	}
	uc.selectionRec.get().RecordSelection(ctx, event)
	return event
}

// recordSelectionForPlan records a selection event and returns a pointer to
// the timestamped copy so the caller can attach it to the RelayPlan. The plan
// then carries the event to the execution boundary, where the orchestrator
// finalizes it with the execution result (success/error/fallback + elapsed).
// The event is marked Planned=true so the recorder emits only the
// plan-boundary metrics (RoutingSelectionPlanned + duration); the outcome
// counters (RoutingSelectionTotal / StickyHit / Fallback) fire once at the
// execution boundary via FinalizeSelectionResult (code review #1).
// planStartedAt is used to compute the selection latency (code review
// MEDIUM-4): the duration histogram records Plan-boundary selection time.
func (uc *RelayUsecase) recordSelectionForPlan(ctx context.Context, event SelectionEvent, planStartedAt time.Time) *SelectionEvent {
	event.Planned = true
	if !planStartedAt.IsZero() {
		event.ElapsedMS = uc.now().Sub(planStartedAt).Milliseconds()
	}
	e := uc.recordSelection(ctx, event)
	return &e
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
	// Measure the selection (Plan) latency so routing_selection_duration is
	// observed at the plan boundary (code review MEDIUM-4). The histogram
	// records selection time, NOT execution time.
	planStartedAt := uc.now()
	// v0.11.0 Phase 3 §3.4: ensure the selection event carries a correlation
	// id. When the caller did not supply one (most server-layer call sites
	// generate it later for billing), Plan mints a short id so the event can
	// still be traced.
	if req.RequestID == "" {
		req.RequestID = generateSelectionRequestID()
	}
	// A terminal [1M] is a client-side extended-context hint, not part of the
	// model identifier understood by the registry or upstream provider.
	req.Model = RelayModelName(req.Model)

	// 1. Resolve model name mapping (e.g. gpt-4o -> gpt-4o-2024-08-06)
	resolvedModel := req.Model
	if uc.modelMapper != nil {
		resolvedModel = uc.modelMapper.Resolve(req.Model)
	}

	// 2. Authenticate
	authSnapshot, err := uc.identity.GetAuthSnapshot(ctx, req.Token, req.ClientIP)
	if err != nil {
		return nil, err
	}

	// 3. Validate model permission
	if len(authSnapshot.AllowedModels) > 0 {
		allowed := false
		for _, m := range authSnapshot.AllowedModels {
			if strings.EqualFold(RelayModelName(m), req.Model) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, apperrors.Newf(apperrors.ReasonModelForbidden, "model %q not allowed for this token", req.Model)
		}
	}

	// 4. A valid sticky subscription route remains authoritative for the
	// conversation. For a new conversation, API-key channels and subscription
	// accounts participate in one priority/weight selection instead of treating
	// subscription accounts as a fallback that can only run when every channel
	// fails.
	if ch, acct, ok := uc.trySubscriptionSticky(ctx, authSnapshot.Group, req.SessionHash, req.Model, resolvedModel); ok {
		_sel := uc.recordSelectionForPlan(ctx, SelectionEvent{
			RequestID:      req.RequestID,
			Group:          authSnapshot.Group,
			Model:          req.Model,
			StickyHit:      true,
			FinalKind:      UpstreamRouteSubscription.String(),
			FinalSourceID:  acct.ID,
			ProviderFamily: ProviderFamilyForModel(req.Model),
		}, planStartedAt)
		_plan := newRelayPlan(authSnapshot, ch, acct, resolvedModel)
		_plan.SelectionEvent = _sel
		return _plan, nil
	}

	channel, channelErr := uc.selectAPIKeyChannel(ctx, authSnapshot.Group, req.Model, resolvedModel)
	subChannel, subAccount, subErr := uc.selectSubscriptionChannel(ctx, authSnapshot.Group, req.Model, resolvedModel)

	switch {
	case channel != nil && subChannel != nil:
		choice := uc.routeSelector.Select(authSnapshot.Group, req.Model, []UpstreamRouteCandidate{
			{Kind: UpstreamRouteChannel, ID: channel.ID, Priority: channel.Priority, Weight: selectorWeight(channel.Weight)},
			{Kind: UpstreamRouteSubscription, ID: subAccount.ID, Priority: subAccount.Priority, Weight: subscriptionRouteWeight(subAccount)},
		})
		channelCand := RoutingCandidate{
			Identity: RoutingSourceIdentity{Kind: UpstreamRouteChannel, ID: channel.ID},
			Priority: channel.Priority,
			Weight:   selectorWeight(channel.Weight),
			Channel:  channel,
		}
		subCand := RoutingCandidate{
			Identity: RoutingSourceIdentity{Kind: UpstreamRouteSubscription, ID: subAccount.ID},
			Priority: subAccount.Priority,
			Weight:   subscriptionRouteWeight(subAccount),
			Channel:  subChannel,
			Account:  subAccount,
		}
		if choice.Kind == UpstreamRouteSubscription {
			_sel := uc.recordSelectionForPlan(ctx, SelectionEvent{
				RequestID:      req.RequestID,
				Group:          authSnapshot.Group,
				Model:          req.Model,
				CandidateKinds: []string{"channel", "subscription"},
				FinalKind:      UpstreamRouteSubscription.String(),
				FinalSourceID:  subAccount.ID,
				PriorityTier:   subAccount.Priority,
				ProviderFamily: ProviderFamilyForModel(req.Model),
			}, planStartedAt)
			_plan := newRelayPlan(authSnapshot, subChannel, subAccount, resolvedModel)
			_plan.SelectionEvent = _sel
			_plan.Candidates = newRoutingCandidateList(authSnapshot.Group, req.Model, resolvedModel, subCand, channelCand)
			return _plan, nil
		}
		_sel := uc.recordSelectionForPlan(ctx, SelectionEvent{
			RequestID:      req.RequestID,
			Group:          authSnapshot.Group,
			Model:          req.Model,
			CandidateKinds: []string{"channel", "subscription"},
			FinalKind:      UpstreamRouteChannel.String(),
			FinalSourceID:  channel.ID,
			PriorityTier:   channel.Priority,
			ProviderFamily: ProviderFamilyForModel(req.Model),
		}, planStartedAt)
		_plan := newRelayPlan(authSnapshot, channel, nil, resolvedModel)
		_plan.SelectionEvent = _sel
		_plan.Candidates = newRoutingCandidateList(authSnapshot.Group, req.Model, resolvedModel, channelCand, subCand)
		return _plan, nil
	case channel != nil:
		_sel := uc.recordSelectionForPlan(ctx, SelectionEvent{
			RequestID:      req.RequestID,
			Group:          authSnapshot.Group,
			Model:          req.Model,
			CandidateKinds: []string{"channel"},
			FinalKind:      UpstreamRouteChannel.String(),
			FinalSourceID:  channel.ID,
			PriorityTier:   channel.Priority,
			ProviderFamily: ProviderFamilyForModel(req.Model),
		}, planStartedAt)
		_plan := newRelayPlan(authSnapshot, channel, nil, resolvedModel)
		_plan.SelectionEvent = _sel
		return _plan, nil
	case subChannel != nil:
		_sel := uc.recordSelectionForPlan(ctx, SelectionEvent{
			RequestID:      req.RequestID,
			Group:          authSnapshot.Group,
			Model:          req.Model,
			CandidateKinds: []string{"subscription"},
			FinalKind:      UpstreamRouteSubscription.String(),
			FinalSourceID:  subAccount.ID,
			PriorityTier:   subAccount.Priority,
			ProviderFamily: ProviderFamilyForModel(req.Model),
		}, planStartedAt)
		_plan := newRelayPlan(authSnapshot, subChannel, subAccount, resolvedModel)
		_plan.SelectionEvent = _sel
		return _plan, nil
	case uc.subscription == nil:
		return nil, channelErr
	case subErr != nil:
		return nil, subErr
	default:
		return nil, channelErr
	}
}

func (uc *RelayUsecase) selectAPIKeyChannel(ctx context.Context, group, clientModel, resolvedModel string) (*Channel, error) {
	channel, err := uc.channel.SelectChannel(ctx, group, clientModel, false)
	if err == nil {
		return channel, nil
	}
	if resolvedModel != clientModel {
		return uc.channel.SelectChannel(ctx, group, resolvedModel, false)
	}
	return nil, err
}

func newRelayPlan(auth *AuthSnapshot, channel *Channel, account *SubscriptionAccount, resolvedModel string) *RelayPlan {
	return &RelayPlan{
		Auth:          auth,
		Channel:       channel,
		Account:       account,
		GlobalModel:   resolvedModel,
		ResolvedModel: ResolveChannelModel(channel, resolvedModel),
	}
}

// newRoutingCandidateList builds the request-scoped ordered failover list
// (sub2api #2). The winner is pre-excluded because attempt 0 already runs
// against it; the first retry then advances to the first alternate without a
// fresh selection RPC.
func newRoutingCandidateList(group, model, globalModel string, winner RoutingCandidate, alternates ...RoutingCandidate) *RoutingCandidateList {
	l := &RoutingCandidateList{
		Group:       group,
		Model:       model,
		GlobalModel: globalModel,
		Candidates:  append([]RoutingCandidate{winner}, alternates...),
	}
	l.Exclude(winner.Identity)
	return l
}

// subscriptionRouteWeight returns the cross-source selection weight for a
// subscription account. v0.11.0 Phase 3 §3.1: the explicit Weight field wins;
// priority is for layering only; 0/1 collapse to 1. This replaces the
// hard-coded Weight:1 that made subscription accounts always lose to channels
// in the cross-source smooth-WRR.
func subscriptionRouteWeight(acct *SubscriptionAccount) int64 {
	if acct == nil {
		return 1
	}
	if acct.Weight > 0 {
		return int64(acct.Weight)
	}
	if acct.Priority > 0 {
		return acct.Priority
	}
	return 1
}

func selectorWeight(weight uint32) int64 {
	if weight == 0 {
		return 1
	}
	return int64(weight)
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
// this request: enabled, same tenancy group, model still matches, and not
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
	// Explicit account models are the source of truth and support operator-defined
	// aliases. Only infer from the platform when the account has no model list.
	if len(account.Models) > 0 {
		if !accountServesModel(account, clientModel, resolvedModel) {
			return false
		}
	} else if !platformServesModel(account.Platform, clientModel) && !platformServesModel(account.Platform, resolvedModel) {
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
	clientModel = RelayModelName(clientModel)
	resolvedModel = RelayModelName(resolvedModel)
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
	// recompute the upstream model against the NEW account's
	// mapping starting from the globally-resolved model (the caller passes
	// base.GlobalModel here, NOT base.ResolvedModel which already carries
	// account A's mapping). Pre-fix this recomputed against resolvedModel
	// verbatim — but callers were passing base.ResolvedModel (already mapped
	// by A), so the result was A's mapped name fed into B's mapping lookup.
	return &RelayPlan{
		Channel:       ch,
		Account:       account,
		GlobalModel:   resolvedModel,
		ResolvedModel: ResolveChannelModel(ch, resolvedModel),
	}, nil
}

// ResolveSubscriptionRoutingSource materializes and validates an exact
// subscription-account sticky binding. It returns both the account projection
// used by generic transports and the full account DO required by adaptor
// transports. Empty model arguments skip model validation for legacy stored
// response routes that did not persist the client model.
func (uc *RelayUsecase) ResolveSubscriptionRoutingSource(
	ctx context.Context,
	accountID int64,
	group, clientModel, resolvedModel string,
) (*Channel, *SubscriptionAccount, error) {
	if uc == nil || uc.subscription == nil || accountID <= 0 {
		return nil, nil, fmt.Errorf("subscription account selector is not configured")
	}
	account, err := uc.subscription.GetSubscriptionAccountByID(ctx, accountID)
	if err != nil {
		return nil, nil, err
	}
	if account == nil || account.ID <= 0 || account.Status != subscriptionAccountStatusEnabled || account.Group != group {
		return nil, nil, fmt.Errorf("subscription account %d is not reusable", accountID)
	}
	if strings.TrimSpace(clientModel) != "" || strings.TrimSpace(resolvedModel) != "" {
		if len(account.Models) > 0 {
			if !accountServesModel(account, clientModel, resolvedModel) {
				return nil, nil, fmt.Errorf("subscription account %d does not serve model", accountID)
			}
		} else if !platformServesModel(account.Platform, clientModel) && !platformServesModel(account.Platform, resolvedModel) {
			return nil, nil, fmt.Errorf("subscription account %d does not serve model", accountID)
		}
	}
	if !uc.isSubscriptionAccountSchedulable(ctx, account) {
		return nil, nil, fmt.Errorf("subscription account %d is not schedulable", accountID)
	}
	channel, err := subscriptionAccountToChannel(account)
	if err != nil {
		return nil, nil, err
	}
	return channel, account, nil
}

func (uc *RelayUsecase) selectSubscriptionAccountForModel(ctx context.Context, group, model string, exclude map[int64]bool) (*SubscriptionAccount, error) {
	platforms := subscriptionPlatformsForModel(model)
	if len(platforms) == 0 {
		// The abilities table is authoritative for aliases and future providers.
		return uc.selectSchedulableSubscriptionAccount(ctx, group, model, "", exclude)
	}

	for _, platform := range platforms {
		account, err := uc.selectSchedulableSubscriptionAccount(ctx, group, model, platform, exclude)
		if err == nil {
			return account, nil
		}
	}
	// A known model prefix is a routing hint, not a hard boundary. This lets an
	// explicit cross-platform ability or model mapping override the convention.
	return uc.selectSchedulableSubscriptionAccount(ctx, group, model, "", exclude)
}

func (uc *RelayUsecase) selectSchedulableSubscriptionAccount(ctx context.Context, group, model, platform string, exclude map[int64]bool) (*SubscriptionAccount, error) {
	if uc.subscription == nil {
		return nil, fmt.Errorf("subscription account selector is not configured")
	}
	// sub2api #2: when the client supports server-side exclusion, pass the
	// request-scoped failed set down instead of looping over priority tiers.
	// The local schedulability re-check still runs so runtime-blocked accounts
	// (relay-local state channel-service cannot see) are skipped and excluded.
	if exClient, ok := uc.subscription.(SubscriptionAccountExcludingClient); ok {
		localExclude := make(map[int64]bool, len(exclude)+8)
		for id, blocked := range exclude {
			if blocked {
				localExclude[id] = true
			}
		}
		var lastErr error
		for range 8 {
			account, err := exClient.SelectSubscriptionAccountExcluding(ctx, group, model, platform, localExclude)
			if err != nil {
				return nil, err
			}
			if account == nil || account.ID <= 0 {
				return nil, fmt.Errorf("subscription account not found")
			}
			if !uc.isSubscriptionAccountSchedulable(ctx, account) {
				localExclude[account.ID] = true
				lastErr = fmt.Errorf("subscription account %d runtime blocked", account.ID)
				continue
			}
			return account, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("subscription account not found")
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
	for range maxAttempts {
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
	lower := strings.ToLower(RelayModelName(model))
	switch {
	case strings.HasPrefix(lower, "claude-"):
		return []string{"claude"}
	case strings.HasPrefix(lower, "gpt-"), strings.HasPrefix(lower, "codex-"), strings.HasPrefix(lower, "o1"), strings.HasPrefix(lower, "o3"), strings.HasPrefix(lower, "o4"):
		return []string{"codex"}
	case strings.HasPrefix(lower, "glm-"):
		return []string{"zhipu"}
	case strings.HasPrefix(lower, "minimax-"), strings.HasPrefix(lower, "minimaxm-"):
		return []string{"minimax"}
	case strings.HasPrefix(lower, "kimi-"), strings.HasPrefix(lower, "k3"):
		return []string{"kimi"}
	default:
		return nil
	}
}

// platformServesModel reports whether platform is a candidate platform for the
// given client-facing model, reusing the model->platform inference.
func platformServesModel(platform, model string) bool {
	if strings.TrimSpace(model) == "" {
		return false
	}
	return slices.Contains(subscriptionPlatformsForModel(model), platform)
}

// accountServesModel reports whether the account exposes the requested model.
// When the account carries no explicit model list we defer to the platform
// match (see platformServesModel) rather than rejecting the reuse.
func accountServesModel(account *SubscriptionAccount, clientModel, resolvedModel string) bool {
	if account == nil || len(account.Models) == 0 {
		return true
	}
	client := RelayModelName(clientModel)
	resolved := RelayModelName(resolvedModel)
	for _, m := range account.Models {
		m = RelayModelName(m)
		if m == "" {
			continue
		}
		if strings.EqualFold(m, client) || (resolved != "" && strings.EqualFold(m, resolved)) {
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
		ID:                    account.ID,
		SubscriptionAccountID: account.ID,
		Type:                  channelType,
		Name:                  account.Name,
		Status:                account.Status,
		BaseURL:               account.BaseURL,
		Group:                 account.Group,
		Models:                append([]string(nil), account.Models...),
		Priority:              account.Priority,
		ModelMapping:          account.ModelMapping,
		UpstreamModelID:       account.UpstreamModelID,
		RestrictModels:        true, // subscription accounts require explicit abilities; never catch-all
		// Key intentionally left empty: the access token is NOT projected onto
		// the generic Channel.Key field. The server layer resolves it via the
		// SubscriptionAccountResolver (plan.Account) / credential store so it
		// cannot leak through code paths that treat Channel.Key as a plain
		// API key.
	}, nil
}

// applyPerAccountModelMapping applies the per-account or per-channel model
// mapping (JSON {"src":"dst"}) on top of the globally-resolved model name.
// If no mapping is configured or the model is not in the map, the input is
// returned unchanged. This is the second remap pass: the global ModelMapper
// runs first in Plan(), then this applies the account/channel-specific remap
// so different upstream providers can map the same client model name to
// different upstream model identifiers. See docs/model-management-design.md §10.1.
//
// P1 (#4): mapping keys may be shell-style wildcards ("claude-*", "*").
// Exact (case-insensitive) match is tried first; if it misses, wildcard keys
// are tried with specific patterns before the "*" catch-all, so a narrow
// "claude-*" shadows a broad "*". This lets an account remap a whole model
// family to one upstream name without enumerating every minor version.
// See docs/model-management-design.md §9.3 #4.
// ApplyChannelModelMapping is the exported form of applyPerAccountModelMapping
// for the server layer: it recomputes the upstream model against a channel's
// (or subscription account's) JSON model mapping. Used by RetryExecutor
// closures to re-apply the per-channel mapping after a retry selects a
// different channel, so the upstream body carries the new channel's mapping
// instead of the first channel's.
func ApplyChannelModelMapping(mappingJSON, model string) string {
	return applyPerAccountModelMapping(mappingJSON, model)
}

// ResolveChannelModel returns the exact model identifier to send to a selected
// upstream. An explicit per-channel mapping is authoritative. Otherwise, when
// selection matched a configured model case-insensitively, preserve the
// channel's configured spelling because some upstreams treat model IDs as
// case-sensitive. Wildcard abilities remain routing-only and are never sent as
// model identifiers.
func ResolveChannelModel(channel *Channel, model string) string {
	model = RelayModelName(model)
	if channel == nil {
		return model
	}
	if mapped, ok := resolvePerAccountModelMapping(channel.ModelMapping, model); ok {
		return RelayModelName(mapped)
	}
	if upstream := strings.TrimSpace(channel.UpstreamModelID); upstream != "" {
		return RelayModelName(upstream)
	}
	for _, configured := range channel.Models {
		configured = RelayModelName(configured)
		if configured != "" && !wildcard.IsPattern(configured) && strings.EqualFold(configured, model) {
			return configured
		}
	}
	return model
}

func applyPerAccountModelMapping(mappingJSON, model string) string {
	model = RelayModelName(model)
	if mapped, ok := resolvePerAccountModelMapping(mappingJSON, model); ok {
		return RelayModelName(mapped)
	}
	return model
}

func resolvePerAccountModelMapping(mappingJSON, model string) (string, bool) {
	model = RelayModelName(model)
	mappingJSON = strings.TrimSpace(mappingJSON)
	if mappingJSON == "" {
		return "", false
	}
	var mapping map[string]string
	if err := jsonx.UnmarshalFromString(mappingJSON, &mapping); err != nil {
		return "", false
	}
	// 1) Exact (case-insensitive) match — fast path.
	if dst, ok := mapping[model]; ok && dst != "" {
		return dst, true
	}
	if dst, ok := mapping[strings.ToLower(model)]; ok && dst != "" {
		return dst, true
	}
	// A JSON object may contain mixed-case keys. Map lookup cannot express a
	// true case-insensitive comparison, so finish the exact pass explicitly.
	for key, dst := range mapping {
		if !wildcard.IsPattern(key) && dst != "" && strings.EqualFold(RelayModelName(key), model) {
			return dst, true
		}
	}
	// 2) Wildcard keys: specific patterns before the "*" catch-all. When
	// several specific patterns match, pick the MOST SPECIFIC one (by
	// non-wildcard char count, ties by full length) so per-account
	// remapping is deterministic — same as ModelMapper.Resolve. See
	// docs/model-management-design.md §11.1.
	var catchAll string
	var bestSpecific string
	var bestSpecificKey string
	var bestSpecificity int
	for key, dst := range mapping {
		if !wildcard.IsPattern(key) || dst == "" {
			continue
		}
		if key == "*" {
			catchAll = dst
			continue
		}
		if !wildcard.Match(key, model) {
			continue
		}
		spec := wildcard.Specificity(key)
		if spec > bestSpecificity || (spec == bestSpecificity && len(key) > len(bestSpecificKey)) {
			bestSpecificity = spec
			bestSpecific = dst
			bestSpecificKey = key
		}
	}
	if bestSpecific != "" {
		return bestSpecific, true
	}
	if catchAll != "" {
		return catchAll, true
	}
	return "", false
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
// and channel selector. The usecase itself is wired as the unified
// cross-namespace fallback selector (sub2api #2), so retries walk the
// request-scoped candidate list first and only re-select with per-candidate
// exclusion when it is exhausted.
func (uc *RelayUsecase) NewRetryExecutor() *RetryExecutor {
	return NewRetryExecutor(uc.retryPolicy, uc.channel).WithFallbackSelector(uc)
}

// SelectFallbackChannel selects from a lower-priority channel tier. It is a
// narrow execution-boundary seam for transports, such as Responses WebSocket,
// that cannot replay through RetryExecutor after downstream bytes are sent.
func (uc *RelayUsecase) SelectFallbackChannel(ctx context.Context, group, model string) (*Channel, error) {
	if uc == nil || uc.channel == nil {
		return nil, fmt.Errorf("channel selector unavailable")
	}
	return uc.channel.SelectChannel(ctx, group, RelayModelName(model), true)
}

// SelectFallbackRoutingSource selects a fallback across both API-key channels
// and subscription accounts while excluding every source that has already
// failed in the current request. This is used by transports that cannot replay
// through RetryExecutor, notably Responses WebSocket.
func (uc *RelayUsecase) SelectFallbackRoutingSource(
	ctx context.Context,
	group, clientModel, resolvedModel string,
	excluded map[RoutingSourceIdentity]bool,
) (*Channel, error) {
	if uc == nil {
		return nil, fmt.Errorf("relay usecase unavailable")
	}
	clientModel = RelayModelName(clientModel)
	resolvedModel = RelayModelName(resolvedModel)

	var channel *Channel
	var channelErr error
	if uc.channel != nil {
		// Pass the request-scoped failures into selection as a filter so a
		// just-failed channel is never re-returned and healthy channels in any
		// tier stay reachable. (Post-hoc exclusion after SelectChannel would
		// strand lower tiers: the selector can keep returning the same failed
		// channel while it remains selectable below the circuit threshold.)
		excludedChannels := make(map[int64]bool)
		for source, blocked := range excluded {
			if blocked && source.Kind == UpstreamRouteChannel && source.ID > 0 {
				excludedChannels[source.ID] = true
			}
		}
		channel, channelErr = uc.channel.SelectChannelExcluding(ctx, group, clientModel, excludedChannels)
		if channelErr != nil && resolvedModel != clientModel {
			channel, channelErr = uc.channel.SelectChannelExcluding(ctx, group, resolvedModel, excludedChannels)
		}
	}

	var subChannel *Channel
	var subAccount *SubscriptionAccount
	var subErr error
	if uc.subscription != nil {
		failedAccounts := make(map[int64]bool)
		for source, blocked := range excluded {
			if blocked && source.Kind == UpstreamRouteSubscription && source.ID > 0 {
				failedAccounts[source.ID] = true
			}
		}
		subAccount, subErr = uc.selectSubscriptionAccountForModel(ctx, group, clientModel, failedAccounts)
		if subErr != nil && resolvedModel != clientModel {
			subAccount, subErr = uc.selectSubscriptionAccountForModel(ctx, group, resolvedModel, failedAccounts)
		}
		if subAccount != nil {
			subChannel, subErr = subscriptionAccountToChannel(subAccount)
		}
	}

	switch {
	case channel != nil && subChannel != nil:
		choice := uc.routeSelector.Select(group, clientModel, []UpstreamRouteCandidate{
			{Kind: UpstreamRouteChannel, ID: channel.ID, Priority: channel.Priority, Weight: selectorWeight(channel.Weight)},
			{Kind: UpstreamRouteSubscription, ID: subAccount.ID, Priority: subAccount.Priority, Weight: subscriptionRouteWeight(subAccount)},
		})
		if choice.Kind == UpstreamRouteSubscription {
			return subChannel, nil
		}
		return channel, nil
	case channel != nil:
		return channel, nil
	case subChannel != nil:
		return subChannel, nil
	case channelErr != nil && subErr != nil:
		return nil, fmt.Errorf("no fallback routing source: channel: %v; subscription: %v", channelErr, subErr)
	case channelErr != nil:
		return nil, channelErr
	case subErr != nil:
		return nil, subErr
	default:
		return nil, fmt.Errorf("no fallback routing source")
	}
}

// RecordRoutingSourceHealth records an execution result in the correct source
// namespace. Subscription projections must never update an ordinary channel
// that happens to have the same numeric ID.
func (uc *RelayUsecase) RecordRoutingSourceHealth(ctx context.Context, ch *Channel, success bool, errMessage string, responseTime int64) {
	if uc == nil || uc.channel == nil || ch == nil {
		return
	}
	if ch.SubscriptionAccountID > 0 {
		_ = uc.channel.RecordSubscriptionAccountHealth(ctx, ch.SubscriptionAccountID, success)
		return
	}
	if ch.ID > 0 {
		_ = uc.channel.RecordChannelHealth(ctx, ch.ID, success, errMessage, responseTime)
	}
}

// ResolveModel returns the upstream model name for the given client model name.
// Returns the original name if no mapping exists or mapper is nil.
func (uc *RelayUsecase) ResolveModel(modelName string) string {
	modelName = RelayModelName(modelName)
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
	return uc.modelMapper.HasCapability(RelayModelName(modelName), capability)
}
