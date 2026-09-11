package biz

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"os"
	"strings"
	"time"
)

var ErrRoutingAccessDenied = errors.Forbidden(identityv1.RoutingIdentityErrorReason_ROUTING_ACCESS_DENIED.String(), "无权使用此分组")

type RoutingPrice struct {
	Ratio                        float64
	Version, Source, BillingMode string
	SubscriptionCovered          bool
}
type AvailableRoutingGroup struct {
	Group   *routing.Group
	Sources []routing.UserGroupGrant
	Price   RoutingPrice
	Models  []string
}
type AvailableRoutingGroups struct {
	Groups           []AvailableRoutingGroup
	Facts            *routing.SubjectFacts
	DefaultAvailable bool
	NextPageToken    string
	CreationEnabled  bool
}
type RoutingAccessChange struct {
	UserID, ExpectedRevision, GroupID                             int64
	Operation, GroupKey, SourceType, SourceRef, PublicGroupAccess string
	StartsAt, ExpiresAt                                           int64
}
type RoutingToken struct {
	ID                           int64
	Key, Name, Mode              string
	GroupID, Revision, CreatedAt int64
}
type RoutingAccessRepo interface {
	Facts(context.Context, int64) (*routing.SubjectFacts, error)
	Change(context.Context, RoutingAccessChange) (*routing.SubjectFacts, error)
	CreateToken(context.Context, int64, string, string, int64) (*RoutingToken, error)
	SetToken(context.Context, int64, int64, string, int64, int64) (int64, error)
	Price(context.Context, int64, int64) (RoutingPrice, error)
	Models(context.Context, int64, string) ([]string, error)
	CheckCapabilities(context.Context) error
}
type RoutingEntitlementReader interface {
	GetRoutingEntitlements(context.Context, int64) (*routing.EntitlementFacts, error)
}

func (uc *RoutingAccessUsecase) SetRoutingEntitlements(r RoutingEntitlementReader) {
	uc.entitlements = r
}

type RoutingAccessUsecase struct {
	entitlements RoutingEntitlementReader
	groups       RoutingGroupReader
	repo         RoutingAccessRepo
}

func NewRoutingAccessUsecase(groups RoutingGroupReader, repo RoutingAccessRepo) *RoutingAccessUsecase {
	return &RoutingAccessUsecase{groups: groups, repo: repo}
}
func fixedCreationEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ADMIN_ROUTING_FIXED_KEYS")), "true")
}
func (uc *RoutingAccessUsecase) Facts(ctx context.Context, userID int64) (*routing.SubjectFacts, error) {
	return uc.effectiveFacts(ctx, userID)
}
func (uc *RoutingAccessUsecase) eligible(ctx context.Context, userID, groupID int64) (*routing.Group, error) {
	f, err := uc.effectiveFacts(ctx, userID)
	if err != nil {
		return nil, err
	}
	if groupID == 0 {
		groupID = f.DefaultGroupID
	}
	d, err := uc.groups.Get(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if d == nil || len(routing.AccessSources(f, d.Group, time.Now().Unix())) == 0 {
		return nil, ErrRoutingAccessDenied
	}
	return d.Group, nil
}
func (uc *RoutingAccessUsecase) Available(ctx context.Context, userID int64, q routing.GroupListRequest) (*AvailableRoutingGroups, error) {
	f, err := uc.effectiveFacts(ctx, userID)
	if err != nil {
		return nil, err
	}
	page, err := uc.groups.List(ctx, q)
	if err != nil {
		return nil, err
	}
	out := &AvailableRoutingGroups{Facts: f, Groups: []AvailableRoutingGroup{}, NextPageToken: page.NextPageToken, CreationEnabled: fixedCreationEnabled()}
	// Default validity is independent of the current page and of payment state.
	d, err := uc.groups.Get(ctx, f.DefaultGroupID)
	if err != nil && errors.FromError(err).Code != 404 {
		return nil, err
	}
	out.DefaultAvailable = d != nil && len(routing.AccessSources(f, d.Group, time.Now().Unix())) > 0
	for _, g := range page.Groups {
		sources := routing.AccessSources(f, g, time.Now().Unix())
		if len(sources) == 0 {
			continue
		}
		price, err := uc.repo.Price(ctx, g.ID, userID)
		if err != nil {
			return nil, err
		}
		models, err := uc.repo.Models(ctx, g.ID, g.Key)
		if err != nil {
			return nil, err
		}
		out.Groups = append(out.Groups, AvailableRoutingGroup{Group: g, Sources: sources, Price: price, Models: models})
	}
	return out, nil
}
func (uc *RoutingAccessUsecase) Change(ctx context.Context, c RoutingAccessChange, self bool) (*routing.SubjectFacts, error) {
	if self && c.Operation != "default" {
		return nil, ErrRoutingAccessDenied
	}
	if c.Operation == "default" {
		g, err := uc.eligible(ctx, c.UserID, c.GroupID)
		if err != nil {
			return nil, err
		}
		c.GroupKey = g.Key
	}
	if c.Operation == "grant" {
		if !fixedCreationEnabled() {
			return nil, ErrRoutingGroupUnavailable
		}
		d, err := uc.groups.Get(ctx, c.GroupID)
		if err != nil {
			return nil, err
		}
		if d == nil || d.Group.Status != "enabled" {
			return nil, ErrRoutingAccessDenied
		}
	}
	return uc.repo.Change(ctx, c)
}
func (uc *RoutingAccessUsecase) CreateToken(ctx context.Context, userID int64, name, mode string, groupID int64) (*RoutingToken, error) {
	if mode == "fixed" && !fixedCreationEnabled() {
		return nil, ErrRoutingGroupUnavailable
	}
	if !routing.ValidPolicy(mode, groupID) || strings.TrimSpace(name) == "" {
		return nil, ErrRoutingGroupInvalid
	}
	if err := uc.repo.CheckCapabilities(ctx); err != nil {
		return nil, err
	}
	if _, err := uc.eligible(ctx, userID, groupID); err != nil {
		return nil, err
	}
	return uc.repo.CreateToken(ctx, userID, strings.TrimSpace(name), mode, groupID)
}
func (uc *RoutingAccessUsecase) SetToken(ctx context.Context, userID, tokenID int64, mode string, groupID, revision int64) (int64, error) {
	if !routing.ValidPolicy(mode, groupID) {
		return 0, ErrRoutingGroupInvalid
	}
	if mode == "fixed" && !fixedCreationEnabled() {
		return 0, ErrRoutingGroupUnavailable
	}
	if err := uc.repo.CheckCapabilities(ctx); err != nil {
		return 0, err
	}
	if _, err := uc.eligible(ctx, userID, groupID); err != nil {
		return 0, err
	}
	return uc.repo.SetToken(ctx, userID, tokenID, mode, groupID, revision)
}

type RoutingGroupStateWriter interface {
	SetState(context.Context, int64, int64, string, string) error
}

func (uc *RoutingAccessUsecase) SetGroupState(ctx context.Context, id, revision int64, status, access string) error {
	if id <= 0 || revision <= 0 || (status != "enabled" && status != "disabled") || (access != "public" && access != "restricted") {
		return ErrRoutingGroupInvalid
	}
	if status == "enabled" {
		if err := uc.repo.CheckCapabilities(ctx); err != nil {
			return err
		}
		if _, err := uc.repo.Price(ctx, id, 0); err != nil {
			return err
		}
	}
	writer, ok := uc.repo.(RoutingGroupStateWriter)
	if !ok {
		return ErrRoutingGroupUnavailable
	}
	return writer.SetState(ctx, id, revision, status, access)
}

func (uc *RoutingAccessUsecase) effectiveFacts(ctx context.Context, id int64) (*routing.SubjectFacts, error) {
	f, err := uc.repo.Facts(ctx, id)
	if err != nil {
		return nil, err
	}
	if subscriptionbiz.EntitlementsEnabled() {
		if uc.entitlements == nil {
			return nil, ErrRoutingGroupUnavailable
		}
		e, err := uc.entitlements.GetRoutingEntitlements(ctx, id)
		if err != nil {
			return nil, err
		}
		f = routing.WithEntitlements(f, e)
	}
	return f, nil
}
func (uc *RoutingAccessUsecase) ValidateSubscriptionGroup(ctx context.Context, id int64) (string, error) {
	if err := uc.repo.CheckCapabilities(ctx); err != nil {
		return "", err
	}
	g, err := uc.groups.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if g == nil || g.Group.Status != "enabled" {
		return "", ErrRoutingGroupInvalid
	}
	price, err := uc.repo.Price(ctx, id, 0)
	if err != nil {
		return "", err
	}
	if price.BillingMode == routing.WalletOnly {
		return "", ErrRoutingGroupInvalid
	}
	return g.Group.Key, nil
}

type RoutingBillingPolicyRepo interface {
	GetBillingPolicy(context.Context, int64) (*routing.BillingPolicy, error)
	PublishBillingPolicy(context.Context, *routing.BillingPolicy, int64) error
}

func (uc *RoutingAccessUsecase) BillingPolicy(ctx context.Context, id int64) (*routing.BillingPolicy, error) {
	repo, ok := uc.repo.(RoutingBillingPolicyRepo)
	if !ok || id <= 0 {
		return nil, ErrRoutingGroupUnavailable
	}
	return repo.GetBillingPolicy(ctx, id)
}
func (uc *RoutingAccessUsecase) PublishBillingPolicy(ctx context.Context, p *routing.BillingPolicy, expected int64) error {
	repo, ok := uc.repo.(RoutingBillingPolicyRepo)
	if !ok {
		return ErrRoutingGroupUnavailable
	}
	if p == nil || !p.Valid() || expected < 0 {
		return ErrRoutingGroupInvalid
	}
	return repo.PublishBillingPolicy(ctx, p, expected)
}
