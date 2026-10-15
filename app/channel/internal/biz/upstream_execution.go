package biz

import (
	"context"
	"micro-one-api/domain/upstream/contract"
	"os"
	"time"

	"github.com/google/uuid"
	"micro-one-api/domain/upstream/provider"
)

// ClaimUpstreamExecution applies owner controls even when a route was cached or pinned.
// CanRoute remains a read-only authorization check for catalogues and audits.
func (uc *ChannelUsecase) ClaimUpstreamExecution(ctx context.Context, claim provider.ExecutionClaim) (provider.ExecutionLease, error) {
	var contracts provider.ExecutionLease
	if r, ok := uc.repo.(interface {
		RequiresContractRuntime(context.Context, string, int64, string) (bool, error)
	}); ok {
		required, err := r.RequiresContractRuntime(ctx, claim.SourceKind, claim.SourceID, claim.UpstreamModelID)
		if err != nil {
			return contracts, err
		}
		if required && (os.Getenv("UPSTREAM_CONTRACTS_V1_ENABLED") != "true" || claim.RuntimeBuild != contract.BuildID) {
			return contracts, ErrContractUnavailable
		}
	}

	if os.Getenv("UPSTREAM_CONTRACTS_V1_ENABLED") == "true" {
		r, err := uc.contracts()
		if err != nil {
			return contracts, err
		}
		contracts.Contracts, contracts.BlockedProtocols, contracts.ExecutionRevision, err = r.ResolveContracts(ctx, claim.SourceKind, claim.SourceID, claim.UpstreamModelID, claim.AttemptID)
		if err != nil {
			return contracts, err
		}
	}
	wrap := func(lease provider.ExecutionLease, err error) (provider.ExecutionLease, error) {
		lease.Contracts = contracts.Contracts
		lease.BlockedProtocols = contracts.BlockedProtocols
		lease.ExecutionRevision = provider.ConfigurationRevision(lease.ConfigurationRevision, contracts.ExecutionRevision, claim.RuntimeBuild)
		return lease, err
	}
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
		return wrap(claimUpstreamExecution(&state.control, claim, state.breakerState(time.Now().UnixNano()), &state.circuitOpenUntil, &state.circuitProbeUntil, ErrChannelNotFound))
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
		return wrap(claimUpstreamExecution(&state.control, claim, state.breakerState(time.Now().UnixNano()), &state.circuitOpenUntil, &state.circuitProbeUntil, ErrSubscriptionAccountNotFound))
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
