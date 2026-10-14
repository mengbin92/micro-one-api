package repair

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/upstream/contract"
)

func TestBoundedRolloutNeverPromotesByTimeOrCandidateClaims(t *testing.T) {
	p, _ := proposalFixture()
	d := p.Candidate
	now := time.Now().UTC()
	until := now.Add(time.Hour)
	d.Repair = &contract.RepairReview{BaseRevision: strings.Repeat("a", 64), BindingRevision: 3, IncidentRevision: 2, ExpiresAt: until.Unix()}
	base := RolloutState{Current: d.Repair.BaseRevision, Revision: 3, IncidentRevision: 2, Status: "active"}
	a, err := NextRollout(d, base, until, now)
	require.NoError(t, err)
	require.Equal(t, "publish", a.Action)
	base.Revision++
	_, err = NextRollout(d, base, until, now)
	require.Error(t, err)
	gray := RolloutState{Current: d.Revision(), Previous: d.Repair.BaseRevision, Revision: 4, IncidentRevision: 2, Status: "gray", StartedAt: now.Add(-31 * time.Minute).Unix(), CompleteAttempts: 99}
	a, err = NextRollout(d, gray, until, now)
	require.NoError(t, err)
	require.Equal(t, "wait", a.Action)
	gray.CompleteAttempts = 100
	a, err = NextRollout(d, gray, until, now)
	require.NoError(t, err)
	require.Equal(t, "promote", a.Action)
	gray.Failures = 1
	a, err = NextRollout(d, gray, until, now)
	require.NoError(t, err)
	require.Equal(t, "quarantine", a.Action)
	gray.Failures = 0
	a, err = NextRollout(d, gray, until, until)
	require.NoError(t, err)
	require.Equal(t, "quarantine", a.Action)
	gray.Status = "quarantined"
	a, err = NextRollout(d, gray, until, now)
	require.NoError(t, err)
	require.Equal(t, "rollback", a.Action)
	require.Equal(t, d.Repair.BaseRevision, a.Revision)
	gray.Previous = "removed-or-unapproved"
	_, err = NextRollout(d, gray, until, now)
	require.Error(t, err)
}
