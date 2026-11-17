package service

import (
	"context"
	"strconv"

	"google.golang.org/protobuf/proto"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
)

// maxUsageObservationPageBytes keeps one list reply well below the gRPC receive
// limit even when every summary row carries its largest bounded metadata.
const maxUsageObservationPageBytes = 3 << 20

func (s *LogService) SetUsageObservationUsecase(uc *biz.UsageObservationUsecase) {
	s.observationUC = uc
}

// RecordUsageObservation is the Relay-only observation writer. It cannot
// settle funds and it never writes a consumption log row.
func (s *LogService) RecordUsageObservation(ctx context.Context, req *logv1.RecordUsageObservationRequest) (*logv1.RecordUsageObservationResponse, error) {
	if req == nil || s.observationUC == nil {
		return nil, biz.ErrInvalidUsageObservation
	}
	receipt, err := s.observationUC.Record(ctx, req.EventId, req.SendId, req.ObservationId, req.Category, req.PayloadSha256, req.Sequence, req.Payload)
	if err != nil {
		return nil, err
	}
	return &logv1.RecordUsageObservationResponse{
		Accepted:      receipt.Accepted,
		Duplicate:     receipt.Duplicate,
		Status:        receipt.Status,
		PayloadSha256: receipt.Digest,
	}, nil
}

// ListUsageObservations is the restricted evidence summary query. Payloads are
// fetched one event at a time through GetUsageObservation so a page can never
// exceed the client's receive limit.
func (s *LogService) ListUsageObservations(ctx context.Context, req *logv1.ListUsageObservationsRequest) (*logv1.ListUsageObservationsResponse, error) {
	if req == nil || s.observationUC == nil {
		return nil, biz.ErrInvalidUsageObservation
	}
	filter := biz.UsageObservationFilter{
		StartTimeMS: req.StartTimeMs, EndTimeMS: req.EndTimeMs, ObservationID: req.ObservationId,
		SendID: req.SendId, Capture: req.Capture, Status: req.Status, Category: req.Category,
		PageSize: req.PageSize, PageToken: req.PageToken,
	}
	if req.UserId != nil {
		filter.UserID = req.GetUserId()
	}
	rows, next, err := s.observationUC.ListUsageObservations(ctx, filter)
	if err != nil {
		return nil, err
	}
	reply := &logv1.ListUsageObservationsResponse{}
	size := 0
	for _, row := range rows {
		item := usageObservationToProto(row)
		// Summaries never carry payload bytes; GetUsageObservation fetches one.
		item.Payload = nil
		itemSize := proto.Size(item)
		if len(reply.Items) > 0 && size+itemSize > maxUsageObservationPageBytes {
			// Stop before exceeding the budget. The cursor is recomputed from the
			// last item actually returned, never from the prefetched row.
			next = pageTokenFor(reply.Items[len(reply.Items)-1])
			break
		}
		reply.Items = append(reply.Items, item)
		size += itemSize
	}
	reply.NextPageToken = next
	return reply, nil
}

// GetUsageObservation returns one event including its canonical payload.
func (s *LogService) GetUsageObservation(ctx context.Context, req *logv1.GetUsageObservationRequest) (*logv1.UsageObservationItem, error) {
	if req == nil || s.observationUC == nil {
		return nil, biz.ErrInvalidUsageObservation
	}
	row, err := s.observationUC.GetUsageObservation(ctx, req.EventId)
	if err != nil {
		return nil, err
	}
	return usageObservationToProto(row), nil
}

func pageTokenFor(item *logv1.UsageObservationItem) string {
	if item == nil {
		return ""
	}
	return strconv.FormatInt(item.ObservedAtMs, 10) + ":" + item.EventId
}

func usageObservationToProto(row *biz.UsageObservationRow) *logv1.UsageObservationItem {
	item := &logv1.UsageObservationItem{
		EventId: row.EventID, SendId: row.SendID, ObservationId: row.ObservationID, Category: row.Category,
		Sequence: row.Sequence, ObservedAtMs: row.ObservedAtMS, Capture: row.Capture, Status: row.Status,
		Protocol: row.Protocol, SourceKind: row.SourceKind, SourceId: row.SourceID, UpstreamModelId: row.UpstreamModelID,
		Purpose: row.Purpose, UserId: row.UserID, HttpStatus: row.HTTPStatus, Stream: row.Stream,
		ParserRevision: row.ParserRevision, ContractRevision: row.ContractRevision, ReceivedAtMs: row.ReceivedAtMS,
		PayloadSha256: row.PayloadSHA256, Payload: row.Payload, AttemptRequestId: row.AttemptRequestID,
		RootRequestId: row.RootRequestID, BuildRevision: row.BuildRevision, CaptureReason: row.CaptureReason,
	}
	var envelope evidence.SendEvent
	if jsonx.Unmarshal(row.Payload, &envelope) == nil && envelope.Record != nil && envelope.Record.Observation.Buckets != nil {
		item.Buckets = envelope.Record.Observation.Buckets[:]
	}
	return item
}
