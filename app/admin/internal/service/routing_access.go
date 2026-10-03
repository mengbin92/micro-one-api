package service

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/filtering"
	"micro-one-api/pkg/ordering"
	"strconv"
)

type routingAccessUsecase interface {
	Facts(context.Context, int64) (*routing.SubjectFacts, error)
	Available(context.Context, int64, routing.GroupListRequest) (*biz.AvailableRoutingGroups, error)
	Change(context.Context, biz.RoutingAccessChange, bool) (*routing.SubjectFacts, error)
	CreateToken(context.Context, int64, string, string, int64, []int64) (*biz.RoutingToken, error)
	SetToken(context.Context, int64, int64, string, int64, int64, []int64) (int64, error)
}

func (s *AdminService) SetRoutingAccessUsecase(uc routingAccessUsecase) { s.routingAccessUc = uc }

type RoutingGrant struct {
	GroupID    int64  `json:"routing_group_id"`
	SourceType string `json:"source_type"`
	SourceRef  string `json:"source_ref"`
	StartsAt   int64  `json:"starts_at"`
	ExpiresAt  int64  `json:"expires_at"`
	Status     string `json:"status"`
}
type RoutingTokenReference struct {
	ID       int64   `json:"id"`
	Name     string  `json:"name"`
	Mode     string  `json:"mode"`
	GroupID  int64   `json:"group_id"`
	GroupIDs []int64 `json:"group_ids,omitempty"`
	Revision int64   `json:"revision"`
}
type RoutingFacts struct {
	AuthorizationRevision       uint64                  `json:"authorization_revision,string"`
	AuthorizationPolicyRevision uint64                  `json:"authorization_policy_revision,string"`
	Tokens                      []RoutingTokenReference `json:"tokens"`
	DefaultGroupID              int64                   `json:"default_routing_group_id"`
	Revision                    int64                   `json:"revision"`
	PublicGroupAccess           string                  `json:"public_group_access"`
	Grants                      []RoutingGrant          `json:"grants"`
}

func factsReply(f *routing.SubjectFacts) *RoutingFacts {
	out := &RoutingFacts{AuthorizationRevision: f.AuthorizationRevision, AuthorizationPolicyRevision: f.AuthorizationPolicyRevision, DefaultGroupID: f.DefaultGroupID, Revision: f.AccessRevision, PublicGroupAccess: f.PublicGroupAccess, Grants: []RoutingGrant{}}
	for _, t := range f.TokenReferences {
		out.Tokens = append(out.Tokens, RoutingTokenReference{ID: t.ID, Name: t.Name, Mode: t.Mode, GroupID: t.GroupID, GroupIDs: t.GroupIDs, Revision: t.Revision})
	}
	for _, g := range f.Grants {
		out.Grants = append(out.Grants, RoutingGrant{g.GroupID, g.SourceType, g.SourceRef, g.StartsAt, g.ExpiresAt, g.Status})
	}
	return out
}

type AvailableGroup struct {
	ID                  int64          `json:"id"`
	Key                 string         `json:"key"`
	DisplayName         string         `json:"display_name"`
	PriceRatio          float64        `json:"price_ratio"`
	PriceSource         string         `json:"price_source"`
	PriceVersion        string         `json:"price_version"`
	BillingMode         string         `json:"billing_mode"`
	SubscriptionCovered bool           `json:"subscription_covered"`
	Models              []string       `json:"models"`
	Sources             []RoutingGrant `json:"sources"`
	OrderedEligible     bool           `json:"ordered_eligible"`
	UserPriceRatio      float64        `json:"user_price_ratio,omitempty"`
	UserPriceVersion    int64          `json:"user_price_version,omitempty"`
}
type AvailableGroups struct {
	Groups           []AvailableGroup `json:"groups"`
	Facts            *RoutingFacts    `json:"facts"`
	DefaultAvailable bool             `json:"default_available"`
	NextPageToken    string           `json:"next_page_token"`
	CreationEnabled  bool             `json:"creation_enabled"`
}
type RoutingAccessRequest struct {
	ExpectedUserRevision   uint64 `json:"expected_user_revision,string"`
	ExpectedPolicyRevision uint64 `json:"expected_policy_revision,string"`
	Reason                 string `json:"reason"`
	ExpectedRevision       int64  `json:"expected_revision"`
	Operation              string `json:"operation"`
	GroupID                int64  `json:"routing_group_id"`
	SourceType             string `json:"source_type"`
	SourceRef              string `json:"source_ref"`
	StartsAt               int64  `json:"starts_at"`
	ExpiresAt              int64  `json:"expires_at"`
	PublicGroupAccess      string `json:"public_group_access"`
}
type RoutingTokenRequest struct {
	Name             string  `json:"name"`
	Mode             string  `json:"routing_mode"`
	GroupID          int64   `json:"routing_group_id"`
	GroupIDs         []int64 `json:"routing_group_ids,omitempty"`
	ExpectedRevision int64   `json:"expected_revision"`
}

func (s *AdminService) RoutingFacts(ctx context.Context, user int64) (*RoutingFacts, error) {
	ctx = operatorRPCContext(ctx)
	if s.routingAccessUc == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	f, err := s.routingAccessUc.Facts(operatorRPCContext(ctx), user)
	if err != nil {
		return nil, err
	}
	return factsReply(f), nil
}
func (s *AdminService) AvailableGroups(ctx context.Context, user int64, q routing.GroupListRequest) (*AvailableGroups, error) {
	ctx = operatorRPCContext(ctx)
	if s.routingAccessUc == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	if q.PageSize < 0 || q.PageSize > 200 || len(q.PageToken) > 512 {
		return nil, biz.ErrRoutingGroupInvalid
	}
	if _, err := filtering.Equalities(q.Filter, "key", "status", "access_mode"); err != nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	if _, err := ordering.Parse(q.OrderBy, "id", "key", "sort_order"); err != nil {
		return nil, biz.ErrRoutingGroupInvalid
	}
	a, err := s.routingAccessUc.Available(ctx, user, q)
	if err != nil {
		return nil, err
	}
	out := &AvailableGroups{Groups: []AvailableGroup{}, Facts: factsReply(a.Facts), DefaultAvailable: a.DefaultAvailable, NextPageToken: a.NextPageToken, CreationEnabled: a.CreationEnabled}
	for _, g := range a.Groups {
		sources := factsReply(&routing.SubjectFacts{Grants: g.Sources}).Grants
		out.Groups = append(out.Groups, AvailableGroup{
			ID: g.Group.ID, Key: g.Group.Key, DisplayName: g.Group.DisplayName,
			PriceRatio: g.Price.Ratio, PriceSource: g.Price.Source, PriceVersion: g.Price.Version,
			BillingMode: g.Price.BillingMode, SubscriptionCovered: g.Price.SubscriptionCovered,
			Models: g.Models, Sources: sources, OrderedEligible: g.OrderedEligible,
			UserPriceRatio: g.Price.UserRatio, UserPriceVersion: g.Price.UserVersion,
		})
	}
	return out, nil
}
func (s *AdminService) ChangeRoutingAccess(ctx context.Context, user int64, r RoutingAccessRequest, self bool) (*RoutingFacts, error) {
	ctx = operatorRPCContext(ctx)
	if s.routingAccessUc == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	f, err := s.routingAccessUc.Change(operatorRPCContext(ctx), biz.RoutingAccessChange{ExpectedUserRevision: r.ExpectedUserRevision, ExpectedPolicyRevision: r.ExpectedPolicyRevision, Reason: r.Reason, UserID: user, ExpectedRevision: r.ExpectedRevision, GroupID: r.GroupID, Operation: r.Operation, SourceType: r.SourceType, SourceRef: r.SourceRef, StartsAt: r.StartsAt, ExpiresAt: r.ExpiresAt, PublicGroupAccess: r.PublicGroupAccess}, self)
	if err != nil {
		return nil, err
	}
	return factsReply(f), nil
}
func (s *AdminService) CreateRoutingToken(ctx context.Context, user int64, r RoutingTokenRequest) (map[string]any, error) {
	ctx = operatorRPCContext(ctx)
	if s.routingAccessUc == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	t, err := s.routingAccessUc.CreateToken(ctx, user, r.Name, r.Mode, r.GroupID, r.GroupIDs)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": t.ID, "key": t.Key, "name": t.Name, "routing_mode": t.Mode, "routing_group_id": t.GroupID, "routing_group_ids": t.GroupIDs, "routing_revision": t.Revision, "status": 1, "created_time": t.CreatedAt}, nil
}
func (s *AdminService) SetRoutingToken(ctx context.Context, user, token int64, r RoutingTokenRequest) (map[string]any, error) {
	ctx = operatorRPCContext(ctx)
	if s.routingAccessUc == nil {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	rev, err := s.routingAccessUc.SetToken(ctx, user, token, r.Mode, r.GroupID, r.ExpectedRevision, r.GroupIDs)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": rev}, nil
}

type RoutingGroupStateRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Reason           string `json:"reason"`
	Status           string `json:"status"`
	AccessMode       string `json:"access_mode"`
}

func (s *AdminService) SetRoutingGroupState(ctx context.Context, id int64, r RoutingGroupStateRequest) error {
	ctx = operatorRPCContext(ctx)
	uc, ok := s.routingAccessUc.(interface {
		SetGroupState(context.Context, int64, int64, string, string) error
	})
	if !ok {
		return biz.ErrRoutingGroupUnavailable
	}
	ctx = operatorRPCContext(authorization.WithWriteReason(ctx, requestWriteReason(ctx, r.Reason)))
	return uc.SetGroupState(ctx, id, r.ExpectedRevision, r.Status, r.AccessMode)
}

type RoutingBillingPolicyDTO struct {
	GroupID     int64   `json:"routing_group_id"`
	Version     int64   `json:"version"`
	BillingMode string  `json:"billing_mode"`
	PriceRatio  float64 `json:"price_ratio"`
	EffectiveAt int64   `json:"effective_at"`
}

func (s *AdminService) RoutingBillingPolicy(ctx context.Context, id int64, update *RoutingBillingPolicyDTO) (*RoutingBillingPolicyDTO, error) {
	ctx = operatorRPCContext(ctx)
	uc, ok := s.routingAccessUc.(interface {
		BillingPolicy(context.Context, int64) (*routing.BillingPolicy, error)
		PublishBillingPolicy(context.Context, *routing.BillingPolicy, int64) error
	})
	if !ok {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	if update != nil {
		p := &routing.BillingPolicy{GroupID: id, BillingMode: update.BillingMode, PriceRatio: update.PriceRatio}
		if err := uc.PublishBillingPolicy(ctx, p, update.Version); err != nil {
			return nil, err
		}
		return &RoutingBillingPolicyDTO{GroupID: id, Version: p.Version, BillingMode: p.BillingMode, PriceRatio: p.PriceRatio, EffectiveAt: p.EffectiveAt}, nil
	}
	p, err := uc.BillingPolicy(ctx, id)
	if err != nil {
		return nil, err
	}
	return &RoutingBillingPolicyDTO{GroupID: id, Version: p.Version, BillingMode: p.BillingMode, PriceRatio: p.PriceRatio, EffectiveAt: p.EffectiveAt}, nil
}

type RoutingUserPriceRequest struct {
	PriceRatio      float64 `json:"price_ratio"`
	ExpectedVersion *int64  `json:"expected_version,string"`
	Reason          string  `json:"reason"`
}

// SetRoutingGroupUserPrice publishes (or clears, when ratio<=0 with clear=true
// via DELETE) a user-specific price override for one routing group.
func (s *AdminService) SetRoutingGroupUserPrice(ctx context.Context, groupID, userID int64, r RoutingUserPriceRequest) (map[string]any, error) {
	ctx = operatorRPCContext(ctx)
	uc, ok := s.routingAccessUc.(interface {
		SetUserRoutingPrice(context.Context, int64, int64, float64) (int64, error)
	})
	if !ok {
		return nil, biz.ErrRoutingGroupUnavailable
	}
	if r.ExpectedVersion != nil {
		ctx = routing.WithExpectedPriceVersion(ctx, *r.ExpectedVersion)
	}
	ctx = authorization.WithWriteReason(ctx, requestWriteReason(ctx, r.Reason))
	ctx = operatorRPCContext(ctx)
	version, err := uc.SetUserRoutingPrice(ctx, userID, groupID, r.PriceRatio)
	if err != nil {
		return nil, err
	}
	return map[string]any{"routing_group_id": groupID, "user_id": userID, "version": version}, nil
}

func (s *AdminService) ClearRoutingGroupUserPrice(ctx context.Context, groupID, userID int64) error {
	ctx = operatorRPCContext(ctx)
	uc, ok := s.routingAccessUc.(interface {
		ClearUserRoutingPrice(context.Context, int64, int64) error
	})
	if !ok {
		return biz.ErrRoutingGroupUnavailable
	}
	return uc.ClearUserRoutingPrice(ctx, userID, groupID)
}

type RoutingResourceOverrideRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	Reason           string `json:"reason"`
	SourceKind       string `json:"source_kind"`
	SourceID         int64  `json:"source_id"`
	PriorityOverride *int64 `json:"priority_override"`
	WeightOverride   *int64 `json:"weight_override"`
}

func (s *AdminService) SetRoutingGroupResourceOverrides(ctx context.Context, groupID int64, r RoutingResourceOverrideRequest) error {
	ctx = operatorRPCContext(ctx)
	uc, ok := s.routingAccessUc.(interface {
		SetResourceOverrides(context.Context, int64, routing.Source, *int64, *int64) error
	})
	if !ok {
		return biz.ErrRoutingGroupUnavailable
	}
	rev, err := strconv.ParseInt(r.ExpectedRevision, 10, 64)
	if authorization.External(ctx) && (err != nil || rev <= 0) {
		return status.Error(codes.InvalidArgument, "expected_revision required")
	}
	ctx = operatorRPCContext(authorization.WithWriteReason(routing.WithExpectedGroupRevision(ctx, rev), requestWriteReason(ctx, r.Reason)))
	return uc.SetResourceOverrides(ctx, groupID, routing.Source{Kind: r.SourceKind, ID: r.SourceID}, r.PriorityOverride, r.WeightOverride)
}
