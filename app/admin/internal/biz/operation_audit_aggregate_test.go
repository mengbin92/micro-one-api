package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type aggregateRepoStub struct {
	items  []OperationAudit
	total  int64
	err    error
	store  string
	filter OperationAuditFilter
}

func (s *aggregateRepoStub) List(_ context.Context, filter OperationAuditFilter) (OperationAuditBatch, error) {
	s.filter = filter
	return OperationAuditBatch{Items: s.items, Total: s.total, StoreID: s.store}, s.err
}

func TestAggregatedOperationAuditMergesSourcesSortedWithSummedTotal(t *testing.T) {
	local := &aggregateRepoStub{store: "local", total: 2, items: []OperationAudit{
		{EventID: "a", OccurredAt: 100},
		{EventID: "b", OccurredAt: 300},
	}}
	channel := &aggregateRepoStub{store: "channel", total: 2, items: []OperationAudit{
		{EventID: "c", OccurredAt: 300},
		{EventID: "d", OccurredAt: 50},
	}}
	billing := &aggregateRepoStub{store: "billing", total: 1, items: []OperationAudit{
		{EventID: "e", OccurredAt: 200},
	}}
	repo := NewAggregatedOperationAuditRepo(local, channel, billing)
	batch, err := repo.List(context.Background(), OperationAuditFilter{Offset: 0, Limit: 20})
	require.NoError(t, err)
	require.EqualValues(t, 5, batch.Total, "merged total sums every owner's count")
	require.Equal(t, []string{"c", "b", "e", "a", "d"}, []string{
		batch.Items[0].EventID, batch.Items[1].EventID, batch.Items[2].EventID, batch.Items[3].EventID, batch.Items[4].EventID,
	}, "global ordering by occurred_at DESC, event_id DESC")

	batch, err = repo.List(context.Background(), OperationAuditFilter{Offset: 1, Limit: 2})
	require.NoError(t, err)
	require.EqualValues(t, 5, batch.Total, "offset/limit must not change the total")
	require.Equal(t, []string{"b", "e"}, []string{batch.Items[0].EventID, batch.Items[1].EventID})
}

func TestAggregatedOperationAuditPropagatesOwnerFailure(t *testing.T) {
	local := &aggregateRepoStub{store: "local", total: 1, items: []OperationAudit{{EventID: "a", OccurredAt: 1}}}
	down := &aggregateRepoStub{err: errors.New("billing owner unavailable")}
	repo := NewAggregatedOperationAuditRepo(local, down)
	_, err := repo.List(context.Background(), OperationAuditFilter{Offset: 0, Limit: 20})
	require.Error(t, err, "an unavailable owner must fail the read explicitly, not read as empty")
}

func TestAggregatedOperationAuditOffsetBeyondFetchedWindow(t *testing.T) {
	local := &aggregateRepoStub{store: "local", total: 1, items: []OperationAudit{{EventID: "a", OccurredAt: 1}}}
	repo := NewAggregatedOperationAuditRepo(local)
	batch, err := repo.List(context.Background(), OperationAuditFilter{Offset: 10, Limit: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, batch.Total)
	require.Empty(t, batch.Items)
}

func TestAggregatedOperationAuditCountsSharedStoreOnce(t *testing.T) {
	source := &aggregateRepoStub{store: "same-physical-store", total: 1, items: []OperationAudit{{EventID: "one", OccurredAt: 1}}}
	batch, err := NewAggregatedOperationAuditRepo(source, source, source).List(context.Background(), OperationAuditFilter{Limit: 20})
	require.NoError(t, err)
	require.EqualValues(t, 1, batch.Total)
	require.Len(t, batch.Items, 1)
}

func TestAggregatedOperationAuditRejectsIncompletePagination(t *testing.T) {
	source := &aggregateRepoStub{store: "store", total: 5001}
	_, err := NewAggregatedOperationAuditRepo(source).List(context.Background(), OperationAuditFilter{Offset: 5000, Limit: 20})
	require.Error(t, err, "must reject a page beyond the per-source fetch window")
}

func TestAggregatedOperationAuditFetchesOnlyNeededPrefix(t *testing.T) {
	source := &aggregateRepoStub{store: "store"}
	_, err := NewAggregatedOperationAuditRepo(source).List(context.Background(), OperationAuditFilter{Offset: 20, Limit: 20})
	require.NoError(t, err)
	require.Equal(t, 0, source.filter.Offset)
	require.Equal(t, 40, source.filter.Limit)
}

func TestAggregatedOperationAuditRejectsUnidentifiedStore(t *testing.T) {
	source := &aggregateRepoStub{total: 1}
	_, err := NewAggregatedOperationAuditRepo(source).List(context.Background(), OperationAuditFilter{Limit: 20})
	require.ErrorIs(t, err, ErrOperationAuditUnavailable)
}

func TestAggregatedOperationAuditRejectsIncompleteOwnerPage(t *testing.T) {
	source := &aggregateRepoStub{store: "store", total: 2, items: []OperationAudit{{EventID: "one"}}}
	_, err := NewAggregatedOperationAuditRepo(source).List(context.Background(), OperationAuditFilter{Limit: 20})
	require.ErrorIs(t, err, ErrOperationAuditUnavailable)
}
