package data

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	channelv1 "micro-one-api/api/channel/v1"
	commonv1 "micro-one-api/api/common/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/routing"
	relaycredential "micro-one-api/domain/upstream/credential"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/routingclient"
	"micro-one-api/platform/routingdto"
)

// IdentityAdapter wraps a gRPC IdentityServiceClient to implement biz.IdentityClient.
type IdentityAdapter struct {
	client       identityv1.IdentityServiceClient
	quotaMu      sync.RWMutex
	quotaBlocked map[int64]time.Time
}

// NewIdentityAdapter creates a new IdentityAdapter.
func NewIdentityAdapter(client identityv1.IdentityServiceClient) *IdentityAdapter {
	return &IdentityAdapter{client: client, quotaBlocked: make(map[int64]time.Time)}
}

func (a *IdentityAdapter) GetAuthSnapshot(ctx context.Context, token, clientIP string) (*relaybiz.AuthSnapshot, error) {
	reply, err := a.client.GetAuthSnapshot(ctx, &identityv1.GetAuthSnapshotRequest{Token: token, ClientIp: clientIP})
	if err != nil {
		return nil, err
	}
	if a.IsTokenQuotaBlocked(reply.GetTokenId()) {
		return nil, errors.New("token quota temporarily unavailable")
	}
	return &relaybiz.AuthSnapshot{
		RoutingFacts:          routingdto.FactsFromProto(reply.RoutingFacts),
		RoutingContextVersion: reply.RoutingContextVersion,
		UserID:                reply.UserId,
		TokenID:               reply.TokenId,
		TokenName:             reply.TokenName,
		Group:                 reply.Group,
		AllowedModels:         reply.AllowedModels,
		UserEnabled:           reply.UserEnabled,
		TokenEnabled:          reply.TokenEnabled,
	}, nil
}

func (a *IdentityAdapter) BlockTokenQuota(tokenID int64, duration time.Duration) {
	if a == nil || tokenID <= 0 {
		return
	}
	a.quotaMu.Lock()
	if a.quotaBlocked == nil {
		a.quotaBlocked = make(map[int64]time.Time)
	}
	a.quotaBlocked[tokenID] = time.Now().Add(duration)
	a.quotaMu.Unlock()
}

func (a *IdentityAdapter) ClearTokenQuotaBlock(tokenID int64) {
	if a == nil || tokenID <= 0 {
		return
	}
	a.quotaMu.Lock()
	delete(a.quotaBlocked, tokenID)
	a.quotaMu.Unlock()
}

func (a *IdentityAdapter) IsTokenQuotaBlocked(tokenID int64) bool {
	if a == nil || tokenID <= 0 {
		return false
	}
	now := time.Now()
	a.quotaMu.Lock()
	until, ok := a.quotaBlocked[tokenID]
	if ok && !until.After(now) {
		delete(a.quotaBlocked, tokenID)
		ok = false
	}
	a.quotaMu.Unlock()
	return ok
}

// splitModels splits a comma-separated model string into a slice.
func splitModels(models string) []string {
	if models == "" {
		return nil
	}
	parts := strings.Split(models, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ChannelAdapter wraps a gRPC ChannelServiceClient to implement biz.ChannelClient.
type ChannelAdapter struct {
	client channelv1.ChannelServiceClient
	// modelHealth moves the passive RecordModelHealth RPC off the request
	// goroutine; see modelHealthQueue. Other methods stay synchronous.
	modelHealth *modelHealthQueue
}

func (a *ChannelAdapter) GetRoutingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	return routingclient.New(a.client).GetRoutingGroup(ctx, id)
}

// HasRoutingCandidates is the read-only ordered-routing probe; it never
// advances weighted-scheduler state.
func (a *ChannelAdapter) HasRoutingCandidates(ctx context.Context, groupID int64, model string) (bool, error) {
	reply, err := a.client.HasRoutingCandidates(ctx, &channelv1.HasRoutingCandidatesRequest{RoutingGroupId: groupID, Model: model})
	if err != nil {
		return false, err
	}
	return reply.GetHasCandidates(), nil
}

// NewChannelAdapter creates a new ChannelAdapter.
func NewChannelAdapter(client channelv1.ChannelServiceClient) *ChannelAdapter {
	return &ChannelAdapter{client: client, modelHealth: newModelHealthQueue(client)}
}

func (a *ChannelAdapter) Resolve(ctx context.Context, channelID int64) (*relaycredential.SubscriptionAccountMetadata, error) {
	return NewChannelSubscriptionAccountStore(a.client).Resolve(ctx, channelID)
}

func (a *ChannelAdapter) SelectSubscriptionAccount(ctx context.Context, group, model, platform string, excludeFirstPriority bool) (*relaybiz.SubscriptionAccount, error) {
	reply, err := a.client.SelectSubscriptionAccount(ctx, &channelv1.SelectSubscriptionAccountRequest{
		Group:                group,
		Model:                model,
		Platform:             platform,
		ExcludeFirstPriority: excludeFirstPriority,
	})
	if err != nil {
		return nil, err
	}
	return subscriptionAccountInfoToBiz(reply.GetAccount()), nil
}

// SelectSubscriptionAccountExcluding passes the request-scoped failed-account
// set down to channel-service so per-candidate filtering happens server-side
// (sub2api #2), instead of the relay looping over excludeFirstPriority tiers.
func (a *ChannelAdapter) SelectSubscriptionAccountExcluding(ctx context.Context, group, model, platform string, excluded map[int64]bool) (*relaybiz.SubscriptionAccount, error) {
	reply, err := a.client.SelectSubscriptionAccount(ctx, &channelv1.SelectSubscriptionAccountRequest{
		Group:              group,
		Model:              model,
		Platform:           platform,
		ExcludedAccountIds: sortedExcludedIDs(excluded),
	})
	if err != nil {
		return nil, err
	}
	return subscriptionAccountInfoToBiz(reply.GetAccount()), nil
}

// sortedExcludedIDs flattens an exclusion set into a deterministic slice.
func sortedExcludedIDs(excluded map[int64]bool) []int64 {
	if len(excluded) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(excluded))
	for id, blocked := range excluded {
		if blocked && id > 0 {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// GetSubscriptionAccountByID materializes a single subscription account (with
// secrets) by id for session-stickiness reuse. Returns (nil, nil) when the id
// is unknown. It reuses the WithSecrets-preferred by-id RPC shared with the
// credential/resolver path (see ChannelSubscriptionAccountStore).
func (a *ChannelAdapter) GetSubscriptionAccountByID(ctx context.Context, accountID int64) (*relaybiz.SubscriptionAccount, error) {
	reply, err := NewChannelSubscriptionAccountStore(a.client).getSubscriptionAccount(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return subscriptionAccountInfoToBiz(reply.GetAccount()), nil
}

// subscriptionAccountInfoToBiz maps the shared proto account message into the
// relay biz account. Returns nil for a nil info so callers surface a clean miss.
func subscriptionAccountInfoToBiz(account *commonv1.SubscriptionAccountInfo) *relaybiz.SubscriptionAccount {
	if account == nil {
		return nil
	}
	return &relaybiz.SubscriptionAccount{
		ID:                    account.GetId(),
		Name:                  account.GetName(),
		Platform:              account.GetPlatform(),
		AccountType:           account.GetAccountType(),
		Status:                account.GetStatus(),
		BaseURL:               account.GetBaseUrl(),
		Group:                 account.GetGroup(),
		Models:                splitModels(account.GetModels()),
		Priority:              account.GetPriority(),
		Weight:                account.GetWeight(),
		AccessToken:           account.GetAccessToken(),
		AccountID:             account.GetAccountId(),
		Fingerprint:           account.GetFingerprint(),
		Concurrency:           account.GetConcurrency(),
		RPMLimit:              account.GetRpmLimit(),
		SessionWindowLimitUSD: account.GetSessionWindowLimitUsd(),
		ModelMapping:          account.GetModelMapping(),
		UpstreamModelID:       account.GetUpstreamModelId(),
	}
}

func (a *ChannelAdapter) SelectChannel(ctx context.Context, group, model string, excludeFirstPriority bool) (*relaybiz.Channel, error) {
	reply, err := a.client.SelectChannel(ctx, &channelv1.SelectChannelRequest{
		Group:                group,
		Model:                model,
		ExcludeFirstPriority: excludeFirstPriority,
	})
	if err != nil {
		return nil, err
	}
	return channelInfoToRelayChannel(reply.Channel), nil
}

// SelectChannelExcluding passes the request-scoped failed-channel set through
// to the channel service, which filters candidates individually so failover
// can reach healthy channels in any tier.
func (a *ChannelAdapter) SelectChannelExcluding(ctx context.Context, group, model string, excluded map[int64]bool) (*relaybiz.Channel, error) {
	ids := make([]int64, 0, len(excluded))
	for id, blocked := range excluded {
		if blocked {
			ids = append(ids, id)
		}
	}
	reply, err := a.client.SelectChannel(ctx, &channelv1.SelectChannelRequest{
		Group:              group,
		Model:              model,
		ExcludedChannelIds: ids,
	})
	if err != nil {
		return nil, err
	}
	return channelInfoToRelayChannel(reply.Channel), nil
}

func channelInfoToRelayChannel(ch *commonv1.ChannelInfo) *relaybiz.Channel {
	if ch == nil {
		return nil
	}
	relayChannel := &relaybiz.Channel{
		ID:              ch.Id,
		Type:            ch.Type,
		Name:            ch.Name,
		Status:          ch.Status,
		BaseURL:         ch.BaseUrl,
		Group:           ch.Group,
		Models:          splitModels(ch.Models),
		Priority:        ch.Priority,
		Weight:          ch.Weight,
		Key:             ch.Key,
		ModelMapping:    ch.GetModelMapping(),
		UpstreamModelID: ch.GetUpstreamModelId(),
		RestrictModels:  ch.GetRestrictModels(),
	}
	if ch.Config != nil {
		relayChannel.Config.APIVersion = ch.Config.ApiVersion
	}
	return relayChannel
}

func (a *ChannelAdapter) RecordChannelHealth(ctx context.Context, channelID int64, success bool, message string, responseTime int64) error {
	reply, err := a.client.RecordChannelHealth(ctx, &channelv1.RecordChannelHealthRequest{
		ChannelId:    channelID,
		Success:      success,
		Error:        message,
		ResponseTime: responseTime,
	})
	if err != nil {
		return err
	}
	if reply != nil && !reply.GetSuccess() {
		return errors.New(reply.GetMessage())
	}
	return nil
}

// RecordModelHealth enqueues a passive model-health sample for background
// submission. It returns nil on acceptance; the caller treats the recording as
// fire-and-forget anyway (best-effort telemetry), and queue-full drops are
// counted rather than surfaced as relay-path errors. Use FlushModelHealth /
// Close when deterministic submission is required.
func (a *ChannelAdapter) RecordModelHealth(_ context.Context, sourceKind string, sourceID int64, modelID, upstreamModelID string, success bool, message string, responseTime int64) error {
	if a == nil || a.client == nil {
		return nil
	}
	if a.modelHealth == nil {
		a.modelHealth = newModelHealthQueue(a.client)
	}
	a.modelHealth.record(&channelv1.RecordModelHealthRequest{
		SourceKind: sourceKind, SourceId: sourceID, ModelId: modelID,
		UpstreamModelId: upstreamModelID, Success: success, Error: message,
		ResponseTime: responseTime,
	})
	return nil
}

// FlushModelHealth blocks until every model-health sample enqueued so far has
// been submitted to the channel service.
func (a *ChannelAdapter) FlushModelHealth(ctx context.Context) error {
	if a == nil || a.modelHealth == nil {
		return nil
	}
	return a.modelHealth.flush(ctx)
}

// ModelHealthStats reports submitted / dropped / failed sample counts.
func (a *ChannelAdapter) ModelHealthStats() (submitted, dropped, failed int64) {
	if a == nil || a.modelHealth == nil {
		return 0, 0, 0
	}
	return a.modelHealth.stats()
}

// Close stops accepting model-health samples and drains the queue.
func (a *ChannelAdapter) Close() error {
	a.modelHealth.close(3 * time.Second)
	return nil
}

func (a *ChannelAdapter) RecordSubscriptionAccountHealth(ctx context.Context, accountID int64, success bool) error {
	if accountID <= 0 {
		return nil
	}
	reply, err := a.client.RecordSubscriptionAccountHealth(ctx, &channelv1.RecordSubscriptionAccountHealthRequest{
		AccountId: accountID,
		Success:   success,
	})
	if err != nil {
		return err
	}
	if reply != nil && !reply.GetSuccess() {
		return errors.New(reply.GetMessage())
	}
	return nil
}

// RecordSubscriptionAccountSlot forwards a relay-local slot acquire/release to
// the channel selector's per-process inflight counter (weight loop closure).
func (a *ChannelAdapter) RecordSubscriptionAccountSlot(ctx context.Context, accountID int64, acquired bool) error {
	if accountID <= 0 {
		return nil
	}
	reply, err := a.client.RecordSubscriptionAccountSlot(ctx, &channelv1.RecordSubscriptionAccountSlotRequest{
		AccountId: accountID,
		Acquired:  acquired,
	})
	if err != nil {
		return err
	}
	if reply != nil && !reply.GetSuccess() {
		return errors.New(reply.GetMessage())
	}
	return nil
}
