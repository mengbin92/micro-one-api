package biz

import (
	"context"
	"errors"
	"strings"
	"time"

	"micro-one-api/pkg/wildcard"
)

// Model routing (P2 #3): model→specified subscription account routing.
//
// Mirrors sub2api Group.ModelRouting (map[string][]int64). Each row pins a
// model name (or wildcard pattern) within a group to a subscription account,
// overriding the normal priority-tier + weighted/random selection. When a
// routing row matches the requested model, SelectSubscriptionAccount
// restricts its candidate tier to the routed account set (still respecting
// status/quota/runtime-blocked), so the request goes to the
// operator-chosen upstream provider.
//
// See docs/model-management-design.md §9.3 #3 / §9.4 P2.

// ModelRouting is the domain object for a model→account routing override.
// It is a pure biz model (no proto, no storage tags).
type ModelRouting struct {
	ID                    int64
	GroupName             string
	Model                 string // client model name or wildcard pattern (e.g. claude-*, *)
	Platform              string // optional platform filter; empty = any platform
	SubscriptionAccountID int64
	Enabled               bool
	Priority              int32 // within a routed tier, higher wins
	CreatedAt             int64
	UpdatedAt             int64
}

var ErrModelRoutingNotFound = errors.New("model routing not found")

// ModelRoutingRepo is the repository interface for model routings, declared
// in biz (the inversion seam) and implemented by data. Kept separate from
// ChannelRepo and ModelRepo so the routing domain can evolve independently.
type ModelRoutingRepo interface {
	ListModelRoutings(ctx context.Context, group, model, platform string) ([]*ModelRouting, error)
	// ListModelRoutingsForSelect returns enabled routing rows that may match a
	// requested (group, model, platform) tuple. Exact rows are returned first,
	// then wildcard-pattern rows, so the caller can apply the same
	// exact-before-wildcard precedence used elsewhere. platform="" means any
	// platform. See docs/model-management-design.md §9.3 #3.
	ListModelRoutingsForSelect(ctx context.Context, group, model, platform string) ([]*ModelRouting, error)
	UpsertModelRouting(ctx context.Context, r *ModelRouting) error
	DeleteModelRouting(ctx context.Context, id int64) error
}

// ModelRoutingUsecase wraps ModelRoutingRepo with domain-level operations.
type ModelRoutingUsecase struct {
	repo ModelRoutingRepo
	now  func() time.Time
}

// NewModelRoutingUsecase creates a new ModelRoutingUsecase.
func NewModelRoutingUsecase(repo ModelRoutingRepo) *ModelRoutingUsecase {
	return &ModelRoutingUsecase{repo: repo, now: time.Now}
}

func (uc *ModelRoutingUsecase) timestamp() int64 {
	if uc == nil || uc.now == nil {
		return time.Now().Unix()
	}
	return uc.now().Unix()
}

// ListModelRoutings returns routing rows matching the optional filters.
func (uc *ModelRoutingUsecase) ListModelRoutings(ctx context.Context, group, model, platform string) ([]*ModelRouting, error) {
	if uc == nil || uc.repo == nil {
		return nil, nil
	}
	return uc.repo.ListModelRoutings(ctx, group, model, platform)
}

// UpsertModelRouting creates or updates a routing row. group/model/account
// are required; platform defaults to "" (any platform).
func (uc *ModelRoutingUsecase) UpsertModelRouting(ctx context.Context, r *ModelRouting) error {
	if uc == nil || uc.repo == nil {
		return errors.New("model routing usecase not configured")
	}
	if r.GroupName == "" {
		r.GroupName = "default"
	}
	if r.Model == "" {
		return errors.New("model is required")
	}
	if r.SubscriptionAccountID <= 0 {
		return errors.New("subscription_account_id is required")
	}
	now := uc.timestamp()
	if r.CreatedAt == 0 {
		r.CreatedAt = now
	}
	r.UpdatedAt = now
	return uc.repo.UpsertModelRouting(ctx, r)
}

// DeleteModelRouting removes a routing row by id.
func (uc *ModelRoutingUsecase) DeleteModelRouting(ctx context.Context, id int64) error {
	if uc == nil || uc.repo == nil {
		return ErrModelRoutingNotFound
	}
	if id <= 0 {
		return ErrModelRoutingNotFound
	}
	return uc.repo.DeleteModelRouting(ctx, id)
}

// RoutingMatchForSelect returns the enabled routing rows that match a
// requested model, applying exact-before-wildcard precedence (same rule as
// abilities/mapping). Returns nil when no routing matches — callers then
// fall back to normal priority selection. The caller is the channel biz
// SelectSubscriptionAccount path; this helper lives in biz so the precedence
// rule is owned by the domain and unit-testable without a repo.
func RoutingMatchForSelect(rows []*ModelRouting, model string) []*ModelRouting {
	if len(rows) == 0 {
		return nil
	}
	// Exact (case-insensitive) match first.
	var exact []*ModelRouting
	for _, r := range rows {
		if strings.EqualFold(r.Model, model) {
			exact = append(exact, r)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	// Specific wildcard patterns (non-"*") before the "*" catch-all.
	var specific, catchAll []*ModelRouting
	for _, r := range rows {
		if !wildcard.IsPattern(r.Model) {
			continue
		}
		if r.Model == "*" {
			catchAll = append(catchAll, r)
			continue
		}
		if wildcard.Match(r.Model, model) {
			specific = append(specific, r)
		}
	}
	if len(specific) > 0 {
		return specific
	}
	return catchAll
}
