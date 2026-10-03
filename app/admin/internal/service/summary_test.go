package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	adminbiz "micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/authorization/management"
	"micro-one-api/domain/authorization/testutil"
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

type sectionIAMRepo struct {
	calls  []string
	scopes map[string]authorization.QueryScope
	err    error
}

func (r *sectionIAMRepo) Execute(context.Context, string, string, management.Request) (management.Response, error) {
	panic("unused")
}
func (r *sectionIAMRepo) ResourceAuthorization(_ context.Context, raw string, req authorization.ResourceRequest) (authorization.ResourceAuthorization, error) {
	if raw != "verified-session" {
		return authorization.ResourceAuthorization{}, status.Error(codes.Unauthenticated, "session invalid")
	}
	r.calls = append(r.calls, req.ExecutionPoint+":"+req.Operation)
	if r.err != nil {
		return authorization.ResourceAuthorization{}, r.err
	}
	return authorization.ResourceAuthorization{Mode: "iam", Query: r.scopes[req.Operation]}, nil
}
func TestSummaryPreflightNeverStartsRestrictedOrUnavailableSections(t *testing.T) {
	repo := &sectionIAMRepo{scopes: map[string]authorization.QueryScope{"identity.user.list": testutil.All(), "system.option.read": testutil.Resources(1)}}
	svc := NewAdminService(nil, nil, nil, nil)
	svc.SetIAMService(NewIAMAdminService(adminbiz.NewIAMUsecase(repo)))
	ctx := WithOperatorCredential(context.Background(), "verified-session")
	var invoked []string
	tasks := []summaryTask{}
	for _, name := range []string{"users", "channels", "pricing_options", "new_unclassified_section"} {
		tasks = append(tasks, summaryTask{name: name, run: func(context.Context) error { invoked = append(invoked, name); return nil }})
	}
	states := svc.runAuthorizedSummaryTasks(ctx, tasks)
	require.Equal(t, []string{"users"}, invoked)
	require.True(t, states["users"].Available)
	for _, name := range []string{"channels", "pricing_options", "new_unclassified_section"} {
		require.Equal(t, "restricted", states[name].Reason)
	}
	repo.err = status.Error(codes.Unavailable, "identity down")
	invoked = nil
	states = svc.runAuthorizedSummaryTasks(ctx, tasks[:1])
	require.Empty(t, invoked)
	require.Equal(t, "unavailable", states["users"].Reason)
}
func TestSummaryOwnerDenialRemainsRestricted(t *testing.T) {
	states := runSummaryTasks(context.Background(), time.Second, []summaryTask{{name: "owner_recheck", run: func(context.Context) error { return status.Error(codes.PermissionDenied, "revoked") }}})
	require.Equal(t, "restricted", states["owner_recheck"].Reason)
}
