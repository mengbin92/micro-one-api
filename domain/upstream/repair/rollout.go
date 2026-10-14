package repair

import (
	"fmt"
	"time"

	"micro-one-api/domain/upstream/contract"
)

// RolloutState contains owner facts, never a candidate's assertion that its
// tests passed. The owner still independently gates every CAS transition.
type RolloutState struct {
	Current, Previous, Status             string
	Revision, IncidentRevision            int64
	StartedAt, CompleteAttempts, Failures int64
}
type RolloutDecision struct {
	Action   string
	Revision string
	Expected uint64
}

func NextRollout(d contract.Document, state RolloutState, until time.Time, now time.Time) (RolloutDecision, error) {
	r := d.Repair
	if r == nil || until.Unix() > r.ExpiresAt || state.Revision < 0 {
		return RolloutDecision{}, fmt.Errorf("bounded reviewed repair rollout required")
	}
	target := d.Revision()
	decision := RolloutDecision{Revision: target, Expected: uint64(state.Revision)}
	if state.Current != target {
		if state.Current != r.BaseRevision || state.Revision != r.BindingRevision || state.IncidentRevision != r.IncidentRevision || !now.Before(until) {
			return decision, fmt.Errorf("rollout base changed or authorization expired")
		}
		decision.Action = "publish"
		return decision, nil
	}
	if state.Status == "active" {
		decision.Action = "complete"
		return decision, nil
	}
	if state.Status == "quarantined" {
		if state.Previous == "" || state.Previous != r.BaseRevision {
			return decision, fmt.Errorf("no authorized rollback target; keep isolation")
		}
		decision.Action, decision.Revision = "rollback", state.Previous
		return decision, nil
	}
	if state.Status != "gray" {
		return decision, fmt.Errorf("unexpected owner rollout state")
	}
	if state.Failures > 0 || !now.Before(until) {
		decision.Action = "quarantine"
		return decision, nil
	}
	decision.Action = "wait"
	if now.Unix()-state.StartedAt >= 1800 && state.CompleteAttempts >= 100 {
		decision.Action = "promote"
	}
	return decision, nil
}
