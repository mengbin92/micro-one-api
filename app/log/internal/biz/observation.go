package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/security/serviceidentity"

	"github.com/go-kratos/kratos/v3/errors"
)

var (
	ErrInvalidUsageObservation  = errors.BadRequest(logv1.UsageObservationErrorReason_INVALID_USAGE_OBSERVATION.String(), "invalid usage observation envelope")
	ErrUsageObservationConflict = errors.Conflict(logv1.UsageObservationErrorReason_USAGE_OBSERVATION_CONFLICT.String(), "usage observation event id reused with different payload")
	ErrUsageObservationLimit    = errors.BadRequest(logv1.UsageObservationErrorReason_USAGE_OBSERVATION_LIMIT.String(), "usage observation exceeds limits")
	ErrUsageObservationNotFound = errors.NotFound(logv1.UsageObservationErrorReason_INVALID_USAGE_OBSERVATION.String(), "usage observation not found")
	// ErrUsageObservationUnavailable is returned when the consumer cannot store
	// evidence durably. It must never be acknowledged.
	ErrUsageObservationUnavailable = errors.InternalServer(logv1.UsageObservationErrorReason_USAGE_OBSERVATION_UNAVAILABLE.String(), "usage observation storage is not durable")
)

const (
	// RecordUsageObservationMethod is the only writer capability. Query paths
	// never grant it.
	RecordUsageObservationMethod = "/api.log.v1.LogService/RecordUsageObservation"
	maxObservationPayloadBytes   = evidence.MaxObservationEventBytes
	// List response budget: summaries are small, but the cap keeps one page from
	// ever approaching the gRPC receive limit.
	maxObservationListBytes = 3 << 20
)

// UsageObservationRow is the dedicated evidence shape owned by log-service.
// It is not a consumption log and never enters usage aggregates.
type UsageObservationRow struct {
	EventID          string
	SendID           string
	ObservationID    string
	Category         string
	Sequence         int64
	PayloadSHA256    string
	Payload          []byte
	UserID           int64
	Protocol         string
	SourceKind       string
	SourceID         int64
	Purpose          string
	UpstreamModelID  string
	AttemptRequestID string
	RootRequestID    string
	Capture          string
	CaptureReason    string
	Status           string
	ParserRevision   string
	ContractRevision string
	BuildRevision    string
	HTTPStatus       int32
	Stream           bool
	ObservedAtMS     int64
	ReceivedAtMS     int64
}

type UsageObservationFilter struct {
	StartTimeMS   int64
	EndTimeMS     int64
	UserID        int64
	ObservationID string
	SendID        string
	Capture       string
	Status        string
	Category      string
	PageSize      int32
	PageToken     string
}

type UsageObservationRepo interface {
	// Record stores the event. A duplicate event id with identical bytes is
	// idempotent; different bytes under the same id return ErrUsageObservationConflict.
	// Implementations that are not durable must return ErrObservationStorageUnavailable.
	Record(ctx context.Context, row *UsageObservationRow) (duplicate bool, err error)
	// ListUsageObservations returns summary rows (Payload may be empty) already
	// constrained by the request query scope.
	ListUsageObservations(ctx context.Context, filter UsageObservationFilter) ([]*UsageObservationRow, string, error)
	// GetUsageObservation returns one row within the request query scope.
	GetUsageObservation(ctx context.Context, eventID string) (*UsageObservationRow, error)
	DeleteObservationsBefore(ctx context.Context, before time.Time) (int64, error)
}

type UsageObservationUsecase struct {
	authorization authorization.Resolver
	repo          UsageObservationRepo
}

func NewUsageObservationUsecase(repo UsageObservationRepo) *UsageObservationUsecase {
	return &UsageObservationUsecase{repo: repo}
}

func (uc *UsageObservationUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }

// Record validates the envelope at the owner. The producer cannot choose a
// digest, an ownership scope or a category outside the contract.
func (uc *UsageObservationUsecase) Record(ctx context.Context, eventID, sendID, observationID, category, payloadSHA256 string, sequence int64, payload []byte) (*evidence.DeliveryReceipt, error) {
	if !serviceidentity.HasSystemCapability(ctx, RecordUsageObservationMethod) {
		return nil, authorization.ErrDenied
	}
	if len(payload) == 0 || len(payload) > maxObservationPayloadBytes || len(eventID) > 255 || len(payloadSHA256) != 64 || sequence < 0 {
		return nil, ErrUsageObservationLimit
	}
	sum := sha256.Sum256(payload)
	digest := hex.EncodeToString(sum[:])
	if !strings.EqualFold(digest, payloadSHA256) {
		return nil, ErrInvalidUsageObservation
	}
	var envelope evidence.SendEvent
	if err := jsonx.Unmarshal(payload, &envelope); err != nil {
		return nil, ErrInvalidUsageObservation
	}
	if err := envelope.Validate(); err != nil {
		return nil, ErrInvalidUsageObservation
	}
	if envelope.ID != eventID || envelope.Send.SendID != sendID || envelope.Send.ObservationID != observationID || envelope.Category != category || envelope.Sequence != sequence {
		return nil, ErrInvalidUsageObservation
	}
	row := &UsageObservationRow{
		EventID: eventID, SendID: sendID, ObservationID: observationID, Category: category,
		Sequence: sequence, PayloadSHA256: digest, Payload: payload,
		UserID: envelope.Send.UserID, Protocol: envelope.Send.Protocol, SourceKind: envelope.Send.SourceKind,
		SourceID: envelope.Send.SourceID, Purpose: envelope.Send.Purpose, UpstreamModelID: envelope.Send.Model,
		AttemptRequestID: envelope.Send.RequestID, RootRequestID: envelope.Send.RootRequestID,
		Capture: envelope.Capture, CaptureReason: envelope.Reason, Status: envelopeObservationStatus(&envelope),
		ParserRevision: envelopeParserRevision(&envelope), ContractRevision: envelopeContractRevision(&envelope), BuildRevision: envelope.BuildRevision,
		ObservedAtMS: envelope.ObservedAt, ReceivedAtMS: time.Now().UnixMilli(),
	}
	if envelope.Record != nil {
		row.HTTPStatus = int32(envelope.Record.HTTPStatus)
		row.Stream = envelope.Record.Stream
	}
	duplicate, err := uc.repo.Record(ctx, row)
	if err != nil {
		if errors.Is(err, ErrUsageObservationUnavailable) {
			return nil, ErrUsageObservationUnavailable
		}
		return nil, err
	}
	status := "stored"
	if duplicate {
		status = "duplicate"
	}
	return &evidence.DeliveryReceipt{Accepted: true, Duplicate: duplicate, Status: status, Digest: digest}, nil
}

// ListUsageObservations applies the registered log read execution point. The
// owner repository additionally applies the full query scope (allow and deny)
// before pagination; the user filter can only narrow that result.
func (uc *UsageObservationUsecase) ListUsageObservations(ctx context.Context, filter UsageObservationFilter) ([]*UsageObservationRow, string, error) {
	if filter.StartTimeMS <= 0 || filter.EndTimeMS <= filter.StartTimeMS {
		return nil, "", ErrInvalidUsageObservation
	}
	if filter.PageSize < 1 || filter.PageSize > 200 {
		filter.PageSize = 50
	}
	ctx, err := authorization.Prepare(ctx, uc.authorization, "log.requests.list", "log.request.list")
	if err != nil {
		return nil, "", err
	}
	// In legacy mode there is no scope at all: an unscoped query would return
	// every user's evidence, so it stays denied unless a user narrows it.
	if _, iam := authorization.QueryScopeFromContext(ctx, "log.request.list"); !iam && filter.UserID <= 0 {
		return nil, "", authorization.ErrDenied
	}
	return uc.repo.ListUsageObservations(ctx, filter)
}

// GetUsageObservation requires independent detail and content authority before
// returning an event's canonical payload.
func (uc *UsageObservationUsecase) GetUsageObservation(ctx context.Context, eventID string) (*UsageObservationRow, error) {
	if strings.TrimSpace(eventID) == "" || len(eventID) > 255 {
		return nil, ErrInvalidUsageObservation
	}
	ctx, err := authorization.Prepare(ctx, uc.authorization, "log.requests.read", "log.request.read")
	if err != nil {
		return nil, err
	}
	ctx, err = authorization.PrepareOptional(ctx, uc.authorization, "log.requests.read", "log.request.content.read")
	if err != nil {
		return nil, err
	}
	return uc.repo.GetUsageObservation(ctx, eventID)
}

func (uc *UsageObservationUsecase) CleanupObservations(ctx context.Context, before time.Time) (int64, error) {
	if before.IsZero() {
		return 0, nil
	}
	return uc.repo.DeleteObservationsBefore(ctx, before)
}

// envelopeObservationStatus keeps the bounded usage verdict separate from the
// transport capture classification.
func envelopeObservationStatus(e *evidence.SendEvent) string {
	if e == nil || e.Record == nil {
		return "unobserved"
	}
	return evidence.NormalizeStatus(e.Record.Observation.Status)
}

func envelopeParserRevision(e *evidence.SendEvent) string {
	if e == nil || e.Record == nil {
		return ""
	}
	return e.Record.Observation.ParserRevision
}

func envelopeContractRevision(e *evidence.SendEvent) string {
	if e == nil || e.Record == nil {
		return ""
	}
	return e.Record.Observation.ContractRevision
}
