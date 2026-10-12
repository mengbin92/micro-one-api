package biz

import (
	"context"
	"time"

	"github.com/google/uuid"
	"micro-one-api/domain/upstream/provider"
)

// ClaimUpstreamExecution applies owner controls even when a route was cached or pinned.
// CanRoute remains a read-only authorization check for catalogues and audits.
func (uc *ChannelUsecase) ClaimUpstreamExecution(ctx context.Context, claim provider.ExecutionClaim) (provider.ExecutionLease, error) {
	uc.runtimeControlMu.Lock()
	defer uc.runtimeControlMu.Unlock()
	switch claim.SourceKind {
	case "channel":
		s := uc.selector
		s.mu.Lock()
		defer s.mu.Unlock()
		channel, err := uc.repo.FindByID(ctx, claim.SourceID)
		if err != nil {
			return provider.ExecutionLease{}, err
		}
		if channel == nil || claim.ConfigurationRevision != channel.UpstreamRevision() || !channel.SelectableAt(uc.now()) {
			return provider.ExecutionLease{}, ErrChannelNotFound
		}
		s.updateChannelLocked(channel)
		state := s.channels[channel.ID]
		return claimUpstreamExecution(&state.control, claim, state.breakerState(time.Now().UnixNano()), &state.circuitOpenUntil, &state.circuitProbeUntil, ErrChannelNotFound)
	case "subscription":
		s := uc.accountSelector
		s.mu.Lock()
		defer s.mu.Unlock()
		account, err := uc.repo.FindSubscriptionAccountByID(ctx, claim.SourceID)
		if err != nil {
			return provider.ExecutionLease{}, err
		}
		if account == nil || claim.ConfigurationRevision != account.UpstreamRevision() || account.Status != ChannelStatusEnabled || !account.IsSchedulableAt(uc.now()) {
			return provider.ExecutionLease{}, ErrSubscriptionAccountNotFound
		}
		state := s.updateAccountLocked(account)
		return claimUpstreamExecution(&state.control, claim, state.breakerState(time.Now().UnixNano()), &state.circuitOpenUntil, &state.circuitProbeUntil, ErrSubscriptionAccountNotFound)
	default:
		return provider.ExecutionLease{}, ErrChannelNotFound
	}
}

// The caller holds the source selector lock through validation and lease creation.
func claimUpstreamExecution(control *upstreamControl, claim provider.ExecutionClaim, circuit circuitState, openUntil, probeUntil *int64, unavailable error) (provider.ExecutionLease, error) {
	now := time.Now().UnixNano()
	if claim.ConfigurationRevision != control.revision || control.blocked(claim.UpstreamModelID, now) || circuit == circuitOpen {
		return provider.ExecutionLease{}, unavailable
	}
	if control.capacityUntil > now {
		return provider.ExecutionLease{}, ErrUpstreamRateLimit
	}
	lease := provider.ExecutionLease{ConfigurationRevision: control.revision}
	if circuit == circuitHalfOpen {
		if *probeUntil > now {
			if control.probeID == "" || claim.ProbeID != control.probeID {
				return provider.ExecutionLease{}, unavailable
			}
		} else {
			*openUntil = circuitHalfOpenSentinel
			*probeUntil = now + circuitBreakerProbeLeaseDuration.Nanoseconds()
			control.probeID = uuid.NewString()
		}
		lease.ProbeID = control.probeID
	}
	return lease, nil
}
