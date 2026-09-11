package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
	"testing"
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
