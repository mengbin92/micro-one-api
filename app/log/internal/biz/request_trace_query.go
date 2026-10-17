package biz

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	logv1 "micro-one-api/api/log/v1"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	"github.com/google/uuid"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/requesttrace"
	"micro-one-api/pkg/jsonx"
)

type RequestTraceFilter struct {
	StartMS       int64
	EndMS         int64
	Outcome       string
	UserID        *int64
	RootRequestID string
	ReservationID string
	PageSize      int32
	PageToken     string
}
type RequestTraceCursor struct {
	StartMS       int64  `json:"start"`
	EndMS         int64  `json:"end"`
	FilterHash    string `json:"filter"`
	SnapshotMaxID int64  `json:"snapshot"`
	AfterStartMS  int64  `json:"after_start"`
	AfterID       int64  `json:"after_id"`
}
type RequestTraceRepo interface {
	ListRequestTraces(context.Context, RequestTraceFilter, RequestTraceCursor) ([]*LogEntry, RequestTraceCursor, bool, error)
	GetRequestTrace(context.Context, string) (*LogEntry, error)
	ReadRequestTrace(context.Context, *requesttrace.TimingSummary) (*requesttrace.TraceDetail, error)
}

var ErrRequestTraceNotFound = errors.NotFound(logv1.RequestTimingErrorReason_REQUEST_TRACE_NOT_FOUND.String(), "request trace not found")

func NormalizeRequestTraceFilter(f RequestTraceFilter, now time.Time) (RequestTraceFilter, RequestTraceCursor, error) {
	bad := func() (RequestTraceFilter, RequestTraceCursor, error) {
		return f, RequestTraceCursor{}, ErrInvalidRequestTiming
	}
	c := RequestTraceCursor{}
	if len(f.PageToken) > 2048 {
		return bad()
	}
	if f.PageToken != "" {
		encoded, err := base64.RawURLEncoding.DecodeString(f.PageToken)
		if err != nil || jsonx.Unmarshal(encoded, &c) != nil || c.SnapshotMaxID <= 0 || c.AfterID <= 0 || c.AfterStartMS <= 0 {
			return bad()
		}
		if f.StartMS == 0 {
			f.StartMS = c.StartMS
		}
		if f.EndMS == 0 {
			f.EndMS = c.EndMS
		}
	}
	if f.EndMS == 0 {
		f.EndMS = now.UnixMilli()
	}
	if f.StartMS == 0 {
		if f.ReservationID != "" {
			f.StartMS = 1
		} else {
			f.StartMS = f.EndMS - int64(24*time.Hour/time.Millisecond)
		}
	}
	if f.StartMS <= 0 || f.EndMS <= f.StartMS || (f.ReservationID == "" && f.EndMS-f.StartMS > int64(7*24*time.Hour/time.Millisecond)) ||
		f.Outcome != "" && !requesttrace.ValidTimingOutcome(f.Outcome) || len(f.RootRequestID) > 128 ||
		f.UserID != nil && *f.UserID < 0 || len(f.ReservationID) > 64 || f.ReservationID != "" && (f.UserID == nil || *f.UserID <= 0) {
		return bad()
	}
	if f.PageSize == 0 {
		f.PageSize = 50
	}
	if f.PageSize < 1 || f.PageSize > 200 {
		return bad()
	}
	identity := f
	identity.PageToken = ""
	identity.PageSize = 0
	encoded, _ := jsonx.Marshal(identity)
	digest := fmt.Sprintf("%x", sha256.Sum256(encoded))
	if c.FilterHash != "" && c.FilterHash != digest {
		return bad()
	}
	c.FilterHash = digest
	c.StartMS = f.StartMS
	c.EndMS = f.EndMS
	return f, c, nil
}

func (uc *LogUsecase) ListRequestTraces(ctx context.Context, filter RequestTraceFilter) ([]*requesttrace.TimingSummary, string, error) {
	ctx, err := uc.authorizeUserOperation(ctx, "log.requests.list", "log.request.list")
	if err != nil {
		return nil, "", err
	}
	f, c, err := NormalizeRequestTraceFilter(filter, time.Now())
	if err != nil {
		return nil, "", err
	}
	repo, ok := uc.repo.(RequestTraceRepo)
	if !ok {
		return nil, "", errors.ServiceUnavailable(logv1.RequestTimingErrorReason_TRACE_UNAVAILABLE.String(), "request trace storage unavailable")
	}
	rows, c, more, err := repo.ListRequestTraces(ctx, f, c)
	if err != nil {
		return nil, "", err
	}
	items := make([]*requesttrace.TimingSummary, 0, len(rows))
	for _, row := range rows {
		items = append(items, row.RequestTiming.Clone())
	}
	token := ""
	if more {
		encoded, _ := jsonx.Marshal(c)
		token = base64.RawURLEncoding.EncodeToString(encoded)
	}
	return items, token, nil
}

func (uc *LogUsecase) GetRequestTrace(ctx context.Context, observationID string) (*requesttrace.TraceDetail, error) {
	ctx, err := uc.authorizeUserOperation(ctx, "log.requests.read", "log.request.read")
	if err != nil {
		return nil, err
	}
	id, err := uuid.Parse(observationID)
	if err != nil || id.String() != observationID {
		return nil, ErrInvalidRequestTiming
	}
	repo, ok := uc.repo.(RequestTraceRepo)
	if !ok {
		return nil, errors.ServiceUnavailable(logv1.RequestTimingErrorReason_TRACE_UNAVAILABLE.String(), "request trace storage unavailable")
	}
	entry, err := repo.GetRequestTrace(ctx, observationID)
	if err != nil {
		return nil, err
	}
	if err = authorization.Require(ctx, "log.request.read", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: entry.ID, OwnerUserID: entry.UserID}); err != nil {
		return nil, err
	}
	if entry.UserID == 0 {
		if q, iam := authorization.QueryScopeFromContext(ctx, "log.request.read"); iam && !q.Global() {
			return nil, authorization.ErrDenied
		}
	}
	return repo.ReadRequestTrace(ctx, entry.RequestTiming.Clone())
}
