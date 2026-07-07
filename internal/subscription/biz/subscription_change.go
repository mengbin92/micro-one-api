package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// Subscription change (upgrade/downgrade) semantics (phase 2.4).
//
// A subscription change moves a user from one plan/group to another. It is
// implemented on top of the renewal and reversal primitives so the wallet,
// ledger, and entitlement stay consistent:
//
//   - Upgrade (to a more expensive plan): the price difference is charged to
//     the wallet, a change ledger entry is written, and the subscription's
//     group_id / subscription_name / metadata are updated in place. The
//     expires_at is NOT extended (upgrades are plan changes, not renewals);
//     if the caller wants to also extend, it issues a separate renewal.
//   - Downgrade (to a cheaper plan): no refund is issued immediately; the
//     downgrade takes effect at the next renewal (next-cycle). This is the
//     safest default because an immediate downgrade that refunds the
//     difference would need the reversal path and risks clawing back
//     already-consumed quota. The "immediate" policy is supported but
//     requires the caller to have already run a reversal.
//
// Policy (fixed, recorded here):
//   - Upgrade: immediate, charge difference, keep expires_at.
//   - Downgrade: next-cycle (recorded as pending_change in metadata; applied
//     at the next renewal/assignment).
//   - Same-group plan change: treated as an upgrade if the new price is
//     higher, downgrade if lower.
//
// Invariant: a user always has at most one active subscription. A change
// mutates the existing active row in place; it never creates a second row.

const (
	SubscriptionChangePolicyImmediate = "immediate"
	SubscriptionChangePolicyNextCycle = "next_cycle"
)

// ChangeRequest is the input to ChangeSubscription.
type ChangeRequest struct {
	UserID             int64
	FromSubscriptionID int64
	ToPlanID           int64
	ToGroupID          int64
	NewPlanName        string
	NewPriceQuota      int64
	OldPriceQuota      int64
	Policy             string // immediate / next_cycle (empty = infer from price)
	Operator           string
	Now                int64 // override for tests
}

// ChangeResult summarises the change.
type ChangeResult struct {
	SubscriptionID      int64
	Applied             bool // false when deferred to next cycle
	ChargedQuota        int64
	NewGroupID          int64
	NewSubscriptionName string
	Policy              string
}

// ChangeSubscription mutates the user's active subscription in place according
// to the change policy. It is called by the admin/billing layer after the
// wallet charge (for upgrades) has been reserved. The caller is responsible
// for the wallet movement; this function only mutates the subscription row
// and records the audit metadata.
func (uc *SubscriptionUsecase) ChangeSubscription(ctx context.Context, req ChangeRequest) (*ChangeResult, error) {
	if uc == nil || uc.repo == nil {
		return nil, ErrSubscriptionChangeNotConfigured
	}
	if req.UserID <= 0 || req.ToGroupID <= 0 {
		return nil, fmt.Errorf("user_id and to_group_id are required")
	}
	sub, err := uc.repo.GetSubscriptionByID(ctx, req.FromSubscriptionID)
	if err != nil {
		return nil, err
	}
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	if sub.Status != SubscriptionStatusActive {
		return nil, ErrSubscriptionNotActive
	}
	if sub.UserID != req.UserID {
		return nil, fmt.Errorf("subscription %d does not belong to user %d", req.FromSubscriptionID, req.UserID)
	}

	policy := req.Policy
	if policy == "" {
		// Infer: more expensive = immediate upgrade; cheaper = next-cycle downgrade.
		if req.NewPriceQuota >= req.OldPriceQuota {
			policy = SubscriptionChangePolicyImmediate
		} else {
			policy = SubscriptionChangePolicyNextCycle
		}
	}

	now := req.Now
	if now == 0 {
		now = uc.now().Unix()
	}

	switch policy {
	case SubscriptionChangePolicyImmediate:
		// Mutate the active row in place. expires_at is preserved (a change
		// is not a renewal). The audit metadata records the from→to transition.
		fromGroupID := sub.GroupID
		sub.GroupID = req.ToGroupID
		if req.NewPlanName != "" {
			sub.SubscriptionName = req.NewPlanName
		}
		sub.Metadata = mergeChangeMetadata(sub.Metadata, changeAudit{
			FromPlanID:  req.OldPriceQuota, // reuse as from-plan ref when caller lacks id
			ToPlanID:    req.ToPlanID,
			FromGroupID: fromGroupID,
			ToGroupID:   req.ToGroupID,
			Policy:      policy,
			Charged:     req.NewPriceQuota - req.OldPriceQuota,
			Operator:    req.Operator,
			At:          now,
		})
		sub.UpdatedAt = now
		// Reset usage windows on a group change so the new group's limits
		// start fresh; the old group's usage no longer applies.
		sub.DailyUsageUSD = 0
		sub.WeeklyUsageUSD = 0
		sub.MonthlyUsageUSD = 0
		sub.DailyWindowStart = now
		sub.WeeklyWindowStart = now
		sub.MonthlyWindowStart = now
		if err := uc.repo.UpdateSubscription(ctx, sub); err != nil {
			return nil, err
		}
		return &ChangeResult{
			SubscriptionID:      sub.ID,
			Applied:             true,
			ChargedQuota:        req.NewPriceQuota - req.OldPriceQuota,
			NewGroupID:          sub.GroupID,
			NewSubscriptionName: sub.SubscriptionName,
			Policy:              policy,
		}, nil

	case SubscriptionChangePolicyNextCycle:
		// Record the pending change in metadata; do not mutate group_id yet.
		// The next AssignOrExtend (renewal) will read pending_change and
		// apply the new group.
		sub.Metadata = mergePendingChangeMetadata(sub.Metadata, changeAudit{
			ToPlanID:  req.ToPlanID,
			ToGroupID: req.ToGroupID,
			Policy:    policy,
			Operator:  req.Operator,
			At:        now,
		})
		sub.UpdatedAt = now
		if err := uc.repo.UpdateSubscription(ctx, sub); err != nil {
			return nil, err
		}
		return &ChangeResult{
			SubscriptionID: sub.ID,
			Applied:        false,
			NewGroupID:     req.ToGroupID,
			Policy:         policy,
		}, nil

	default:
		return nil, fmt.Errorf("unknown change policy %q", policy)
	}
}

type changeAudit struct {
	FromPlanID  int64  `json:"from_plan_id,omitempty"`
	ToPlanID    int64  `json:"to_plan_id,omitempty"`
	FromGroupID int64  `json:"from_group_id,omitempty"`
	ToGroupID   int64  `json:"to_group_id,omitempty"`
	Policy      string `json:"policy"`
	Charged     int64  `json:"charged,omitempty"`
	Operator    string `json:"operator,omitempty"`
	At          int64  `json:"at"`
}

func mergeChangeMetadata(existing string, audit changeAudit) string {
	var obj map[string]json.RawMessage
	trimmed := trimSpace(existing)
	if trimmed != "" {
		_ = json.Unmarshal([]byte(trimmed), &obj)
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(audit)
	obj["last_change"] = b
	out, _ := json.Marshal(obj)
	return string(out)
}

func mergePendingChangeMetadata(existing string, audit changeAudit) string {
	var obj map[string]json.RawMessage
	trimmed := trimSpace(existing)
	if trimmed != "" {
		_ = json.Unmarshal([]byte(trimmed), &obj)
	}
	if obj == nil {
		obj = map[string]json.RawMessage{}
	}
	b, _ := json.Marshal(audit)
	obj["pending_change"] = b
	out, _ := json.Marshal(obj)
	return string(out)
}

func pendingChangeMetadata(existing string) (changeAudit, bool) {
	var obj map[string]json.RawMessage
	trimmed := trimSpace(existing)
	if trimmed == "" {
		return changeAudit{}, false
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return changeAudit{}, false
	}
	raw, ok := obj["pending_change"]
	if !ok {
		return changeAudit{}, false
	}
	var audit changeAudit
	if err := json.Unmarshal(raw, &audit); err != nil {
		return changeAudit{}, false
	}
	return audit, audit.ToGroupID > 0
}

func clearPendingChangeMetadata(existing string) string {
	var obj map[string]json.RawMessage
	trimmed := trimSpace(existing)
	if trimmed == "" {
		return existing
	}
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return existing
	}
	delete(obj, "pending_change")
	if len(obj) == 0 {
		return ""
	}
	out, _ := json.Marshal(obj)
	return string(out)
}

// ErrSubscriptionChangeNotConfigured is returned when the change usecase has no repo.
var ErrSubscriptionChangeNotConfigured = errors.New("subscription change service not configured")

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
