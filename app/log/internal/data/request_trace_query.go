package data

import (
	"context"
	"errors"
	"sort"

	"gorm.io/gorm"
	"micro-one-api/app/log/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/platform/database/authzquery"
)

func (r *Repository) ListRequestTraces(ctx context.Context, f biz.RequestTraceFilter, c biz.RequestTraceCursor) ([]*biz.LogEntry, biz.RequestTraceCursor, bool, error) {
	if r.db == nil {
		return r.listRequestTracesMemory(ctx, f, c)
	}
	q, err := authzquery.ApplyContext(ctx, r.db.WithContext(ctx).Model(&logModel{}), authzquery.Columns{Resource: "id", User: "user_id"}, "log.request.list")
	if err != nil {
		return nil, c, false, err
	}
	q = applyTimingVisibility(ctx, q).Where("source = ? AND request_started_at_ms >= ? AND request_started_at_ms < ?", requesttrace.TimingSource, f.StartMS, f.EndMS)
	if f.UserID != nil {
		q = q.Where("user_id = ?", *f.UserID)
	}
	if f.RootRequestID != "" {
		q = q.Where("root_request_id = ?", f.RootRequestID)
	}
	if f.Outcome != "" {
		q = q.Where("request_outcome = ?", f.Outcome)
	}
	if f.ReservationID != "" {
		refs := r.db.Model(&requestTraceAttemptRefModel{}).Select("observation_id").Where("user_id = ? AND reservation_id = ?", *f.UserID, f.ReservationID)
		q = q.Where("observation_id IN (?)", refs)
	}
	if c.SnapshotMaxID == 0 {
		var maxID struct{ ID int64 }
		if err = q.Session(&gorm.Session{}).Select("COALESCE(MAX(id),0) AS id").Scan(&maxID).Error; err != nil {
			return nil, c, false, err
		}
		c.SnapshotMaxID = maxID.ID
	}
	q = q.Where("id <= ?", c.SnapshotMaxID)
	if c.AfterID > 0 {
		q = q.Where("(request_started_at_ms < ? OR (request_started_at_ms = ? AND id < ?))", c.AfterStartMS, c.AfterStartMS, c.AfterID)
	}
	var models []logModel
	if err = q.Order("request_started_at_ms DESC, id DESC").Limit(int(f.PageSize) + 1).Find(&models).Error; err != nil {
		return nil, c, false, err
	}
	more := len(models) > int(f.PageSize)
	if more {
		models = models[:f.PageSize]
	}
	entries := make([]*biz.LogEntry, 0, len(models))
	for _, model := range models {
		entry, err := logModelToEntry(model)
		if err != nil {
			return nil, c, false, err
		}
		entries = append(entries, entry)
	}
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		c.AfterID = last.ID
		c.AfterStartMS = last.RequestTiming.StartedAtMS
	}
	return entries, c, more, nil
}

func (r *Repository) listRequestTracesMemory(ctx context.Context, f biz.RequestTraceFilter, c biz.RequestTraceCursor) ([]*biz.LogEntry, biz.RequestTraceCursor, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if c.SnapshotMaxID == 0 {
		c.SnapshotMaxID = r.seq
	}
	var entries []*biz.LogEntry
	for _, entry := range r.mem {
		s := entry.RequestTiming
		if s == nil || entry.ID > c.SnapshotMaxID || !timingVisible(ctx, entry) ||
			authorization.Require(ctx, "log.request.list", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: entry.ID, OwnerUserID: entry.UserID}) != nil ||
			s.StartedAtMS < f.StartMS || s.StartedAtMS >= f.EndMS || f.UserID != nil && s.UserID != *f.UserID ||
			f.RootRequestID != "" && s.RootRequestID != f.RootRequestID || f.Outcome != "" && s.Outcome != f.Outcome {
			continue
		}
		if c.AfterID > 0 && (s.StartedAtMS > c.AfterStartMS || s.StartedAtMS == c.AfterStartMS && entry.ID >= c.AfterID) {
			continue
		}
		if f.ReservationID != "" {
			found := false
			for _, a := range s.Attempts {
				if a.ReservationID != nil && *a.ReservationID == f.ReservationID {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		copy := *entry
		copy.RequestTiming = s.Clone()
		entries = append(entries, &copy)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].RequestTiming.StartedAtMS == entries[j].RequestTiming.StartedAtMS {
			return entries[i].ID > entries[j].ID
		}
		return entries[i].RequestTiming.StartedAtMS > entries[j].RequestTiming.StartedAtMS
	})
	more := len(entries) > int(f.PageSize)
	if more {
		entries = entries[:f.PageSize]
	}
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		c.AfterID = last.ID
		c.AfterStartMS = last.RequestTiming.StartedAtMS
	}
	return entries, c, more, nil
}

func (r *Repository) GetRequestTrace(ctx context.Context, id string) (*biz.LogEntry, error) {
	if r.db == nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, entry := range r.mem {
			if entry.RequestTiming != nil && entry.RequestTiming.ObservationID == id {
				if err := authorization.Require(ctx, "log.request.read", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: entry.ID, OwnerUserID: entry.UserID}); err != nil {
					return nil, err
				}
				copy := *entry
				copy.RequestTiming = entry.RequestTiming.Clone()
				return &copy, nil
			}
		}
		return nil, biz.ErrRequestTraceNotFound
	}
	q, err := authzquery.ApplyContext(ctx, r.db.WithContext(ctx).Model(&logModel{}), authzquery.Columns{Resource: "id", User: "user_id"}, "log.request.read")
	if err != nil {
		return nil, err
	}
	var model logModel
	err = q.Where("source = ? AND observation_id = ?", requesttrace.TimingSource, id).Take(&model).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrRequestTraceNotFound
	}
	if err != nil {
		return nil, err
	}
	return logModelToEntry(model)
}
