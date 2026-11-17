package service

import (
	"context"
	kerrors "github.com/go-kratos/kratos/v3/errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/upstream/evidence"
	relaybiz "micro-one-api/internal/biz"
)

// usageObservationSender adapts the Log owner RPC to the relay observation
// sender contract. It never retries by itself: the outbox owns retry policy.
type usageObservationSender struct {
	client logv1.LogServiceClient
}

func NewUsageObservationSender(client logv1.LogServiceClient) relaybiz.UsageObservationSender {
	return &usageObservationSender{client: client}
}

func (s *usageObservationSender) Send(ctx context.Context, e evidence.SendEvent) (evidence.DeliveryReceipt, error) {
	payload, digest, err := e.Encode()
	if err != nil {
		return evidence.DeliveryReceipt{}, err
	}
	resp, err := s.client.RecordUsageObservation(ctx, &logv1.RecordUsageObservationRequest{
		EventId:       e.ID,
		SendId:        e.Send.SendID,
		ObservationId: e.Send.ObservationID,
		Category:      e.Category,
		Sequence:      e.Sequence,
		PayloadSha256: digest,
		Payload:       payload,
	})
	if err != nil {
		reason := kerrors.FromError(err).Reason
		if status.Code(err) == codes.AlreadyExists || reason == logv1.UsageObservationErrorReason_USAGE_OBSERVATION_CONFLICT.String() {
			return evidence.DeliveryReceipt{Digest: digest}, relaybiz.ErrObservationConflict
		}
		if reason == logv1.UsageObservationErrorReason_USAGE_OBSERVATION_UNAVAILABLE.String() {
			return evidence.DeliveryReceipt{Digest: digest}, relaybiz.ErrObservationStorageUnavailable
		}
		return evidence.DeliveryReceipt{Digest: digest}, err
	}
	if resp == nil {
		return evidence.DeliveryReceipt{Digest: digest}, relaybiz.ErrObservationReceipt
	}
	return evidence.DeliveryReceipt{Accepted: resp.Accepted, Duplicate: resp.Duplicate, Status: resp.Status, Digest: resp.PayloadSha256}, nil
}
