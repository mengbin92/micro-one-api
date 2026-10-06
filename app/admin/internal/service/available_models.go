package service

import (
	"context"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/routing"
	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/routingdto"
)

// AvailableModels authenticates the browser session or API key at identity,
// then derives groups solely from owner facts. A request cannot choose groups.
func (s *AdminService) AvailableModels(ctx context.Context, raw, clientIP string) ([]string, error) {
	if s.identityClient == nil || s.channelClient == nil {
		return nil, status.Error(codes.Unavailable, "model dependencies unavailable")
	}
	var userID, tokenID int64
	var allowed []string
	var facts *routing.SubjectFacts
	session := strings.Count(raw, ".") == 2
	if session {
		id, err := s.AuthenticateSelf(ctx, raw)
		if err != nil {
			return nil, err
		}
		userID = id
		reply, err := s.identityClient.GetUserRoutingFacts(operatorRPCContext(ctx), &identityv1.GetUserRoutingFactsRequest{UserId: id})
		if err != nil {
			return nil, err
		}
		facts = routingdto.FactsFromProto(reply.GetFacts())
		if facts != nil {
			facts.TokenMode = "inherit"
			facts.TokenGroupID = 0
			facts.TokenGroupIDs = nil
		}
	} else {
		reply, err := s.identityClient.GetAuthSnapshot(operatorRPCContext(ctx), &identityv1.GetAuthSnapshotRequest{Token: raw, ClientIp: clientIP})
		if err != nil {
			return nil, err
		}
		if !reply.GetUserEnabled() || !reply.GetTokenEnabled() || reply.GetUserId() <= 0 || reply.GetTokenId() <= 0 {
			return nil, status.Error(codes.Unauthenticated, "token invalid")
		}
		userID, tokenID, allowed = reply.UserId, reply.TokenId, reply.AllowedModels
		facts = routingdto.FactsFromProto(reply.GetRoutingFacts())
	}
	if facts == nil || facts.AccessRevision <= 0 {
		return nil, status.Error(codes.Unavailable, "routing facts unavailable")
	}
	if subscriptionbiz.EntitlementsEnabled() {
		if s.subscriptionUc == nil {
			return nil, status.Error(codes.Unavailable, "subscription facts unavailable")
		}
		entitlements, err := s.subscriptionUc.GetRoutingEntitlements(ctx, userID)
		if err != nil {
			return nil, err
		}
		facts = routing.WithEntitlements(facts, entitlements)
	}
	ordered := routing.OrderedGroupIDs(facts)
	ids := ordered
	if len(ids) == 0 {
		id := routing.SelectedGroupID(facts)
		if id <= 0 {
			return nil, status.Error(codes.PermissionDenied, "routing policy denied")
		}
		ids = []int64{id}
	}
	verified := make([]int64, 0, len(ids))
	for ordinal, id := range ids {
		detail, err := s.channelClient.GetRoutingGroup(operatorRPCContext(ctx), &channelv1.GetRoutingGroupRequest{Id: id})
		if err != nil {
			if status.Code(err) == codes.NotFound && len(ordered) > 0 {
				continue
			}
			return nil, err
		}
		if detail.GetGroup() == nil {
			return nil, status.Error(codes.Unavailable, "routing group unavailable")
		}
		g := detail.Group
		group := &routing.Group{ID: g.Id, Key: g.Key, Status: g.Status, AccessMode: g.AccessMode, Revision: g.Revision}
		if session {
			if len(routing.AccessSources(facts, group, time.Now().Unix())) == 0 {
				return nil, status.Error(codes.PermissionDenied, "routing access denied")
			}
		} else if len(ordered) > 0 {
			if _, err := routing.ResolveOrdered(userID, tokenID, facts, group, ordinal, time.Now().Unix()); err != nil {
				continue
			}
		} else {
			if _, err := routing.Resolve(userID, tokenID, facts, group, time.Now().Unix()); err != nil {
				return nil, status.Error(codes.PermissionDenied, "routing access denied")
			}
		}
		verified = append(verified, id)
	}
	if len(verified) == 0 {
		return []string{}, nil
	}
	reply, err := s.channelClient.ListAvailableModels(operatorRPCContext(ctx), &channelv1.ListAvailableModelsRequest{RoutingGroupIds: verified})
	if err != nil {
		return nil, err
	}
	permit := map[string]bool{}
	for _, model := range allowed {
		permit[strings.ToLower(strings.TrimSpace(model))] = true
	}
	out := make([]string, 0, len(reply.GetModels()))
	for _, model := range reply.GetModels() {
		if len(permit) == 0 || permit[strings.ToLower(strings.TrimSpace(model))] {
			out = append(out, model)
		}
	}
	return out, nil
}
