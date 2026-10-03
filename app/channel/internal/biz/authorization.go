package biz

import (
	"context"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/security/serviceidentity"
)

// ChannelAuthorizationRepo supplies storage-owned group IDs. It does not
// accept request-supplied object facts as an authorization source.
type ChannelAuthorizationRepo interface {
	ChannelAuthorizationFacts(context.Context, int64) (authorization.ObjectFacts, error)
	SubscriptionAccountAuthorizationFacts(context.Context, int64) (authorization.ObjectFacts, error)
	RoutingGroupAuthorizationIDs(context.Context, string) ([]int64, error)
}

func (uc *ChannelUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }
func (uc *ChannelUsecase) authorize(ctx context.Context, point, op string) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.Prepare(ctx, uc.authorization, point, op)
}
func (uc *ChannelUsecase) authorizeOptional(ctx context.Context, point, op string) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.PrepareOptional(ctx, uc.authorization, point, op)
}
func (uc *ChannelUsecase) facts(ctx context.Context, id int64, account bool) (authorization.ObjectFacts, error) {
	repo, ok := uc.repo.(ChannelAuthorizationRepo)
	if !ok {
		return authorization.ObjectFacts{}, authorization.ErrDenied
	}
	if account {
		return repo.SubscriptionAccountAuthorizationFacts(ctx, id)
	}
	return repo.ChannelAuthorizationFacts(ctx, id)
}
func (uc *ChannelUsecase) authorizeObject(ctx context.Context, point, op string, id int64, account bool) (context.Context, error) {
	ctx, err := uc.authorize(ctx, point, op)
	if err != nil {
		return ctx, err
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, op); !iam {
		return ctx, nil
	}
	facts, err := uc.facts(ctx, id, account)
	if err != nil {
		return ctx, err
	}
	return ctx, authorization.Require(ctx, op, facts)
}
func (uc *ChannelUsecase) authorizeCreate(ctx context.Context, point, op, group string) (context.Context, error) {
	ctx, err := uc.authorize(ctx, point, op)
	if err != nil {
		return ctx, err
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, op); !iam {
		return ctx, nil
	}
	repo, ok := uc.repo.(ChannelAuthorizationRepo)
	if !ok {
		return ctx, authorization.ErrDenied
	}
	ids, err := repo.RoutingGroupAuthorizationIDs(ctx, group)
	if err != nil {
		return ctx, err
	}
	return ctx, authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform(), RoutingGroupIDs: ids})
}
func (uc *ChannelUsecase) ReadChannel(ctx context.Context, id int64) (*Channel, error) {
	channel, err := uc.GetChannel(ctx, id)
	if err != nil {
		return nil, err
	}
	ctx, err = uc.authorizeOptional(ctx, "channel.channels.secret", "channel.channel.secret.read")
	if err != nil {
		return nil, err
	}
	ctx, err = uc.authorizeOptional(ctx, "channel.health", "monitor.health.channel.read")
	if err != nil {
		return nil, err
	}
	return uc.redactChannel(ctx, channel)
}
func (uc *ChannelUsecase) redactChannel(ctx context.Context, channel *Channel) (*Channel, error) {
	copy := *channel
	copy.HealthFieldsVisible = true
	if _, iam := authorization.QueryScopeFromContext(ctx, "channel.channel.secret.read"); !iam {
		return &copy, nil
	}
	copy.ModelMapping = ""
	facts, err := uc.facts(ctx, channel.ID, false)
	if err != nil {
		return nil, err
	}
	if authorization.Require(ctx, "channel.channel.secret.read", facts) != nil {
		copy.Key = ""
	}
	copy.HealthFieldsVisible = authorization.Require(ctx, "monitor.health.channel.read", facts) == nil
	if !copy.HealthFieldsVisible {
		copy.TestTime, copy.ResponseTime = 0, 0
		copy.HealthStatus, copy.HealthLastError = "", ""
		copy.HealthLastSuccessTime, copy.HealthLastFailureTime = 0, 0
		copy.HealthConsecutiveFailures, copy.CircuitOpenedUntil = 0, 0
	}
	return &copy, nil
}

func redactAccountMappings(ctx context.Context, rows []*SubscriptionAccount) []*SubscriptionAccount {
	if _, iam := authorization.QueryScopeFromContext(ctx, "channel.account.list"); !iam {
		return rows
	}
	for i, account := range rows {
		if account == nil {
			continue
		}
		copy := *account
		copy.ModelMapping = ""
		rows[i] = &copy
	}
	return rows
}

// GuardUnfinishedHTTP closes legacy direct HTTP entry points until the full
// resource implementation is bound. The owner still probes live IAM mode.
func (uc *ChannelUsecase) GuardUnfinishedHTTP(ctx context.Context, point, op string) error {
	_, err := uc.authorize(ctx, point, op)
	return err
}
func SubscriptionAccountPublicMetadata(raw string) string {
	var values map[string]any
	if jsonx.Unmarshal([]byte(raw), &values) != nil {
		return ""
	}
	view := map[string]any{}
	for _, key := range []string{"last_error", "recovery_policy", "recovery_reason", "recovery_until", "last_quota_alert_kind", "last_quota_alert_at"} {
		if value, ok := values[key]; ok {
			view[key] = value
		}
	}
	if len(view) == 0 {
		return ""
	}
	result, _ := jsonx.Marshal(view)
	return string(result)
}

func (uc *ChannelUsecase) ChannelForUpdate(ctx context.Context, id int64) (*Channel, error) {
	ctx, err := uc.authorizeObject(ctx, "channel.channels.update", "channel.channel.update", id, false)
	if err != nil {
		return nil, err
	}
	return uc.repo.FindByID(ctx, id)
}
func (uc *ChannelUsecase) SubscriptionAccountForUpdate(ctx context.Context, id int64) (*SubscriptionAccount, error) {
	ctx, err := uc.authorizeObject(ctx, "channel.accounts.update", "channel.account.update", id, true)
	if err != nil {
		return nil, err
	}
	return uc.repo.FindSubscriptionAccountByID(ctx, id)
}

// AuthorizeOAuth binds the staged credential exchange to the target group's
// authoritative facts. Exchange calls it again before contacting the provider.
func (uc *ChannelUsecase) AuthorizeOAuth(ctx context.Context, group string) (context.Context, error) {
	if group == "" {
		group = "default"
	}
	return uc.authorizeCreate(ctx, "channel.accounts.oauth", "channel.account.oauth.bind", group)
}

func (uc *ChannelUsecase) ReadSelectorStats(ctx context.Context) (map[int64]ChannelStats, error) {
	ctx, err := uc.authorize(ctx, "channel.health", "monitor.health.selector.read")
	if err != nil {
		return nil, err
	}
	out := map[int64]ChannelStats{}
	for id, stats := range uc.SelectorStats() {
		if _, scoped := authorization.QueryScopeFromContext(ctx, "monitor.health.selector.read"); scoped {
			facts, err := uc.facts(ctx, id, false)
			if err != nil {
				return nil, err
			}
			if authorization.Require(ctx, "monitor.health.selector.read", facts) != nil {
				continue
			}
		}
		out[id] = stats
	}
	return out, nil
}
