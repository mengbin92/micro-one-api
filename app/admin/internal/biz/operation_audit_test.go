package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type operationAuditRepoStub struct {
	filter OperationAuditFilter
	items  []OperationAudit
	total  int64
}

func (r *operationAuditRepoStub) List(_ context.Context, filter OperationAuditFilter) ([]OperationAudit, int64, error) {
	r.filter = filter
	return r.items, r.total, nil
}

func TestOperationAuditUsecaseNormalizesPaginationAndValidatesFilters(t *testing.T) {
	repo := &operationAuditRepoStub{total: 3}
	uc := NewOperationAuditUsecase(repo)
	page, err := uc.List(context.Background(), OperationAuditFilter{ActorUserID: 7, Operation: " pricing ", Result: " SUCCESS "}, 0, 500)
	require.NoError(t, err)
	require.Equal(t, 1, page.Page)
	require.Equal(t, 100, page.PageSize)
	require.Equal(t, OperationAuditFilter{ActorUserID: 7, Operation: "pricing", Result: "success", Limit: 100}, repo.filter)

	_, err = uc.List(context.Background(), OperationAuditFilter{StartTimeMS: 20, EndTimeMS: 10}, 1, 20)
	require.ErrorIs(t, err, ErrInvalidOperationAuditQuery)
	_, err = uc.List(context.Background(), OperationAuditFilter{Result: "unknown"}, 1, 20)
	require.ErrorIs(t, err, ErrInvalidOperationAuditQuery)
}
