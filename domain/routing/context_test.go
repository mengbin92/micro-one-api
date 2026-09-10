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
