// Package routingdto converts internal RPC contracts at service/client boundaries.
package routingdto

import (
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/domain/routing"
)

func ContextToProto(r *routing.ResolvedRoutingContext) *commonv1.ResolvedRoutingContext {
	if r == nil {
		return nil
	}
	return &commonv1.ResolvedRoutingContext{Version: r.Version, UserId: r.UserID, TokenId: r.TokenID, GroupId: r.GroupID, GroupKey: r.GroupKey, TokenMode: r.TokenMode, TokenRevision: r.TokenRevision, UserAccessRevision: r.UserAccessRevision, GroupRevision: r.GroupRevision, SubscriptionEntitlementVersion: r.SubscriptionEntitlementVersion, SubscriptionId: r.SubscriptionID, SelectionSource: r.SelectionSource, CandidateGroupIds: r.CandidateGroupIDs, AttemptOrdinal: r.AttemptOrdinal}
}

func ContextFromProto(r *commonv1.ResolvedRoutingContext) *routing.ResolvedRoutingContext {
	if r == nil {
		return nil
	}
	return &routing.ResolvedRoutingContext{Version: r.Version, UserID: r.UserId, TokenID: r.TokenId, GroupID: r.GroupId, GroupKey: r.GroupKey, TokenMode: r.TokenMode, TokenRevision: r.TokenRevision, UserAccessRevision: r.UserAccessRevision, GroupRevision: r.GroupRevision, SubscriptionEntitlementVersion: r.SubscriptionEntitlementVersion, SubscriptionID: r.SubscriptionId, SelectionSource: r.SelectionSource, CandidateGroupIDs: r.CandidateGroupIds, AttemptOrdinal: r.AttemptOrdinal}
}

func FactsToProto(f *routing.SubjectFacts) *commonv1.RoutingSubjectFacts {
	if f == nil {
		return nil
	}
	p := &commonv1.RoutingSubjectFacts{DefaultGroupId: f.DefaultGroupID, PublicGroupAccess: f.PublicGroupAccess, AccessRevision: f.AccessRevision, TokenMode: f.TokenMode, TokenGroupId: f.TokenGroupID, TokenRevision: f.TokenRevision, TokenGroupIds: f.TokenGroupIDs}
	for _, g := range f.Grants {
		p.Grants = append(p.Grants, &commonv1.UserRoutingGroupGrant{GroupId: g.GroupID, SourceType: g.SourceType, SourceRef: g.SourceRef, StartsAt: g.StartsAt, ExpiresAt: g.ExpiresAt, Status: g.Status})
	}
	return p
}

func FactsFromProto(p *commonv1.RoutingSubjectFacts) *routing.SubjectFacts {
	if p == nil {
		return nil
	}
	f := &routing.SubjectFacts{DefaultGroupID: p.DefaultGroupId, PublicGroupAccess: p.PublicGroupAccess, AccessRevision: p.AccessRevision, TokenMode: p.TokenMode, TokenGroupID: p.TokenGroupId, TokenRevision: p.TokenRevision, TokenGroupIDs: p.TokenGroupIds}
	for _, g := range p.Grants {
		if g != nil {
			f.Grants = append(f.Grants, routing.UserGroupGrant{GroupID: g.GroupId, SourceType: g.SourceType, SourceRef: g.SourceRef, StartsAt: g.StartsAt, ExpiresAt: g.ExpiresAt, Status: g.Status})
		}
	}
	return f
}
