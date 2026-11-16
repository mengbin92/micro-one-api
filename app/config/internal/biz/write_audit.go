package biz

import (
	"context"
	"errors"
	"strings"

	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

var (
	ErrWriteAuditUnavailable  = errors.New("write audit read unavailable")
	ErrInvalidWriteAuditQuery = errors.New("invalid write audit query")
)

// WriteAudit is one row of the owner's resource_write_audits table. The type
// is owner-local; the admin audit console aggregates the same shape from
// every owner.
type WriteAudit struct {
	EventID          string
	ActorUserID      int64
	Operation        string
	ResourceID       string
	DecisionVersions string
	Reason           string
	OccurredAt       int64
	Result           string
}

type WriteAuditPage struct {
	Items   []WriteAudit
	Total   int64
	StoreID string
}

type WriteAuditFilter struct {
	ActorUserID int64
	Operation   string
	ResourceID  string
	Result      string
	StartTimeMS int64
	EndTimeMS   int64
	Offset      int
	Limit       int
}

type WriteAuditRepo interface {
	ListWriteAudits(ctx context.Context, filter WriteAuditFilter) (WriteAuditPage, error)
}

// WriteAuditUsecase serves the owner's resource_write_audits table to the
// admin audit console. It carries no operator decisions: the transport
// registry admits only the admin system identity for this RPC and any
// operator-carrying request is rejected here.
type WriteAuditUsecase struct {
	repo WriteAuditRepo
}

func NewWriteAuditUsecase(repo WriteAuditRepo) *WriteAuditUsecase {
	return &WriteAuditUsecase{repo: repo}
}

func (uc *WriteAuditUsecase) List(ctx context.Context, filter WriteAuditFilter) (WriteAuditPage, error) {
	if uc == nil || uc.repo == nil {
		return WriteAuditPage{}, ErrWriteAuditUnavailable
	}
	if authorization.External(ctx) && !serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return WriteAuditPage{}, authorization.ErrDenied
	}
	filter.Operation = strings.TrimSpace(filter.Operation)
	filter.ResourceID = strings.TrimSpace(filter.ResourceID)
	filter.Result = strings.ToLower(strings.TrimSpace(filter.Result))
	if filter.ActorUserID < 0 || filter.StartTimeMS < 0 || filter.EndTimeMS < 0 ||
		(filter.StartTimeMS > 0 && filter.EndTimeMS > 0 && filter.StartTimeMS > filter.EndTimeMS) {
		return WriteAuditPage{}, ErrInvalidWriteAuditQuery
	}
	if len(filter.Operation) > 160 || len(filter.ResourceID) > 128 {
		return WriteAuditPage{}, ErrInvalidWriteAuditQuery
	}
	if filter.Result != "" && filter.Result != "success" && filter.Result != "failure" {
		return WriteAuditPage{}, ErrInvalidWriteAuditQuery
	}
	if filter.Limit <= 0 || filter.Limit > 5000 {
		filter.Limit = 5000
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	return uc.repo.ListWriteAudits(ctx, filter)
}
