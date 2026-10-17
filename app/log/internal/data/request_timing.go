package data

import (
	"context"
	"fmt"

	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"

	"gorm.io/gorm"
)

// This is an association index, not a second span store. ReservationID stays
// NULL for attempts rejected before reservation, and ownership comes from the
// verified summary, never from individual attempt inputs.
type requestTraceAttemptRefModel struct {
	ObservationID string  `gorm:"column:observation_id;primaryKey;size:36"`
	AttemptNumber int32   `gorm:"column:attempt_number;primaryKey"`
	UserID        int64   `gorm:"column:user_id;index:idx_trace_ref_reservation,priority:1"`
	RequestID     string  `gorm:"column:request_id;size:128"`
	ReservationID *string `gorm:"column:reservation_id;size:64;index:idx_trace_ref_reservation,priority:2"`
}

func (requestTraceAttemptRefModel) TableName() string { return "request_trace_attempt_refs" }

func validateRequestTimingEntry(entry *biz.LogEntry) error {
	if entry == nil {
		return biz.ErrInvalidRequestTiming
	}
	if entry.Source != requesttrace.TimingSource {
		if entry.RequestTiming != nil {
			return biz.ErrInvalidRequestTiming
		}
		return nil
	}
	s := entry.RequestTiming
	if err := s.Validate(); err != nil {
		return biz.ErrInvalidRequestTiming
	}
	if entry.UserID != s.UserID || entry.RootRequestID != s.RootRequestID ||
		entry.DedupeKey != requesttrace.TimingDedupeKey(s.ObservationID) {
		return biz.ErrInvalidRequestTiming
	}
	return nil
}

func encodeRequestTiming(entry *biz.LogEntry) (string, error) {
	if err := validateRequestTimingEntry(entry); err != nil {
		return "", err
	}
	encoded, err := jsonx.Marshal(entry.RequestTiming)
	return string(encoded), err
}

func decodeRequestTiming(encoded string) (*requesttrace.TimingSummary, error) {
	var summary requesttrace.TimingSummary
	if err := jsonx.Unmarshal([]byte(encoded), &summary); err != nil {
		return nil, fmt.Errorf("decode stored request timing: %w", err)
	}
	if err := summary.Validate(); err != nil {
		return nil, fmt.Errorf("invalid stored request timing: %w", err)
	}
	return &summary, nil
}

func newRequestTraceAttemptRefs(entry *biz.LogEntry) []requestTraceAttemptRefModel {
	s := entry.RequestTiming
	if s == nil {
		return nil
	}
	refs := make([]requestTraceAttemptRefModel, 0, len(s.Attempts))
	for _, a := range s.Attempts {
		refs = append(refs, requestTraceAttemptRefModel{
			ObservationID: s.ObservationID, AttemptNumber: a.Number,
			UserID: s.UserID, RequestID: a.RequestID, ReservationID: a.ReservationID,
		})
	}
	return refs
}

// Use precisely the deletion's owner predicate in the same transaction. Do not
// remove consumption/audit claims: their existing replay policy is unchanged.
func deleteRequestTimingRelations(tx, scoped *gorm.DB) error {
	timing := scoped.Session(&gorm.Session{}).Model(&logModel{}).Where("source = ?", requesttrace.TimingSource)
	observations := timing.Session(&gorm.Session{}).Select("observation_id")
	if err := tx.Where("observation_id IN (?)", observations).Delete(&requestTraceAttemptRefModel{}).Error; err != nil {
		return err
	}
	ids := timing.Session(&gorm.Session{}).Select("id")
	return tx.Where("log_id IN (?)", ids).Delete(&logIngestDedupeClaimModel{}).Error
}

func (r *Repository) deleteTimingMemory(entry *biz.LogEntry) {
	if entry.RequestTiming == nil {
		return
	}
	id := entry.RequestTiming.ObservationID
	delete(r.timingRefs, id)
	delete(r.dedupe, requesttrace.TimingDedupeKey(id))
}

func timingVisible(ctx context.Context, entry *biz.LogEntry) bool {
	if entry.Source != requesttrace.TimingSource || entry.UserID != 0 {
		return true
	}
	q, iam := authorization.QueryScopeFromContext(ctx, "log.request.list")
	return !iam || q.Global()
}

func applyTimingVisibility(ctx context.Context, query *gorm.DB) *gorm.DB {
	if q, iam := authorization.QueryScopeFromContext(ctx, "log.request.list"); iam && !q.Global() {
		return query.Where("(COALESCE(source, '') <> ? OR COALESCE(user_id, 0) <> 0)", requesttrace.TimingSource)
	}
	return query
}
