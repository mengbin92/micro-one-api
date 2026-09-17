package biz

import (
	"context"
	"errors"
	"testing"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
)

type routingAccessRepoFake struct {
	IdentityRepo
	writes int
}

func (r *routingAccessRepoFake) UserRoutingFacts(context.Context, int64) (*routing.SubjectFacts, error) {
	return &routing.SubjectFacts{AccessRevision: 2}, nil
}
func (r *routingAccessRepoFake) UpdateRoutingAccess(context.Context, RoutingAccessChange) error {
	r.writes++
	return nil
}
func (r *routingAccessRepoFake) SetTokenRouting(context.Context, int64, int64, string, int64, int64, []int64) (int64, error) {
	r.writes++
	return 2, nil
}
func TestRoutingAccessLocalConstraints(t *testing.T) {
	t.Setenv("IDENTITY_ROUTING_V2", "true")
	r := &routingAccessRepoFake{}
	uc := &IdentityUsecase{repo: r}
	ctx := context.Background()
	for _, c := range []RoutingAccessChange{{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 2, SourceType: "subscription", SourceRef: "forged"}, {UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 2, SourceType: "migration", SourceRef: "legacy_group"}, {UserID: 1, ExpectedRevision: 1, Operation: "default"}, {UserID: 1, ExpectedRevision: 1, Operation: "public_access", PublicGroupAccess: "anything"}, {UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 2, SourceType: "admin", SourceRef: "a", StartsAt: 100, ExpiresAt: 99}} {
		_, err := uc.UpdateRoutingAccess(ctx, c)
		require.Error(t, err)
	}
	for _, p := range []struct {
		mode string
		id   int64
		ids  []int64
	}{{"fixed", 0, nil}, {"inherit", 2, nil}, {"inherit", 0, []int64{2}}, {"auto", 0, nil}, {"ordered", 2, []int64{2}}, {"ordered", 0, nil}, {"ordered", 0, []int64{2, 2}}, {"ordered", 0, []int64{0}}, {"", 0, nil}} {
		_, err := uc.SetTokenRouting(ctx, 1, 2, p.mode, p.id, 1, p.ids)
		require.Error(t, err)
	}
	require.Zero(t, r.writes)
	_, err := uc.UpdateRoutingAccess(ctx, RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 2, SourceType: "admin", SourceRef: "a"})
	require.NoError(t, err)
	_, err = uc.SetTokenRouting(ctx, 1, 2, "fixed", 2, 1, nil)
	require.NoError(t, err)
	_, err = uc.SetTokenRouting(ctx, 1, 2, "ordered", 0, 1, []int64{2, 3})
	require.NoError(t, err)
	require.Equal(t, 3, r.writes)
}

type routingAccessBusyFactsFake struct {
	IdentityRepo
	busyLeft int
	reads    int
	writes   int
}

func (r *routingAccessBusyFactsFake) UserRoutingFacts(context.Context, int64) (*routing.SubjectFacts, error) {
	r.reads++
	if r.busyLeft > 0 {
		r.busyLeft--
		return nil, sqlite3.Error{Code: sqlite3.ErrBusy}
	}
	return &routing.SubjectFacts{AccessRevision: 2}, nil
}
func (r *routingAccessBusyFactsFake) UpdateRoutingAccess(context.Context, RoutingAccessChange) error {
	r.writes++
	return nil
}
func (r *routingAccessBusyFactsFake) SetTokenRouting(context.Context, int64, int64, string, int64, int64, []int64) (int64, error) {
	return 2, nil
}

// Regression for release run 35197009912 (sqlite3 sessions phase): the write
// commit succeeded but the follow-up facts re-read collided with a concurrent
// writer on the shared SQLite file ("database is locked") and surfaced as a
// spurious 503. The re-read must retry the typed busy error.
func TestUpdateRoutingAccessRetriesBusyFactsRead(t *testing.T) {
	t.Setenv("IDENTITY_ROUTING_V2", "true")
	r := &routingAccessBusyFactsFake{busyLeft: 2}
	uc := &IdentityUsecase{repo: r}
	f, err := uc.UpdateRoutingAccess(context.Background(), RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 2, SourceType: "admin", SourceRef: "fixture"})
	require.NoError(t, err)
	require.EqualValues(t, 2, f.AccessRevision)
	require.Equal(t, 1, r.writes, "write runs exactly once")
	require.Equal(t, 3, r.reads, "two busy reads then a successful re-read")
}

// Non-transient errors must not be retried: a conflict after the write must
// surface immediately without extra reads.
func TestUpdateRoutingAccessDoesNotRetryPermanentFactsError(t *testing.T) {
	t.Setenv("IDENTITY_ROUTING_V2", "true")
	r := &routingAccessBusyFactsFake{}
	// Swap in a facts reader that always fails with a non-busy error.
	perm := &routingAccessPermanentErrorFake{routingAccessBusyFactsFake: r}
	uc := &IdentityUsecase{repo: perm}
	_, err := uc.UpdateRoutingAccess(context.Background(), RoutingAccessChange{UserID: 1, ExpectedRevision: 1, Operation: "grant", GroupID: 2, SourceType: "admin", SourceRef: "fixture"})
	require.Error(t, err)
	require.Equal(t, 1, perm.reads, "permanent errors surface after the first read")
}

type routingAccessPermanentErrorFake struct {
	*routingAccessBusyFactsFake
}

func (r *routingAccessPermanentErrorFake) UserRoutingFacts(context.Context, int64) (*routing.SubjectFacts, error) {
	r.reads++
	return nil, errors.New("connection refused")
}
