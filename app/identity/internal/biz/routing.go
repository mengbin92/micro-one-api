package biz

import (
	"context"
	"os"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/routing"
)

var (
	ErrRoutingFactsUnavailable = errors.ServiceUnavailable(identityv1.RoutingIdentityErrorReason_ROUTING_FACTS_UNAVAILABLE.String(), "routing facts unavailable; complete identity migration first")
	ErrRoutingDefaultInvalid   = errors.BadRequest(identityv1.RoutingIdentityErrorReason_ROUTING_DEFAULT_INVALID.String(), "default routing group unavailable")
	ErrRoutingAccessConflict   = errors.Conflict(identityv1.RoutingIdentityErrorReason_ROUTING_ACCESS_CONFLICT.String(), "routing access changed; reload and retry")
)

type RoutingGroupReader interface {
	FindRoutingGroup(context.Context, string) (*routing.Group, error)
}
type RoutingFactsRepo interface {
	GetRoutingFacts(context.Context, int64, int64, string) (*routing.SubjectFacts, error)
}

func RoutingV2Enabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("IDENTITY_ROUTING_V2")), "true")
}

func (uc *IdentityUsecase) SetRoutingGroupReader(r RoutingGroupReader) { uc.routingGroups = r }

func registrationGroup(legacy string) string {
	if !RoutingV2Enabled() {
		return legacy
	}
	group := os.Getenv("IDENTITY_DEFAULT_ROUTING_GROUP")
	if group == "" {
		return "default"
	}
	return group
}

func (uc *IdentityUsecase) bindLegacyGroup(ctx context.Context, user *User) error {
	if !RoutingV2Enabled() {
		return nil
	}
	if uc.routingGroups == nil {
		return ErrRoutingFactsUnavailable
	}
	group, err := uc.routingGroups.FindRoutingGroup(ctx, user.Group)
	if err != nil {
		return ErrRoutingFactsUnavailable
	}
	if group == nil || group.ID <= 0 || group.Key != user.Group || group.Status != "enabled" {
		return ErrRoutingDefaultInvalid
	}
	user.DefaultRoutingGroupID = group.ID
	return nil
}

func (uc *IdentityUsecase) createUser(ctx context.Context, user *User) error {
	if err := uc.bindLegacyGroup(ctx, user); err != nil {
		return err
	}
	return uc.repo.CreateUser(ctx, user)
}
