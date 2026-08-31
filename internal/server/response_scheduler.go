package server

import (
	"context"
	"fmt"
	"strings"

	relaybiz "micro-one-api/internal/biz"
)

// OpenAIWSRoutingScheduler resolves the first turn for a Responses session.
// It prefers a precise previous-response route, then a sticky session route,
// and falls back to the normal relay planner when no route can be reused.
type OpenAIWSRoutingScheduler struct {
	server  *HTTPServer
	planner relayPlanner
}

func NewOpenAIWSRoutingScheduler(server *HTTPServer) *OpenAIWSRoutingScheduler {
	return &OpenAIWSRoutingScheduler{server: server, planner: server}
}

type relayPlanner interface {
	Plan(ctx context.Context, req relaybiz.RelayRequest) (*relaybiz.RelayPlan, error)
}

func (s *OpenAIWSRoutingScheduler) ResolveStoredRoute(ctx context.Context, token, clientModel, previousResponseID string) (*relaybiz.RelayPlan, bool) {
	if s == nil || s.server == nil {
		return nil, false
	}
	previousResponseID = strings.TrimSpace(previousResponseID)
	if previousResponseID == "" {
		return nil, false
	}
	if route, ok := s.server.lookupResponseRouteWithSticky(ctx, token, clientModel, previousResponseID); ok {
		if s.server.identityClient == nil {
			return nil, false
		}
		authSnapshot, err := s.server.getAuthSnapshot(ctx, token)
		if err == nil && (route.UserID == 0 || route.UserID == authSnapshot.UserId) {
			modelForPermission := strings.TrimSpace(clientModel)
			if modelForPermission == "" {
				modelForPermission = strings.TrimSpace(route.Model)
			}
			if !authAllowsModel(authSnapshot.AllowedModels, modelForPermission) {
				return nil, false
			}
			if route.SubscriptionAccountID > 0 && route.Account == nil {
				if s.server.relayUsecase == nil {
					return nil, false
				}
				globalModel := strings.TrimSpace(route.GlobalModel)
				if globalModel == "" {
					globalModel = strings.TrimSpace(clientModel)
				}
				channel, account, resolveErr := s.server.relayUsecase.ResolveSubscriptionRoutingSource(
					ctx, route.SubscriptionAccountID, authSnapshot.Group, clientModel, globalModel,
				)
				if resolveErr != nil {
					return nil, false
				}
				route.Channel = *channel
				route.Account = account
			}
			globalModel, resolvedModel := s.routeModels(route, clientModel)
			return &relaybiz.RelayPlan{
				Auth: &relaybiz.AuthSnapshot{
					UserID:        authSnapshot.UserId,
					TokenID:       authSnapshot.TokenId,
					TokenName:     authSnapshot.TokenName,
					Group:         authSnapshot.Group,
					AllowedModels: authSnapshot.AllowedModels,
					UserEnabled:   authSnapshot.UserEnabled,
					TokenEnabled:  authSnapshot.TokenEnabled,
				},
				Channel:       &route.Channel,
				Account:       route.Account,
				GlobalModel:   globalModel,
				ResolvedModel: resolvedModel,
			}, true
		}
	}
	return nil, false
}

func (s *OpenAIWSRoutingScheduler) ResolveSessionRoute(ctx context.Context, token, clientModel, sessionHash string) (*relaybiz.RelayPlan, bool) {
	if s == nil || s.server == nil {
		return nil, false
	}
	sessionHash = strings.TrimSpace(sessionHash)
	if sessionHash == "" {
		return nil, false
	}
	var route responseRoute
	if !s.server.lookupWSStickySessionRoute(ctx, token, clientModel, sessionHash, &route) {
		return nil, false
	}
	if s.server.identityClient == nil {
		return nil, false
	}
	authSnapshot, err := s.server.getAuthSnapshot(ctx, token)
	if err != nil || (route.UserID != 0 && route.UserID != authSnapshot.UserId) {
		return nil, false
	}
	if !authAllowsModel(authSnapshot.AllowedModels, clientModel) {
		return nil, false
	}
	globalModel, resolvedModel := s.routeModels(route, clientModel)
	return &relaybiz.RelayPlan{
		Auth: &relaybiz.AuthSnapshot{
			UserID:        authSnapshot.UserId,
			TokenID:       authSnapshot.TokenId,
			TokenName:     authSnapshot.TokenName,
			Group:         authSnapshot.Group,
			AllowedModels: authSnapshot.AllowedModels,
			UserEnabled:   authSnapshot.UserEnabled,
			TokenEnabled:  authSnapshot.TokenEnabled,
		},
		Channel:       &route.Channel,
		Account:       route.Account,
		GlobalModel:   globalModel,
		ResolvedModel: resolvedModel,
	}, true
}

func (s *OpenAIWSRoutingScheduler) routeModels(route responseRoute, clientModel string) (string, string) {
	globalModel := relaybiz.RelayModelName(route.GlobalModel)
	requestedModel := relaybiz.RelayModelName(clientModel)
	if requestedModel == "" {
		requestedModel = relaybiz.RelayModelName(route.Model)
	}
	if globalModel == "" {
		globalModel = requestedModel
		if requestedModel != "" && s != nil && s.server != nil && s.server.relayUsecase != nil {
			globalModel = strings.TrimSpace(s.server.relayUsecase.ResolveModel(requestedModel))
		}
	}
	resolvedModel := relaybiz.RelayModelName(route.ResolvedModel)
	if resolvedModel == "" && globalModel != "" {
		resolvedModel = relaybiz.ResolveChannelModel(&route.Channel, globalModel)
	}
	return globalModel, resolvedModel
}

func (s *OpenAIWSRoutingScheduler) ResolvePlan(ctx context.Context, token, clientModel, previousResponseID, sessionHash string) (*relaybiz.RelayPlan, error) {
	if plan, ok := s.ResolveStoredRoute(ctx, token, clientModel, previousResponseID); ok {
		return plan, nil
	}
	if plan, ok := s.ResolveSessionRoute(ctx, token, clientModel, sessionHash); ok {
		if s.server.wsSticky != nil && plan.Auth != nil {
			s.server.wsSticky.RefreshSessionTTL(ctx, plan.Auth.Group, sessionHash, s.server.openAIWSStickyTTL())
		}
		return plan, nil
	}
	if s == nil || s.server == nil {
		return nil, fmt.Errorf("openai ws scheduler unavailable")
	}
	if s.planner == nil {
		return nil, fmt.Errorf("openai ws scheduler unavailable")
	}
	plan, err := s.planner.Plan(ctx, relaybiz.RelayRequest{
		Token: token,
		Model: clientModel,
	})
	if err != nil {
		return nil, err
	}
	return plan, nil
}

func (s *OpenAIWSRoutingScheduler) BindSession(ctx context.Context, plan *relaybiz.RelayPlan, sessionHash string) {
	if s == nil || s.server == nil || s.server.wsSticky == nil || plan == nil || plan.Auth == nil || plan.Channel == nil {
		return
	}
	sessionHash = strings.TrimSpace(sessionHash)
	if sessionHash == "" {
		return
	}
	s.server.wsSticky.BindSessionRoute(ctx, plan.Auth.Group, sessionHash, plan.Channel, s.server.openAIWSStickyTTL())
}

func authAllowsModel(allowedModels []string, model string) bool {
	model = relaybiz.RelayModelName(model)
	if model == "" || len(allowedModels) == 0 {
		return true
	}
	for _, allowed := range allowedModels {
		if strings.EqualFold(relaybiz.RelayModelName(allowed), model) {
			return true
		}
	}
	return false
}
