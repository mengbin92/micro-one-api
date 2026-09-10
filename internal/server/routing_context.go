package server

import (
	"context"
	"fmt"

	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/routingdto"
)

func (s *HTTPServer) routingAuth(ctx context.Context, p *identityv1.GetAuthSnapshotReply) (*relaybiz.AuthSnapshot, error) {
	if p == nil {
		return nil, fmt.Errorf("missing identity snapshot")
	}
	auth := &relaybiz.AuthSnapshot{UserID: p.UserId, TokenID: p.TokenId, TokenName: p.TokenName, Group: p.Group, AllowedModels: p.AllowedModels, UserEnabled: p.UserEnabled, TokenEnabled: p.TokenEnabled, RoutingFacts: routingdto.FactsFromProto(p.RoutingFacts), RoutingContextVersion: p.RoutingContextVersion}
	if relaybiz.RoutingContextV2Enabled() {
		if s.relayUsecase == nil {
			return nil, fmt.Errorf("routing context resolver unavailable")
		}
		if err := s.relayUsecase.ResolveRoutingContext(ctx, auth); err != nil {
			return nil, err
		}
	}
	return auth, nil
}

func (s *HTTPServer) reserveAuthenticatedQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string, accountID int64, auth *identityv1.GetAuthSnapshotReply) (*billingv1.ReserveQuotaResponse, error) {
	resolved, err := s.routingAuth(ctx, auth)
	if err != nil {
		return nil, err
	}
	return s.reserveQuota(ctx, userID, requestID, estimatedTokens, model, channelID, accountID, resolved.RoutingContext)
}
