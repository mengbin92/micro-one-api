package biz

import (
	"context"
	"math"

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
	// UserRatio/UserVersion are set when a user-specific override replaces
	// the group ratio for this user.
	UserRatio   float64
	UserVersion int64
}
type AvailableRoutingGroup struct {
	Group   *routing.Group
	Sources []routing.UserGroupGrant
	Price   RoutingPrice
	Models  []string
	// OrderedEligible reports whether this group can serve ordered (auto)
	// tokens for the user right now.
	OrderedEligible bool
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
	GroupIDs                     []int64
}
type RoutingAccessRepo interface {
	Facts(context.Context, int64) (*routing.SubjectFacts, error)
	Change(context.Context, RoutingAccessChange) (*routing.SubjectFacts, error)
	CreateToken(context.Context, int64, string, string, int64, []int64) (*RoutingToken, error)
	SetToken(context.Context, int64, int64, string, int64, int64, []int64) (int64, error)
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
func orderedCreationEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ADMIN_ROUTING_ORDERED_KEYS")), "true")
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
		out.Groups = append(out.Groups, AvailableRoutingGroup{Group: g, Sources: sources, Price: price, Models: models, OrderedEligible: orderedCreationEnabled()})
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

// validateOrderedCandidates enforces the ordered (auto) creation contract:
// every listed group must exist and not be archived; at least one must be
// currently eligible. Pre-listed ineligible groups are allowed — they become
// effective as soon as the user is granted access.
func (uc *RoutingAccessUsecase) validateOrderedCandidates(ctx context.Context, facts *routing.SubjectFacts, groupIDs []int64) error {
	eligible := 0
	now := time.Now().Unix()
	for _, gid := range groupIDs {
		d, err := uc.groups.Get(ctx, gid)
		if err != nil {
			return err
		}
		if d == nil || d.Group.Status == "archived" {
			return ErrRoutingGroupInvalid
		}
		if len(routing.AccessSources(facts, d.Group, now)) > 0 {
			eligible++
		}
	}
	if eligible == 0 {
		return ErrRoutingAccessDenied
	}
	return nil
}

func (uc *RoutingAccessUsecase) CreateToken(ctx context.Context, userID int64, name, mode string, groupID int64, groupIDs []int64) (*RoutingToken, error) {
	if mode == "fixed" && !fixedCreationEnabled() {
		return nil, ErrRoutingGroupUnavailable
	}
	if mode == "ordered" && !orderedCreationEnabled() {
		return nil, ErrRoutingGroupUnavailable
	}
	if mode == "ordered" {
		if !routing.ValidOrderedPolicy(mode, groupID, groupIDs) || strings.TrimSpace(name) == "" {
			return nil, ErrRoutingGroupInvalid
		}
	} else if !routing.ValidPolicy(mode, groupID) || strings.TrimSpace(name) == "" || len(groupIDs) > 0 {
		return nil, ErrRoutingGroupInvalid
	}
	if err := uc.repo.CheckCapabilities(ctx); err != nil {
		return nil, err
	}
	if mode == "ordered" {
		facts, err := uc.effectiveFacts(ctx, userID)
		if err != nil {
			return nil, err
		}
		if err := uc.validateOrderedCandidates(ctx, facts, groupIDs); err != nil {
			return nil, err
		}
	} else if _, err := uc.eligible(ctx, userID, groupID); err != nil {
		return nil, err
	}
	return uc.repo.CreateToken(ctx, userID, strings.TrimSpace(name), mode, groupID, groupIDs)
}
func (uc *RoutingAccessUsecase) SetToken(ctx context.Context, userID, tokenID int64, mode string, groupID, revision int64, groupIDs []int64) (int64, error) {
	if mode == "fixed" && !fixedCreationEnabled() {
		return 0, ErrRoutingGroupUnavailable
	}
	if mode == "ordered" && !orderedCreationEnabled() {
		return 0, ErrRoutingGroupUnavailable
	}
	if mode == "ordered" {
		if !routing.ValidOrderedPolicy(mode, groupID, groupIDs) {
			return 0, ErrRoutingGroupInvalid
		}
	} else if !routing.ValidPolicy(mode, groupID) || len(groupIDs) > 0 {
		return 0, ErrRoutingGroupInvalid
	}
	if err := uc.repo.CheckCapabilities(ctx); err != nil {
		return 0, err
	}
	if mode == "ordered" {
		facts, err := uc.effectiveFacts(ctx, userID)
		if err != nil {
			return 0, err
		}
		if err := uc.validateOrderedCandidates(ctx, facts, groupIDs); err != nil {
			return 0, err
		}
	} else if _, err := uc.eligible(ctx, userID, groupID); err != nil {
		return 0, err
	}
	return uc.repo.SetToken(ctx, userID, tokenID, mode, groupID, revision, groupIDs)
}

type RoutingGroupStateWriter interface {
	SetState(context.Context, int64, int64, string, string) error
}

// RoutingGroupCreateWriter is implemented by the data repo; the channel owner
// RPC is the authoritative creator.
type RoutingGroupCreateWriter interface {
	CreateGroup(context.Context, string, string, string, string) (*routing.Group, error)
}

// CreateGroup adds a routing group owned by channel. Input rules come from
// domain/routing so admin and channel cannot drift; the channel usecase
// re-validates authoritatively. The capability gate matches the other group
// writes: a new group must not exist where routing v2 is unavailable.
func (uc *RoutingAccessUsecase) CreateGroup(ctx context.Context, key, displayName, description, accessMode string) (*routing.Group, error) {
	if !routing.ValidNewGroupKey(key) || !routing.ValidGroupAccessMode(accessMode) {
		return nil, ErrRoutingGroupInvalid
	}
	if err := uc.repo.CheckCapabilities(ctx); err != nil {
		return nil, err
	}
	writer, ok := uc.repo.(RoutingGroupCreateWriter)
	if !ok {
		return nil, ErrRoutingGroupUnavailable
	}
	return writer.CreateGroup(ctx, key, strings.TrimSpace(displayName), description, accessMode)
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

// RoutingUserPriceWriter is implemented by the data repo when the billing
// capability is wired. The user override REPLACES the group ratio.
type RoutingUserPriceWriter interface {
	SetUserRoutingPrice(context.Context, int64, int64, float64) (int64, error)
	ClearUserRoutingPrice(context.Context, int64, int64) error
}

func (uc *RoutingAccessUsecase) SetUserRoutingPrice(ctx context.Context, userID, groupID int64, ratio float64) (int64, error) {
	if userID <= 0 || groupID <= 0 || ratio <= 0 || math.IsInf(ratio, 0) || math.IsNaN(ratio) {
		return 0, ErrRoutingGroupInvalid
	}
	writer, ok := uc.repo.(RoutingUserPriceWriter)
	if !ok {
		return 0, ErrRoutingGroupUnavailable
	}
	return writer.SetUserRoutingPrice(ctx, userID, groupID, ratio)
}

func (uc *RoutingAccessUsecase) ClearUserRoutingPrice(ctx context.Context, userID, groupID int64) error {
	if userID <= 0 || groupID <= 0 {
		return ErrRoutingGroupInvalid
	}
	writer, ok := uc.repo.(RoutingUserPriceWriter)
	if !ok {
		return ErrRoutingGroupUnavailable
	}
	return writer.ClearUserRoutingPrice(ctx, userID, groupID)
}

// RoutingResourceOverrideWriter is implemented by the data repo when the
// channel capability is wired. Nil priority/weight clear the override
// (inherit the resource's own values).
type RoutingResourceOverrideWriter interface {
	SetRoutingGroupResourceOverrides(context.Context, int64, routing.Source, *int64, *int64) error
}

func (uc *RoutingAccessUsecase) SetResourceOverrides(ctx context.Context, groupID int64, source routing.Source, priority, weight *int64) error {
	if groupID <= 0 || source.ID <= 0 || (source.Kind != routing.Channel && source.Kind != routing.Subscription) {
		return ErrRoutingGroupInvalid
	}
	if priority != nil && *priority < 0 || weight != nil && *weight < 0 {
		return ErrRoutingGroupInvalid
	}
	writer, ok := uc.repo.(RoutingResourceOverrideWriter)
	if !ok {
		return ErrRoutingGroupUnavailable
	}
	return writer.SetRoutingGroupResourceOverrides(ctx, groupID, source, priority, weight)
}
