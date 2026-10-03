package biz

import (
	"context"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/routing"
	"sort"
	"strings"
)

type RoutingGroupManagementRepo interface {
	ArchiveRoutingGroup(context.Context, int64, int64) (*RoutingGroup, error)
	ReplaceRoutingGroupMembers(context.Context, int64, int64, []routing.Source) (*RoutingGroup, error)
}

func (uc *RoutingGroupUsecase) Archive(ctx context.Context, id, revision int64, reason string) (*RoutingGroup, error) {
	if id <= 0 || revision <= 0 || strings.TrimSpace(reason) == "" {
		return nil, ErrRoutingGroupInvalid
	}
	r, ok := uc.repo.(RoutingGroupManagementRepo)
	if !ok {
		return nil, ErrRoutingGroupStorage
	}
	var err error
	if authorization.External(ctx) {
		ctx, err = authorization.Prepare(ctx, uc.authorization, "channel.routing_groups.write", "channel.routing_group.archive")
		if err != nil {
			return nil, err
		}
	}
	if err := authorization.Require(ctx, "channel.routing_group.archive", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
		return nil, err
	}
	return r.ArchiveRoutingGroup(authorization.WithWriteReason(ctx, reason), id, revision)
}
func (uc *RoutingGroupUsecase) ReplaceMembers(ctx context.Context, id, revision int64, members []routing.Source, reason string) (*RoutingGroup, error) {
	if id <= 0 || revision <= 0 || len(members) > 2000 || strings.TrimSpace(reason) == "" {
		return nil, ErrRoutingGroupInvalid
	}
	members = append([]routing.Source(nil), members...)
	seen := map[routing.Source]bool{}
	for _, source := range members {
		if source.ID <= 0 || (source.Kind != routing.Channel && source.Kind != routing.Subscription) || seen[source] {
			return nil, ErrRoutingGroupInvalid
		}
		seen[source] = true
	}
	sort.Slice(members, func(i, j int) bool {
		if members[i].Kind == members[j].Kind {
			return members[i].ID < members[j].ID
		}
		return members[i].Kind < members[j].Kind
	})
	r, ok := uc.repo.(RoutingGroupManagementRepo)
	if !ok {
		return nil, ErrRoutingGroupStorage
	}
	var err error
	if authorization.External(ctx) {
		ctx, err = authorization.Prepare(ctx, uc.authorization, "channel.routing_groups.write", "channel.routing_group.members.update")
		if err != nil {
			return nil, err
		}
		for _, entry := range []struct{ point, op string }{{"channel.channels.update", "channel.channel.update"}, {"channel.accounts.update", "channel.account.update"}} {
			ctx, err = authorization.PrepareOptional(ctx, uc.authorization, entry.point, entry.op)
			if err != nil {
				return nil, err
			}
		}
	}
	if err := authorization.Require(ctx, "channel.routing_group.members.update", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id}); err != nil {
		return nil, err
	}
	return r.ReplaceRoutingGroupMembers(authorization.WithWriteReason(ctx, reason), id, revision, members)
}
