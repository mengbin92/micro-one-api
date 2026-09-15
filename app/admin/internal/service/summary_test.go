package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	adminbiz "micro-one-api/app/admin/internal/biz"
)

func TestSummaryTaskTimeoutPreservesHealthySiblings(t *testing.T) {
	var healthy bool
	start := time.Now()
	states := runSummaryTasks(context.Background(), 20*time.Millisecond, []summaryTask{
		{name: "slow", run: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
		{name: "healthy", run: func(context.Context) error { healthy = true; return nil }},
		{name: "failed", run: func(context.Context) error { return errors.New("backend unavailable") }},
	})
	require.True(t, healthy)
	require.True(t, states["healthy"].Available)
	require.Equal(t, "timeout", states["slow"].Reason)
	require.Equal(t, "unavailable", states["failed"].Reason)
	require.Less(t, time.Since(start), time.Second)
}

func TestSummaryRequestBudgetIncludesQueueAndJoinsWorkers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	var active, started atomic.Int32
	tasks := make([]summaryTask, 20)
	for i := range tasks {
		tasks[i] = summaryTask{name: string(rune('a' + i)), run: func(ctx context.Context) error {
			started.Add(1)
			active.Add(1)
			defer active.Add(-1)
			<-ctx.Done()
			return ctx.Err()
		}}
	}
	start := time.Now()
	states := runSummaryTasks(ctx, time.Second, tasks)
	require.EqualValues(t, summaryConcurrency, started.Load(), "queued jobs must not call a dependency after cancellation")
	require.Zero(t, active.Load(), "no background work may outlive the request")
	require.Len(t, states, len(tasks))
	for _, state := range states {
		require.Equal(t, "timeout", state.Reason)
	}
	require.Less(t, time.Since(start), time.Second)
}

func TestSummaryAlreadyCanceledDoesNotCallDependencies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	states := runSummaryTasks(ctx, time.Second, []summaryTask{{name: "queued", run: func(context.Context) error {
		t.Error("dependency invoked after client canceled")
		return nil
	}}})
	require.Equal(t, "canceled", states["queued"].Reason)
}

type summaryOptionsRepo struct {
	keys   []string
	values map[string]string
	err    error
}

func (r *summaryOptionsRepo) Get(ctx context.Context, key string) (string, error) {
	r.keys = append(r.keys, key)
	return r.values[key], r.err
}
func (*summaryOptionsRepo) Set(context.Context, string, string) error { return nil }

func TestSummaryPricingOptionsScopeAndFailures(t *testing.T) {
	repo := &summaryOptionsRepo{values: map[string]string{"QuotaPerUnit": "12345"}}
	svc := NewAdminService(nil, nil, nil, adminbiz.NewSystemOptionsUsecase(repo))
	options, err := svc.summaryPricingOptions(context.Background())
	require.NoError(t, err)
	require.Len(t, options, 5)
	require.Equal(t, OneAPIOption{Key: "AmountPerUnit", Value: "12345"}, options[4])
	require.Equal(t, []string{"ModelRatio", "CompletionRatio", "ModelPrice", "GroupRatio", "AmountPerUnit", "QuotaPerUnit"}, repo.keys)
	repo.err = errors.New("config unavailable")
	options, err = svc.summaryPricingOptions(context.Background())
	require.ErrorIs(t, err, repo.err)
	require.Nil(t, options, "failed configuration reads must not become successful defaults")
}
