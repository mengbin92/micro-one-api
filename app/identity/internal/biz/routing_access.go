package biz

import (
	"context"
	"fmt"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/platform/database/xdb"
	"time"
)

// RoutingAccessChange is an identity-owned command. Remote group references
// have already been checked by admin orchestration; no channel PO crosses here.
type RoutingAccessChange struct {
	ExpectedUserRevision, ExpectedPolicyRevision                  uint64
	Reason                                                        string
	UserID, ExpectedRevision, GroupID                             int64
	Operation, GroupKey, SourceType, SourceRef, PublicGroupAccess string
	StartsAt, ExpiresAt                                           int64
}
type RoutingAccessRepo interface {
	UserRoutingFacts(context.Context, int64) (*routing.SubjectFacts, error)
	UpdateRoutingAccess(context.Context, RoutingAccessChange) error
	SetTokenRouting(context.Context, int64, int64, string, int64, int64, []int64) (int64, error)
}

func (uc *IdentityUsecase) routingAccessRepo() (RoutingAccessRepo, error) {
	r, ok := uc.repo.(RoutingAccessRepo)
	if !ok || !RoutingV2Enabled() {
		return nil, ErrRoutingFactsUnavailable
	}
	return r, nil
}
func (uc *IdentityUsecase) UserRoutingFacts(ctx context.Context, userID int64) (*routing.SubjectFacts, error) {
	r, err := uc.routingAccessRepo()
	if err != nil {
		return nil, err
	}
	return r.UserRoutingFacts(ctx, userID)
}
func (uc *IdentityUsecase) UpdateRoutingAccess(ctx context.Context, c RoutingAccessChange) (*routing.SubjectFacts, error) {
	r, err := uc.routingAccessRepo()
	if err != nil {
		return nil, err
	}
	if c.UserID <= 0 || c.ExpectedRevision <= 0 {
		return nil, ErrRoutingAccessConflict
	}
	switch c.Operation {
	case "grant", "revoke":
		if c.GroupID <= 0 || (c.SourceType != "admin" && !(c.Operation == "revoke" && c.SourceType == "migration")) || c.SourceRef == "" || len(c.SourceRef) > 128 || c.StartsAt < 0 || c.ExpiresAt < 0 || (c.ExpiresAt > 0 && c.ExpiresAt <= c.StartsAt) {
			return nil, ErrRoutingDefaultInvalid
		}
	case "default":
		if c.GroupID <= 0 || c.GroupKey == "" {
			return nil, ErrRoutingDefaultInvalid
		}
	case "public_access":
		if c.PublicGroupAccess != "all" && c.PublicGroupAccess != "explicit_only" {
			return nil, ErrRoutingDefaultInvalid
		}
	default:
		return nil, ErrRoutingDefaultInvalid
	}
	mode, modeErr := uc.AuthorizationMode(ctx)
	if modeErr != nil {
		return nil, modeErr
	}
	if mode == "iam" {
		err = uc.updateIAMRoutingAccess(ctx, c)
	} else if uc.iam != nil {
		err = uc.runtimeWrite(ctx, "account.routing", fmt.Sprint(c.UserID), "legacy routing access mutation", authorization.Actor{ServiceID: "identity-legacy-account"}, func(ctx context.Context, tx IAMTx) error {
			p, err := uc.iam.Policy(ctx, tx)
			if err != nil {
				return err
			}
			if p.CheckWrite(authorization.LegacyAccountWrite, false) != nil {
				return ErrIAMCutoverBlocked
			}
			if err = uc.iam.UpdateRoutingAccessTx(ctx, tx, c); err != nil {
				return err
			}
			rev, err := uc.iam.UserRevision(ctx, tx, c.UserID)
			if err != nil {
				return err
			}
			if err = uc.iam.AdvanceUser(ctx, tx, c.UserID, rev); err != nil {
				return err
			}
			return uc.iam.AdvancePolicy(ctx, tx, p.PolicyRevision, false)
		})
	} else {
		err = r.UpdateRoutingAccess(ctx, c)
	}
	if err != nil {
		return nil, err
	}
	// The facts re-read runs in its own transaction, outside the retried
	// write above (data owns RetryTxOnBusy; biz never touches storage
	// clients). On the shared-file SQLite topology it can still collide with
	// a concurrent writer committing between its first read and the WAL
	// snapshot upgrade ("database is locked"), which previously surfaced to
	// the admin API as a spurious 503 right after a successful grant
	// (release run 35197009912, sessions phase). A read is idempotent, so
	// retry the typed transient here. MySQL row locks never trip the guard.
	var f *routing.SubjectFacts
	for attempt := 0; ; attempt++ {
		f, err = r.UserRoutingFacts(ctx, c.UserID)
		if err == nil || !xdb.IsSQLiteBusy(err) || attempt >= 2 {
			return f, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 10 * time.Millisecond):
		}
	}
}
func (uc *IdentityUsecase) SetTokenRouting(ctx context.Context, userID, tokenID int64, mode string, groupID, revision int64, groupIDs []int64) (int64, error) {
	r, err := uc.routingAccessRepo()
	if err != nil {
		return 0, err
	}
	if userID <= 0 || tokenID <= 0 || revision <= 0 {
		return 0, ErrRoutingDefaultInvalid
	}
	valid := routing.ValidPolicy(mode, groupID)
	if mode == "ordered" {
		valid = routing.ValidOrderedPolicy(mode, groupID, groupIDs)
	} else if len(groupIDs) > 0 {
		valid = false
	}
	if !valid {
		return 0, ErrRoutingDefaultInvalid
	}
	return r.SetTokenRouting(ctx, userID, tokenID, mode, groupID, revision, groupIDs)
}
