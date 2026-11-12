package biz

import (
	"context"
	"errors"
	"strings"
)

var ErrInvalidOperationAuditQuery = errors.New("invalid operation audit query")
var ErrOperationAuditUnavailable = errors.New("operation audit service unavailable")

type OperationAudit struct {
	EventID string
	// ActorUsername is resolved from the identity schema at read time; the
	// shared audit table only stores actor_user_id.
	ActorUsername    string
	ActorUserID      int64
	Operation        string
	ResourceID       string
	DecisionVersions string
	Reason           string
	OccurredAt       int64
	Result           string
}

type OperationAuditFilter struct {
	ActorUserID int64
	Operation   string
	ResourceID  string
	Result      string
	StartTimeMS int64
	EndTimeMS   int64
	Offset      int
	Limit       int
}

type OperationAuditPage struct {
	Items    []OperationAudit
	Total    int64
	Page     int
	PageSize int
}

type OperationAuditRepo interface {
	List(ctx context.Context, filter OperationAuditFilter) ([]OperationAudit, int64, error)
}

type OperationAuditUsecase struct {
	repo OperationAuditRepo
}

func NewOperationAuditUsecase(repo OperationAuditRepo) *OperationAuditUsecase {
	return &OperationAuditUsecase{repo: repo}
}

func (uc *OperationAuditUsecase) List(ctx context.Context, filter OperationAuditFilter, page, pageSize int) (OperationAuditPage, error) {
	if uc == nil || uc.repo == nil {
		return OperationAuditPage{}, ErrOperationAuditUnavailable
	}
	filter.Operation = strings.TrimSpace(filter.Operation)
	filter.ResourceID = strings.TrimSpace(filter.ResourceID)
	filter.Result = strings.ToLower(strings.TrimSpace(filter.Result))
	if filter.ActorUserID < 0 || filter.StartTimeMS < 0 || filter.EndTimeMS < 0 || (filter.StartTimeMS > 0 && filter.EndTimeMS > 0 && filter.StartTimeMS > filter.EndTimeMS) {
		return OperationAuditPage{}, ErrInvalidOperationAuditQuery
	}
	if len(filter.Operation) > 160 || len(filter.ResourceID) > 128 || page < 0 || pageSize < 0 || page > 1_000_000 {
		return OperationAuditPage{}, ErrInvalidOperationAuditQuery
	}
	if filter.Result != "" && filter.Result != "success" && filter.Result != "failure" {
		return OperationAuditPage{}, ErrInvalidOperationAuditQuery
	}
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	filter.Offset = (page - 1) * pageSize
	filter.Limit = pageSize
	items, total, err := uc.repo.List(ctx, filter)
	if err != nil {
		return OperationAuditPage{}, err
	}
	return OperationAuditPage{Items: items, Total: total, Page: page, PageSize: pageSize}, nil
}
