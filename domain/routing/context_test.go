package routing

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResolveInheritedAccessFacts(t *testing.T) {
	group := &Group{ID: 9, Key: "Case", Status: "enabled", AccessMode: "public", Revision: 3}
	facts := &SubjectFacts{DefaultGroupID: 9, PublicGroupAccess: "explicit_only", AccessRevision: 1, TokenMode: "inherit", TokenRevision: 1}
	_, err := ResolveInherited(1, 2, facts, group, 100)
	require.Error(t, err, "default preference is not a grant")
	facts.Grants = []UserGroupGrant{{GroupID: 9, Status: "active", StartsAt: 90, ExpiresAt: 101}}
	resolved, err := ResolveInherited(1, 2, facts, group, 100)
	require.NoError(t, err)
	require.Equal(t, "Case", resolved.GroupKey)
	_, err = ResolveInherited(1, 2, facts, group, 101)
	require.Error(t, err, "expiry boundary is exclusive")
	facts.PublicGroupAccess = "all"
	_, err = ResolveInherited(1, 2, facts, group, 101)
	require.NoError(t, err)
	group.Status = "disabled"
	_, err = ResolveInherited(1, 2, facts, group, 100)
	require.Error(t, err, "disabled overrides every grant")
	group.Status = "enabled"
	facts.TokenMode = "fixed"
	facts.TokenGroupID = 9
	_, err = ResolveInherited(1, 2, facts, group, 100)
	require.Error(t, err)
}

func TestFixedRoutingEligibilityAndIsolation(t *testing.T) {
	f := &SubjectFacts{DefaultGroupID: 1, AccessRevision: 4, TokenMode: "fixed", TokenGroupID: 2, TokenRevision: 3, PublicGroupAccess: "explicit_only", Grants: []UserGroupGrant{{GroupID: 2, SourceType: "admin", SourceRef: "a", Status: "active", StartsAt: 100, ExpiresAt: 200}, {GroupID: 2, SourceType: "admin", SourceRef: "b", Status: "active"}}}
	g := &Group{ID: 2, Key: "VIP", Status: "enabled", AccessMode: "restricted", Revision: 5}
	c, err := Resolve(10, 20, f, g, 150)
	require.NoError(t, err)
	require.Equal(t, "token_fixed", c.SelectionSource)
	require.EqualValues(t, 2, c.GroupID)
	before := c.Digest()
	f.DefaultGroupID = 9
	after, err := Resolve(10, 20, f, g, 150)
	require.NoError(t, err)
	require.Equal(t, before, after.Digest())
	f.Grants[0].Status = "revoked"
	_, err = Resolve(10, 20, f, g, 200)
	require.NoError(t, err, "other source remains valid")
	f.Grants[1].ExpiresAt = 200
	_, err = Resolve(10, 20, f, g, 200)
	require.Error(t, err, "expiry is exclusive")
	g.AccessMode = "public"
	_, err = Resolve(10, 20, f, g, 200)
	require.Error(t, err)
	f.PublicGroupAccess = "all"
	_, err = Resolve(10, 20, f, g, 200)
	require.NoError(t, err)
	g.Status = "disabled"
	_, err = Resolve(10, 20, f, g, 200)
	require.Error(t, err)
	require.NotEqual(t, SessionKey(c, "conversation"), SessionKey(&ResolvedRoutingContext{UserID: 10, TokenID: 21, GroupID: 2}, "conversation"))
	for _, mode := range []string{"", "auto", "ordered"} {
		f.TokenMode = mode
		require.Zero(t, SelectedGroupID(f))
	}
}
