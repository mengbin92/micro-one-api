package biz

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
	"testing"
)

type accessRepoFake struct {
	RoutingAccessRepo
	facts     *routing.SubjectFacts
	writes    int
	capErr    error
	lastGroup int64
}

func (r *accessRepoFake) Facts(context.Context, int64) (*routing.SubjectFacts, error) {
	return r.facts, nil
}
func (r *accessRepoFake) CheckCapabilities(context.Context) error { return r.capErr }
func (r *accessRepoFake) CreateToken(_ context.Context, _ int64, _ string, _ string, id int64, groupIDs []int64) (*RoutingToken, error) {
	r.writes++
	r.lastGroup = id
	return &RoutingToken{ID: 1, GroupID: id, GroupIDs: groupIDs}, nil
}
func (r *accessRepoFake) SetToken(_ context.Context, _ int64, _ int64, _ string, _ int64, _ int64, _ []int64) (int64, error) {
	r.writes++
	return 2, nil
}
func (r *accessRepoFake) Change(_ context.Context, c RoutingAccessChange) (*routing.SubjectFacts, error) {
	r.writes++
	return r.facts, nil
}
func (r *accessRepoFake) Price(context.Context, int64, int64) (RoutingPrice, error) {
	return RoutingPrice{Ratio: 2, BillingMode: "subscription_first"}, nil
}
func (r *accessRepoFake) Models(context.Context, int64, string) ([]string, error) {
	return []string{"m"}, nil
}

type accessGroupsFake struct {
	RoutingGroupReader
	groups []*routing.Group
}

func (r accessGroupsFake) Get(_ context.Context, id int64) (*routing.GroupDetail, error) {
	for _, g := range r.groups {
		if g.ID == id {
			return &routing.GroupDetail{Group: g}, nil
		}
	}
	return nil, ErrRoutingGroupInvalid
}
func (r accessGroupsFake) List(context.Context, routing.GroupListRequest) (*routing.GroupListResult, error) {
	return &routing.GroupListResult{Groups: r.groups}, nil
}
func TestRoutingAccessOrchestration(t *testing.T) {
	t.Setenv("ADMIN_ROUTING_FIXED_KEYS", "true")
	ctx := context.Background()
	r := &accessRepoFake{facts: &routing.SubjectFacts{DefaultGroupID: 1, AccessRevision: 1, PublicGroupAccess: "explicit_only", Grants: []routing.UserGroupGrant{{GroupID: 2, Status: "active"}}}}
	g := accessGroupsFake{groups: []*routing.Group{{ID: 1, Key: "default", Status: "enabled"}, {ID: 2, Key: "vip", Status: "enabled"}, {ID: 3, Key: "public", Status: "enabled", AccessMode: "public"}}}
	uc := NewRoutingAccessUsecase(g, r)
	_, err := uc.CreateToken(ctx, 10, "k", "fixed", 3, nil)
	require.Error(t, err)
	require.Zero(t, r.writes)
	_, err = uc.CreateToken(ctx, 10, "k", "inherit", 0, nil)
	require.Error(t, err, "default alone grants nothing")
	_, err = uc.CreateToken(ctx, 10, "k", "fixed", 2, nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, r.lastGroup)
	_, err = uc.CreateToken(ctx, 10, "k", "inherit", 2, nil)
	require.Error(t, err)
	r.capErr = fmt.Errorf("old billing")
	_, err = uc.CreateToken(ctx, 10, "k", "fixed", 2, nil)
	require.Error(t, err)
	require.Equal(t, 1, r.writes)
	r.capErr = nil
	list, err := uc.Available(ctx, 10, routing.GroupListRequest{})
	require.NoError(t, err)
	require.Len(t, list.Groups, 1)
	require.False(t, list.DefaultAvailable)
	_, err = uc.Change(ctx, RoutingAccessChange{UserID: 10, Operation: "grant", GroupID: 2}, true)
	require.Error(t, err)
	g.groups[1].Status = "disabled"
	_, err = uc.CreateToken(ctx, 10, "k", "fixed", 2, nil)
	require.Error(t, err)

	// Closing creation must retain the directory, inherit creation and revoke.
	g.groups[1].Status = "enabled"
	r.facts.DefaultGroupID = 2
	t.Setenv("ADMIN_ROUTING_FIXED_KEYS", "false")
	_, err = uc.CreateToken(ctx, 10, "k", "fixed", 2, nil)
	require.Error(t, err)
	_, err = uc.CreateToken(ctx, 10, "k", "inherit", 0, nil)
	require.NoError(t, err)
	_, err = uc.Change(ctx, RoutingAccessChange{UserID: 10, Operation: "grant", GroupID: 2}, false)
	require.Error(t, err)
	_, err = uc.Change(ctx, RoutingAccessChange{UserID: 10, Operation: "revoke", GroupID: 2}, false)
	require.NoError(t, err)
	list, err = uc.Available(ctx, 10, routing.GroupListRequest{})
	require.NoError(t, err)
	require.False(t, list.CreationEnabled)
	require.Len(t, list.Groups, 1)
	require.Equal(t, 3, r.writes)
}

func TestOrderedTokenCreationValidation(t *testing.T) {
	t.Setenv("ADMIN_ROUTING_FIXED_KEYS", "true")
	ctx := context.Background()
	r := &accessRepoFake{facts: &routing.SubjectFacts{DefaultGroupID: 1, AccessRevision: 1, PublicGroupAccess: "explicit_only", Grants: []routing.UserGroupGrant{{GroupID: 2, Status: "active"}}}}
	g := accessGroupsFake{groups: []*routing.Group{
		{ID: 1, Key: "default", Status: "enabled"},
		{ID: 2, Key: "vip", Status: "enabled"},
		{ID: 3, Key: "archive", Status: "archived"},
		{ID: 4, Key: "plain", Status: "enabled"},
	}}
	uc := NewRoutingAccessUsecase(g, r)

	// Gate off by default.
	_, err := uc.CreateToken(ctx, 10, "k", "ordered", 0, []int64{2})
	require.ErrorIs(t, err, ErrRoutingGroupUnavailable)
	require.Zero(t, r.writes)

	t.Setenv("ADMIN_ROUTING_ORDERED_KEYS", "true")

	// Ordered requires a valid explicit list and no fixed group id.
	_, err = uc.CreateToken(ctx, 10, "k", "ordered", 2, nil)
	require.ErrorIs(t, err, ErrRoutingGroupInvalid)
	_, err = uc.CreateToken(ctx, 10, "k", "ordered", 0, []int64{2, 2})
	require.ErrorIs(t, err, ErrRoutingGroupInvalid)
	_, err = uc.CreateToken(ctx, 10, "k", "inherit", 0, []int64{2})
	require.ErrorIs(t, err, ErrRoutingGroupInvalid, "non-ordered modes reject a candidate list")
	require.Zero(t, r.writes)

	// At least one currently eligible group; pre-listed inaccessible groups
	// are allowed and become effective once granted.
	tok, err := uc.CreateToken(ctx, 10, "k", "ordered", 0, []int64{2, 4})
	require.NoError(t, err)
	require.Equal(t, []int64{2, 4}, tok.GroupIDs)
	require.EqualValues(t, 1, r.writes)

	// Archived or unknown groups invalidate the list.
	_, err = uc.CreateToken(ctx, 10, "k", "ordered", 0, []int64{2, 3})
	require.ErrorIs(t, err, ErrRoutingGroupInvalid)
	_, err = uc.CreateToken(ctx, 10, "k", "ordered", 0, []int64{2, 9})
	require.ErrorIs(t, err, ErrRoutingGroupInvalid)

	// A list with no currently eligible group is denied.
	_, err = uc.CreateToken(ctx, 10, "k", "ordered", 0, []int64{4})
	require.ErrorIs(t, err, ErrRoutingAccessDenied)

	// SetToken follows the same rules.
	_, err = uc.SetToken(ctx, 10, 1, "ordered", 0, 1, []int64{2, 4})
	require.NoError(t, err)
	_, err = uc.SetToken(ctx, 10, 1, "fixed", 0, 1, []int64{2})
	require.ErrorIs(t, err, ErrRoutingGroupInvalid)
}
