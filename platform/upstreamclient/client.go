// Package upstreamclient converts internal upstream control objects at the RPC boundary.
package upstreamclient

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/upstream/provider"
	"micro-one-api/platform/contractdto"
)

func ReportObservation(ctx context.Context, client channelv1.ChannelServiceClient, o provider.Observation) error {
	_, err := client.RecordUpstreamObservation(ctx, &channelv1.RecordUpstreamObservationRequest{
		SourceKind: o.SourceKind, SourceId: o.SourceID, ConfigurationRevision: o.ConfigurationRevision,
		ProbeId: o.ProbeID, ObservationId: o.ID, StartedAtUnixNano: o.StartedAt.UnixNano(),
		Outcome: int32(o.Outcome), FailureKind: o.FailureKind, UpstreamModelId: o.UpstreamModelID,
		ResponseTime: o.ResponseTime, RetryAfterMs: o.RetryAfter.Milliseconds(), Error: o.Error,
	})
	return err
}

func ClaimExecution(ctx context.Context, client channelv1.ChannelServiceClient, claim provider.ExecutionClaim) (provider.ExecutionLease, error) {
	reply, err := client.ClaimUpstreamExecution(ctx, &channelv1.ClaimUpstreamExecutionRequest{
		RuntimeBuild: claim.RuntimeBuild, AttemptId: claim.AttemptID, SourceKind: claim.SourceKind, SourceId: claim.SourceID, ConfigurationRevision: claim.ConfigurationRevision, UpstreamModelId: claim.UpstreamModelID, ProbeId: claim.ProbeID,
	})
	if err != nil {
		if status.Code(err) == codes.ResourceExhausted {
			return provider.ExecutionLease{}, &provider.ExecutionCapacityError{Err: err}
		}
		return provider.ExecutionLease{}, err
	}
	lease := provider.ExecutionLease{ConfigurationRevision: reply.GetConfigurationRevision(), ProbeID: reply.GetProbeId(), BlockedProtocols: reply.GetBlockedProtocols(), ExecutionRevision: reply.GetExecutionRevision()}
	for _, p := range reply.GetContracts() {
		s, err := contractdto.Snapshot(p)
		if err != nil {
			return provider.ExecutionLease{}, err
		}
		lease.Contracts = append(lease.Contracts, s)
	}
	return lease, nil
}
