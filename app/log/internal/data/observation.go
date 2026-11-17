package data

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"micro-one-api/app/log/internal/biz"
	"micro-one-api/platform/database/authzquery"
	"micro-one-api/platform/database/xdb"
)

// usageObservationModel is the dedicated Log-owner evidence table. It is not
// the consumption log table and never joins usage/quota aggregates.
// observed_at_ms is the exact millisecond the send was observed: the query
// contract is millisecond precision, so a second-granularity column would
// silently widen windows.
type usageObservationModel struct {
	EventID       string   `gorm:"column:event_id;primaryKey;size:255;index:idx_usage_observation_observed,priority:2;index:idx_usage_observation_user_time,priority:3"`
	SendID        string   `gorm:"column:send_id;size:160;index:idx_usage_observation_send_id"`
	ObservationID string   `gorm:"column:observation_id;size:64;index:idx_usage_observation_send,priority:1"`
	Category      string   `gorm:"column:category;size:16;index:idx_usage_observation_send,priority:2"`
	Sequence      int64    `gorm:"column:sequence"`
	PayloadSHA256 string   `gorm:"column:payload_sha256;size:64"`
	Payload       string   `gorm:"column:payload"`
	UserID        int64    `gorm:"column:user_id;index:idx_usage_observation_user_time,priority:1"`
	Protocol      string   `gorm:"column:usage_protocol;size:64"`
	SourceKind    string   `gorm:"column:source_kind;size:32"`
	SourceID      int64    `gorm:"column:source_id"`
	Purpose       string   `gorm:"column:purpose;size:32;index:idx_usage_observation_purpose"`
	UpstreamModel string   `gorm:"column:upstream_model_id;size:255"`
	RequestID     string   `gorm:"column:attempt_request_id;size:128"`
	RootRequestID string   `gorm:"column:root_request_id;size:128"`
	Capture       string   `gorm:"column:capture;size:32;index:idx_usage_observation_capture"`
	CaptureReason string   `gorm:"column:capture_reason;size:64"`
	Status        string   `gorm:"column:usage_status;size:16;index:idx_usage_observation_status"`
	ParserRev     string   `gorm:"column:parser_revision;size:64"`
	ContractRev   string   `gorm:"column:contract_revision;size:64"`
	BuildRev      string   `gorm:"column:build_revision;size:128"`
	HTTPStatus    int32    `gorm:"column:http_status"`
	IsStream      xdb.Flag `gorm:"column:is_stream"`
	ObservedAtMS  int64    `gorm:"column:observed_at_ms;index:idx_usage_observation_observed,priority:1;index:idx_usage_observation_user_time,priority:2"`
	ReceivedAtMS  int64    `gorm:"column:received_at_ms;index:idx_usage_observation_received"`
}

func (usageObservationModel) TableName() string { return "upstream_usage_observations" }

type usageObservationRepo struct {
	db *gorm.DB
}

// NewUsageObservationRepo returns the interface on purpose: the observation
// table is a dedicated evidence store, not a second LogRepo.
//
// A repository without a database (the explicit in-memory development mode) is
// represented by an unavailable implementation: it must never acknowledge
// evidence, because the relay deletes its only durable copy on ACK.
func NewUsageObservationRepo(r *Repository) biz.UsageObservationRepo {
	if r == nil || r.db == nil {
		return unavailableObservationRepo{}
	}
	return &usageObservationRepo{db: r.db}
}

// unavailableObservationRepo fails closed for non-durable storage.
type unavailableObservationRepo struct{}

func (unavailableObservationRepo) Record(context.Context, *biz.UsageObservationRow) (bool, error) {
	return false, biz.ErrUsageObservationUnavailable
}
func (unavailableObservationRepo) ListUsageObservations(context.Context, biz.UsageObservationFilter) ([]*biz.UsageObservationRow, string, error) {
	return nil, "", biz.ErrUsageObservationUnavailable
}
func (unavailableObservationRepo) GetUsageObservation(context.Context, string) (*biz.UsageObservationRow, error) {
	return nil, biz.ErrUsageObservationUnavailable
}
func (unavailableObservationRepo) DeleteObservationsBefore(context.Context, time.Time) (int64, error) {
	return 0, biz.ErrUsageObservationUnavailable
}

func (r *usageObservationRepo) Record(ctx context.Context, row *biz.UsageObservationRow) (bool, error) {
	if row == nil {
		return false, biz.ErrInvalidUsageObservation
	}
	m := &usageObservationModel{
		EventID: row.EventID, SendID: row.SendID, ObservationID: row.ObservationID, Category: row.Category,
		Sequence: row.Sequence, PayloadSHA256: row.PayloadSHA256, Payload: string(row.Payload), UserID: row.UserID,
		Protocol: row.Protocol, SourceKind: row.SourceKind, SourceID: row.SourceID, Purpose: row.Purpose,
		UpstreamModel: row.UpstreamModelID, RequestID: row.AttemptRequestID, RootRequestID: row.RootRequestID,
		Capture: row.Capture, CaptureReason: row.CaptureReason, Status: row.Status,
		ParserRev: row.ParserRevision, ContractRev: row.ContractRevision, BuildRev: row.BuildRevision,
		HTTPStatus: row.HTTPStatus, IsStream: xdb.BoolInt(row.Stream), ObservedAtMS: row.ObservedAtMS, ReceivedAtMS: row.ReceivedAtMS,
	}
	err := r.db.WithContext(ctx).Create(m).Error
	if err == nil {
		return false, nil
	}
	if !isLogDuplicateKeyErr(err) {
		return false, err
	}
	var existing usageObservationModel
	if lookupErr := r.db.WithContext(ctx).Where("event_id = ?", row.EventID).Take(&existing).Error; lookupErr != nil {
		return false, err
	}
	if existing.PayloadSHA256 != row.PayloadSHA256 {
		return false, biz.ErrUsageObservationConflict
	}
	return true, nil
}

func (r *usageObservationRepo) ListUsageObservations(ctx context.Context, filter biz.UsageObservationFilter) ([]*biz.UsageObservationRow, string, error) {
	if filter.PageSize < 1 || filter.PageSize > 200 {
		filter.PageSize = 50
	}
	afterMS, eventID, cursorOK := parseObservationPageToken(filter.PageToken)
	if filter.PageToken != "" && !cursorOK {
		return nil, "", biz.ErrInvalidUsageObservation
	}
	// Summary columns only: a page never loads payload bytes.
	query := r.db.WithContext(ctx).Model(&usageObservationModel{}).
		Select("event_id, send_id, observation_id, category, sequence, payload_sha256, user_id, usage_protocol, source_kind, source_id, purpose, upstream_model_id, attempt_request_id, root_request_id, capture, capture_reason, usage_status, parser_revision, contract_revision, build_revision, http_status, is_stream, observed_at_ms, received_at_ms").
		Where("observed_at_ms >= ? AND observed_at_ms < ?", filter.StartTimeMS, filter.EndTimeMS)
	query, err := authzquery.ApplyContext(ctx, query, authzquery.Columns{User: "user_id"}, "log.request.list")
	if err != nil {
		return nil, "", err
	}
	if filter.UserID > 0 {
		// Narrowing only; the query scope above is authoritative.
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.ObservationID != "" {
		query = query.Where("observation_id = ?", filter.ObservationID)
	}
	if filter.SendID != "" {
		query = query.Where("send_id = ?", filter.SendID)
	}
	if filter.Capture != "" {
		query = query.Where("capture = ?", filter.Capture)
	}
	if filter.Status != "" {
		query = query.Where("usage_status = ?", filter.Status)
	}
	if filter.Category != "" {
		query = query.Where("category = ?", filter.Category)
	}
	if cursorOK {
		query = query.Where("(observed_at_ms < ? OR (observed_at_ms = ? AND event_id < ?))", afterMS, afterMS, eventID)
	}
	var models []usageObservationModel
	if err := query.Order("observed_at_ms DESC, event_id DESC").Limit(int(filter.PageSize) + 1).Find(&models).Error; err != nil {
		return nil, "", err
	}
	next := ""
	if len(models) > int(filter.PageSize) {
		models = models[:filter.PageSize]
		last := models[len(models)-1]
		next = strconv.FormatInt(last.ObservedAtMS, 10) + ":" + last.EventID
	}
	rows := make([]*biz.UsageObservationRow, len(models))
	for i, m := range models {
		rows[i] = observationRowFromModel(m)
	}
	return rows, next, nil
}

func (r *usageObservationRepo) GetUsageObservation(ctx context.Context, eventID string) (*biz.UsageObservationRow, error) {
	query := r.db.WithContext(ctx).Model(&usageObservationModel{}).Where("event_id = ?", eventID)
	query, err := authzquery.ApplyContext(ctx, query, authzquery.Columns{User: "user_id"}, "log.request.read", "log.request.content.read")
	if err != nil {
		return nil, err
	}
	var m usageObservationModel
	if err := query.Take(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrUsageObservationNotFound
		}
		return nil, err
	}
	return observationRowFromModel(m), nil
}

func (r *usageObservationRepo) DeleteObservationsBefore(ctx context.Context, before time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Where("received_at_ms < ?", before.UnixMilli()).Delete(&usageObservationModel{})
	return result.RowsAffected, result.Error
}

func observationRowFromModel(m usageObservationModel) *biz.UsageObservationRow {
	return &biz.UsageObservationRow{
		EventID: m.EventID, SendID: m.SendID, ObservationID: m.ObservationID, Category: m.Category,
		Sequence: m.Sequence, PayloadSHA256: m.PayloadSHA256, Payload: []byte(m.Payload), UserID: m.UserID,
		Protocol: m.Protocol, SourceKind: m.SourceKind, SourceID: m.SourceID, Purpose: m.Purpose,
		UpstreamModelID: m.UpstreamModel, AttemptRequestID: m.RequestID, RootRequestID: m.RootRequestID,
		Capture: m.Capture, CaptureReason: m.CaptureReason, Status: m.Status,
		ParserRevision: m.ParserRev, ContractRevision: m.ContractRev, BuildRevision: m.BuildRev,
		HTTPStatus: m.HTTPStatus, Stream: m.IsStream != 0, ObservedAtMS: m.ObservedAtMS, ReceivedAtMS: m.ReceivedAtMS,
	}
}

func parseObservationPageToken(token string) (int64, string, bool) {
	if token == "" {
		return 0, "", false
	}
	left, right, ok := strings.Cut(token, ":")
	if !ok || right == "" || len(right) > 255 {
		return 0, "", false
	}
	at, err := strconv.ParseInt(left, 10, 64)
	if err != nil || at <= 0 {
		return 0, "", false
	}
	return at, right, true
}
