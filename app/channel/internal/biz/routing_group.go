package biz

import (
	"context"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/ordering"
	"micro-one-api/platform/security/serviceidentity"

	"github.com/go-kratos/kratos/v3/errors"
)

type RoutingGroup = routing.Group
type RoutingGroupDetail = routing.GroupDetail

var (
	ErrRoutingGroupNotFound          = errors.NotFound(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_NOT_FOUND.String(), "routing group not found")
	ErrRoutingGroupInvalid           = errors.BadRequest(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_INVALID.String(), "invalid routing group request")
	ErrRoutingGroupMigrationRequired = errors.ServiceUnavailable(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_MIGRATION_REQUIRED.String(), "routing group migration is required")
	ErrRoutingGroupBaselineConflict  = errors.Conflict(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_BASELINE_CONFLICT.String(), "routing group baseline differs; refresh the audit before backfill")
	ErrRoutingGroupStorage           = errors.ServiceUnavailable(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_STORAGE_UNAVAILABLE.String(), "routing group storage unavailable")
	ErrRoutingGroupExists            = errors.Conflict(channelv1.RoutingGroupErrorReason_ROUTING_GROUP_EXISTS.String(), "routing group key already exists")
)

type RoutingGroupRepo interface {
	ListRoutingGroups(context.Context, RoutingGroupListOptions) ([]*RoutingGroup, error)
	GetRoutingGroup(context.Context, int64) (*RoutingGroupDetail, error)
}

type RoutingGroupListOptions struct {
	Filter  map[string]string
	OrderBy []ordering.Field
	Offset  int
	Limit   int
}

type RoutingGroupUsecase struct {
	repo          RoutingGroupRepo
	authorization authorization.Resolver
}

func (uc *RoutingGroupUsecase) SetAuthorization(r authorization.Resolver) { uc.authorization = r }
func (uc *RoutingGroupUsecase) authorize(ctx context.Context, op string) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	point := "channel.routing_groups.write"
	if op == "channel.routing_group.list" {
		point = "channel.routing_groups.list"
	}
	if op == "channel.routing_group.read" {
		point = "channel.routing_groups.read"
	}
	return authorization.Prepare(ctx, uc.authorization, point, op)
}
func (uc *RoutingGroupUsecase) members(ctx context.Context) (context.Context, error) {
	if !authorization.External(ctx) || serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		return ctx, nil
	}
	return authorization.PrepareOptional(ctx, uc.authorization, "channel.routing_groups.read", "channel.routing_group.members.read")
}

func NewRoutingGroupUsecase(repo RoutingGroupRepo) *RoutingGroupUsecase {
	return &RoutingGroupUsecase{repo: repo}
}
func (uc *RoutingGroupUsecase) List(ctx context.Context, options RoutingGroupListOptions) ([]*RoutingGroup, error) {
	if options.Offset < 0 || options.Limit < 1 || options.Limit > 201 {
		return nil, ErrRoutingGroupInvalid
	}
	ctx, err := uc.authorize(ctx, "channel.routing_group.list")
	if err != nil {
		return nil, err
	}
	return uc.repo.ListRoutingGroups(ctx, options)
}
func (uc *RoutingGroupUsecase) Get(ctx context.Context, id int64) (*RoutingGroupDetail, error) {
	if id <= 0 {
		return nil, ErrRoutingGroupInvalid
	}
	ctx, err := uc.authorize(ctx, "channel.routing_group.read")
	if err != nil {
		return nil, err
	}
	ctx, err = uc.members(ctx)
	if err != nil {
		return nil, err
	}
	return uc.repo.GetRoutingGroup(ctx, id)
}

// RoutingGroupCreateRepo is implemented by the data layer: it inserts the group
// entity and enqueues the change outbox event in one transaction.
type RoutingGroupCreateRepo interface {
	CreateRoutingGroup(context.Context, *RoutingGroup) (*RoutingGroup, error)
}

// Create adds an explicitly created group. The caller supplies operator input
// only: a new group always starts disabled and restricted, so enabling it later
// keeps running the capability/price gates in SetState, and members arrive
// through resource CSVs once the key resolves. Owner: channel.
func (uc *RoutingGroupUsecase) Create(ctx context.Context, key, displayName, description, accessMode string) (*RoutingGroupDetail, error) {
	if !routing.ValidNewGroupKey(key) {
		return nil, ErrRoutingGroupInvalid
	}
	if !routing.ValidGroupAccessMode(accessMode) {
		return nil, ErrRoutingGroupInvalid
	}
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = key
	}
	if len(displayName) > maxGroupTextBytes || len(description) > maxGroupTextBytes {
		return nil, ErrRoutingGroupInvalid
	}
	if accessMode == "" {
		accessMode = "restricted"
	}
	repo, ok := uc.repo.(RoutingGroupCreateRepo)
	if !ok {
		return nil, ErrRoutingGroupStorage
	}
	ctx, err := uc.authorize(ctx, "channel.routing_group.create")
	if err != nil {
		return nil, err
	}
	if err = authorization.Require(ctx, "channel.routing_group.create", authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return nil, err
	}
	created, err := repo.CreateRoutingGroup(ctx, &RoutingGroup{
		Key:             key,
		DisplayName:     displayName,
		Description:     description,
		Status:          "disabled",
		AccessMode:      accessMode,
		ModelAccessMode: "all_authorized",
		Revision:        1,
	})
	if err != nil {
		return nil, err
	}
	return &RoutingGroupDetail{Group: created, MembersVisible: true, Resources: []routing.GroupResource{}, ModelGrants: []routing.GroupModelGrant{}}, nil
}

// maxGroupTextBytes bounds the free-text metadata columns (TEXT in MySQL) so a
// single request cannot store unbounded payloads.
const maxGroupTextBytes = 4096

type RoutingGroupBackfillResult struct {
	ReportHash string `json:"report_hash"`
	Groups     int    `json:"groups"`
	Grants     int    `json:"verified_grants"`
}
type RoutingGroupBackfillRepo interface {
	ApplyRoutingGroupBackfill(context.Context, *GroupAuditReport) (*RoutingGroupBackfillResult, error)
}
type RoutingGroupBackfillUsecase struct{ repo RoutingGroupBackfillRepo }

func NewRoutingGroupBackfillUsecase(repo RoutingGroupBackfillRepo) *RoutingGroupBackfillUsecase {
	return &RoutingGroupBackfillUsecase{repo: repo}
}
func (uc *RoutingGroupBackfillUsecase) Apply(ctx context.Context, report *GroupAuditReport) (*RoutingGroupBackfillResult, error) {
	if report == nil || report.Version != 2 || !report.Migration.ReadyForBackfill || report.Migration.BlockingIssues != 0 || len(report.Migration.Groups) == 0 || len(report.Models) == 0 {
		return nil, ErrRoutingGroupBaselineConflict
	}
	for _, issue := range report.Issues {
		if issue.Blocking {
			return nil, ErrRoutingGroupBaselineConflict
		}
	}
	keys := map[string]bool{}
	for _, group := range report.Migration.Groups {
		if group.LegacyKey == "" || len(group.LegacyKey) > 1024 || keys[group.LegacyKey] || (group.InitialStatus != "enabled" && group.InitialStatus != "disabled") || group.AccessMode != "restricted" {
			return nil, ErrRoutingGroupInvalid
		}
		keys[group.LegacyKey] = true
	}
	if len(keys) != len(report.Groups) {
		return nil, ErrRoutingGroupBaselineConflict
	}
	seen := map[string]bool{}
	for _, key := range report.Groups {
		if !keys[key] || seen[key] {
			return nil, ErrRoutingGroupBaselineConflict
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, model := range report.Models {
		if model == "" || seen[model] {
			return nil, ErrRoutingGroupInvalid
		}
		seen[model] = true
	}
	return uc.repo.ApplyRoutingGroupBackfill(ctx, report)
}

// Group state is an explicit command so omitted fields cannot reset metadata.
type RoutingGroupStateRepo interface {
	SetRoutingGroupState(context.Context, int64, int64, string, string) error
}

func (uc *RoutingGroupUsecase) SetState(ctx context.Context, id, revision int64, status, access string) (*RoutingGroupDetail, error) {
	if id <= 0 || revision <= 0 || (status != "enabled" && status != "disabled") || (access != "public" && access != "restricted") {
		return nil, ErrRoutingGroupInvalid
	}
	r, ok := uc.repo.(RoutingGroupStateRepo)
	if !ok {
		return nil, ErrRoutingGroupStorage
	}
	ctx, err := uc.authorize(ctx, "channel.routing_group.update")
	if err != nil {
		return nil, err
	}
	if authorization.External(ctx) && !serviceidentity.HasSystemCapability(ctx, serviceidentity.RPCMethod(ctx)) {
		for _, op := range []string{"channel.routing_group.enable", "channel.routing_group.disable"} {
			ctx, err = authorization.PrepareOptional(ctx, uc.authorization, "channel.routing_groups.write", op)
			if err != nil {
				return nil, err
			}
		}
	}
	ctx, err = uc.members(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.SetRoutingGroupState(ctx, id, revision, status, access); err != nil {
		return nil, err
	}
	return uc.repo.GetRoutingGroup(ctx, id)
}

// GroupResourceOverridesRepo is implemented by the data layer over the 098
// relation columns.
type GroupResourceOverridesRepo interface {
	SetRoutingGroupResourceOverrides(context.Context, int64, routing.Source, *int64, *int64) error
}

// SetResourceOverrides publishes nullable priority/weight overrides for one
// group resource relation. Nil fields restore inheritance. The group must not
// be archived; revision bump + outbox are the data layer's contract.
func (uc *RoutingGroupUsecase) SetResourceOverrides(ctx context.Context, groupID int64, source routing.Source, priority, weight *int64) (*RoutingGroupDetail, error) {
	if groupID <= 0 || source.ID <= 0 || (source.Kind != routing.Channel && source.Kind != routing.Subscription) {
		return nil, ErrRoutingGroupInvalid
	}
	if priority != nil && *priority < 0 {
		return nil, ErrRoutingGroupInvalid
	}
	if weight != nil && *weight < 0 {
		return nil, ErrRoutingGroupInvalid
	}
	ctx, err := uc.authorize(ctx, "channel.routing_group.resource_override.update")
	if err != nil {
		return nil, err
	}
	if err = authorization.Require(ctx, "channel.routing_group.resource_override.update", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: groupID}); err != nil {
		return nil, err
	}
	// Membership existence is an owner write precondition, not an extra read grant.
	detail, err := uc.repo.GetRoutingGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if detail == nil || detail.Group == nil || detail.Group.Status == "archived" {
		return nil, ErrRoutingGroupInvalid
	}
	member := false
	for _, resource := range detail.Resources {
		if resource.Source.Kind == source.Kind && resource.Source.ID == source.ID {
			member = true
			break
		}
	}
	if !member {
		return nil, ErrRoutingGroupNotFound
	}
	r, ok := uc.repo.(GroupResourceOverridesRepo)
	if !ok {
		return nil, ErrRoutingGroupStorage
	}
	ctx, err = uc.members(ctx)
	if err != nil {
		return nil, err
	}
	if err := r.SetRoutingGroupResourceOverrides(ctx, groupID, source, priority, weight); err != nil {
		return nil, err
	}
	return uc.repo.GetRoutingGroup(ctx, groupID)
}
