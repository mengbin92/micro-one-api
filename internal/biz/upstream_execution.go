package biz

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"micro-one-api/domain/upstream/contract"
	"os"

	"micro-one-api/domain/upstream/provider"
)

type UpstreamExecutionClaimer interface {
	ClaimUpstreamExecution(context.Context, provider.ExecutionClaim) (provider.ExecutionLease, error)
}

// AdmitUpstreamExecution fences cached projections and keeps probe leases request-local.
func (uc *RelayUsecase) AdmitUpstreamExecution(ctx context.Context, ch *Channel) (*Channel, error) {
	if ch == nil {
		return nil, fmt.Errorf("upstream source unavailable")
	}
	admitted := *ch
	if uc == nil || ch.UpstreamConfigurationRevision == "" {
		return &admitted, nil
	}
	claimer, ok := uc.channel.(UpstreamExecutionClaimer)
	if !ok {
		return &admitted, nil
	}
	claim := provider.ExecutionClaim{SourceKind: UpstreamSourceChannel, SourceID: ch.ID, ConfigurationRevision: ch.UpstreamConfigurationRevision, UpstreamModelID: ch.UpstreamModelID, ProbeID: ch.HealthProbeID}
	if os.Getenv("UPSTREAM_CONTRACTS_V1_ENABLED") == "true" {
		claim.AttemptID = uuid.NewString()
		claim.RuntimeBuild = contract.BuildID
	}
	if ch.SubscriptionAccountID > 0 {
		claim.SourceKind, claim.SourceID = UpstreamSourceSubscription, ch.SubscriptionAccountID
	}
	lease, err := claimer.ClaimUpstreamExecution(ctx, claim)
	if err == nil && lease.ConfigurationRevision != ch.UpstreamConfigurationRevision {
		err = fmt.Errorf("upstream configuration changed; select a fresh source")
	}
	if err != nil {
		retryable := true
		code := 503
		var capacity *provider.ExecutionCapacityError
		if errors.As(err, &capacity) {
			code = 429
		}
		return nil, &RetryableError{Status: code, Err: err, Retryable: &retryable, NoUpstreamAttempt: true}
	}
	admitted.HealthProbeID = lease.ProbeID
	admitted.Contracts, admitted.ContractBlocked, admitted.ContractExecutionRevision = lease.Contracts, lease.BlockedProtocols, lease.ExecutionRevision
	return &admitted, nil
}
