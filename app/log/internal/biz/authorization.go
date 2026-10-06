package biz

import (
	"context"
	"errors"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
	"strings"
	"time"
)

func (uc *LogUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }
func (uc *LogUsecase) authorize(ctx context.Context, point, op string) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.Prepare(ctx, uc.authorization, point, op)
}
func (uc *LogUsecase) authorizeSystem(ctx context.Context, method, point, op string) error {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, method) {
		return nil
	}
	prepared, err := authorization.Prepare(ctx, uc.authorization, point, op)
	if err != nil {
		return err
	}
	if _, iam := authorization.QueryScopeFromContext(prepared, op); iam {
		return authorization.ErrDenied
	}
	return nil
}

func (uc *LogUsecase) authorizeContent(ctx context.Context) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.PrepareOptional(ctx, uc.authorization, "log.requests.read", "log.request.content.read")
}
func redactLog(ctx context.Context, entry *LogEntry) *LogEntry {
	if entry == nil {
		return nil
	}
	copy := *entry
	if authorization.Require(ctx, "log.request.content.read", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: entry.ID, OwnerUserID: entry.UserID}) != nil {
		copy.Message = ""
		copy.UsageFieldShape = ""
	}
	return &copy
}
func redactLogs(ctx context.Context, entries []*LogEntry) []*LogEntry {
	for i, entry := range entries {
		entries[i] = redactLog(ctx, entry)
	}
	return entries
}

// Self entry points prove the actor again at the data owner. Administrative
// permissions and request user IDs cannot enlarge this predicate.
func (uc *LogUsecase) prepareOwn(ctx context.Context, userID int64, op string) (context.Context, error) {
	ctx, err := authorization.PrepareSelf(ctx, uc.authorization, "log.self", op)
	if err != nil {
		return ctx, err
	}
	if q, iam := authorization.QueryScopeFromContext(ctx, op); iam && q.ActorID != userID {
		return ctx, authorization.ErrDenied
	}
	return ctx, nil
}
func (uc *LogUsecase) ListOwnLogs(ctx context.Context, userID int64, page, pageSize int32, level, keyword string) ([]*LogEntry, int64, error) {
	ctx, err := uc.prepareOwn(ctx, userID, "log.request.list")
	if err != nil {
		return nil, 0, err
	}
	if q, iam := authorization.QueryScopeFromContext(ctx, "log.request.list"); iam {
		ctx = authorization.WithQueryScope(ctx, "log.request.content.read", q)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	return uc.repo.ListByUser(ctx, userID, page, pageSize, level, keyword)
}
func (uc *LogUsecase) OwnUsageStats(ctx context.Context, userID int64, start, end time.Time) ([]*UsageStat, error) {
	ctx, err := uc.prepareOwn(ctx, userID, "log.request.stats.read")
	if err != nil {
		return nil, err
	}
	return uc.repo.UsageByUser(ctx, userID, start, end)
}

// ExportLogs is explicitly paged. Export grants define the query scope and do
// not inherit list scope; content remains independently authorized.
func (uc *LogUsecase) ExportLogs(ctx context.Context, page, pageSize int32, level, source, keyword string) ([]*LogEntry, int64, error) {
	ctx, err := uc.authorizeUserOperation(ctx, "log.requests.export", "log.request.export")
	if err != nil {
		return nil, 0, err
	}
	ctx, err = uc.authorizeContent(ctx)
	if err != nil {
		return nil, 0, err
	}
	if q, iam := authorization.QueryScopeFromContext(ctx, "log.request.export"); iam {
		ctx = authorization.WithQueryScope(ctx, "log.request.list", q)
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 200
	}
	rows, total, err := uc.repo.List(ctx, page, pageSize, level, source, keyword)
	return redactLogs(ctx, rows), total, err
}

func (uc *LogUsecase) PurgeLogs(ctx context.Context, before time.Time, reason string) (int64, error) {
	ctx, err := uc.authorizeUserOperation(ctx, "log.requests.delete", "log.request.purge")
	if err != nil {
		return 0, err
	}
	if before.IsZero() || before.Unix() <= 0 || before.After(time.Now()) || strings.TrimSpace(reason) == "" {
		return 0, errors.New("past cutoff and reason required")
	}
	if q, iam := authorization.QueryScopeFromContext(ctx, "log.request.purge"); iam && !q.Global() {
		return 0, authorization.ErrDenied
	}
	ctx = authorization.WithWriteReason(ctx, reason)
	return uc.repo.Delete(ctx, DeleteLogsFilter{EndTime: before, Operation: "log.request.purge"})
}

func (uc *LogUsecase) authorizeUserOperation(ctx context.Context, point, op string) (context.Context, error) {
	if !authorization.External(ctx) {
		return ctx, nil
	}
	return authorization.Prepare(ctx, uc.authorization, point, op)
}
