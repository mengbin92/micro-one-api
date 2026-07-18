// Package testutil re-exports the channel business types, interfaces,
// constructors and sentinel errors needed by cross-service integration
// tests. These symbols live under app/channel/internal and are
// normally invisible outside that subtree.
package testutil

import (
	channelbiz "micro-one-api/app/channel/internal/biz"
	channelservice "micro-one-api/app/channel/internal/service"
)

// Type aliases for entities.
type (
	Channel                           = channelbiz.Channel
	Ability                           = channelbiz.Ability
	SubscriptionAccount               = channelbiz.SubscriptionAccount
	SubscriptionAccountAbility        = channelbiz.SubscriptionAccountAbility
	AccountQuotaSnapshot              = channelbiz.AccountQuotaSnapshot
	SubscriptionAccountQuotaUsage     = channelbiz.SubscriptionAccountQuotaUsage
	SubscriptionAccountQuotaResetRun  = channelbiz.SubscriptionAccountQuotaResetRun
	ChannelHealthEvent                = channelbiz.ChannelHealthEvent
	ChannelRepo                       = channelbiz.ChannelRepo
	ChannelUsecase                    = channelbiz.ChannelUsecase
	ChannelStats                          = channelbiz.ChannelStats
	SubscriptionAccountQuotaEventFilter    = channelbiz.SubscriptionAccountQuotaEventFilter
	SubscriptionAccountQuotaEventAggregate = channelbiz.SubscriptionAccountQuotaEventAggregate
)

// Sentinel errors.
var (
	ErrChannelNotFound             = channelbiz.ErrChannelNotFound
	ErrSubscriptionAccountNotFound = channelbiz.ErrSubscriptionAccountNotFound
	ErrQuotaResetRunDuplicate      = channelbiz.ErrQuotaResetRunDuplicate
)

// Constants.
const (
	ChannelStatusEnabled     = channelbiz.ChannelStatusEnabled
	ChannelHealthHealthy     = channelbiz.ChannelHealthHealthy
	ChannelHealthDegraded    = channelbiz.ChannelHealthDegraded
	ChannelHealthUnavailable = channelbiz.ChannelHealthUnavailable
)

// NewChannelUsecase re-exports the constructor.
func NewChannelUsecase(repo ChannelRepo, eventBus any) *ChannelUsecase {
	return channelbiz.NewChannelUsecase(repo, nil)
}

// NewChannelService re-exports the service constructor.
func NewChannelService(uc *ChannelUsecase) *channelservice.ChannelService {
	return channelservice.NewChannelService(uc)
}

// SelectorStats re-exports the usecase's weighted-selector runtime stats
// accessor so cross-service integration tests can confirm that channel
// selection actually flows through WeightedSelector (inflight / error
// rate / p95 latency / weight populated) instead of bypassing it.
func SelectorStats(uc *ChannelUsecase) map[int64]ChannelStats {
	return uc.SelectorStats()
}
