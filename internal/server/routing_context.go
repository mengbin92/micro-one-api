package server

import (
	"context"
	"fmt"

	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/routing"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/routingdto"
)

func (s *HTTPServer) routingAuth(ctx context.Context, p *identityv1.GetAuthSnapshotReply, boundGroupID int64) (*relaybiz.AuthSnapshot, error) {
	if p == nil {
		return nil, fmt.Errorf("missing identity snapshot")
	}
	auth := &relaybiz.AuthSnapshot{UserID: p.UserId, TokenID: p.TokenId, TokenName: p.TokenName, Group: p.Group, AllowedModels: p.AllowedModels, UserEnabled: p.UserEnabled, TokenEnabled: p.TokenEnabled, RoutingFacts: routingdto.FactsFromProto(p.RoutingFacts), RoutingContextVersion: p.RoutingContextVersion}
	if relaybiz.RoutingContextV2Enabled() || auth.RoutingFacts != nil && (auth.RoutingFacts.TokenMode == "fixed" || auth.RoutingFacts.TokenMode == "ordered") {
		if s.relayUsecase == nil {
			return nil, fmt.Errorf("routing context resolver unavailable")
		}
		if err := s.relayUsecase.ResolveRoutingContext(ctx, auth, relaybiz.RoutingResolveOptions{BoundGroupID: boundGroupID}); err != nil {
			return nil, err
		}
	}
	return auth, nil
}

func (s *HTTPServer) reserveAuthenticatedQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string, accountID int64, auth *identityv1.GetAuthSnapshotReply, boundGroupID int64) (*billingv1.ReserveQuotaResponse, error) {
	resolved, err := s.routingAuth(ctx, auth, boundGroupID)
	if err != nil {
		return nil, err
	}
	return s.reserveQuota(ctx, userID, requestID, estimatedTokens, model, channelID, accountID, resolved.RoutingContext)
}

func resolvedGroupID(auth *relaybiz.AuthSnapshot) int64 {
	if auth == nil || auth.RoutingContext == nil {
		return 0
	}
	return auth.RoutingContext.GroupID
}
func selectedProtoGroupID(auth *identityv1.GetAuthSnapshotReply) int64 {
	if auth == nil || auth.RoutingFacts == nil {
		return 0
	}
	return routing.SelectedGroupID(routingdto.FactsFromProto(auth.RoutingFacts))
}
func routingSessionScope(auth *relaybiz.AuthSnapshot) string {
	if auth.RoutingContext == nil {
		return auth.Group
	}
	return fmt.Sprintf("v2/u%d/t%d/g%d", auth.UserID, auth.TokenID, auth.RoutingContext.GroupID)
}
func protoSessionScope(auth *identityv1.GetAuthSnapshotReply) string {
	if auth.RoutingContextVersion == 0 {
		return auth.Group
	}
	return fmt.Sprintf("v2/u%d/t%d/g%d", auth.UserId, auth.TokenId, selectedProtoGroupID(auth))
}
