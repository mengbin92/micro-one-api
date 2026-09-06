package server

import (
	"context"
	"strings"

	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/domain/routing"
	relaybiz "micro-one-api/internal/biz"
)

func (s *HTTPServer) checkStoredSource(ctx context.Context, group, model string, source routing.Source) (routing.Permission, error) {
	if s.relayUsecase != nil {
		return s.relayUsecase.CanRoute(ctx, group, model, s.relayUsecase.ResolveModel(model), source)
	}
	if s.channelClient == nil || model == "" {
		return routing.Permission{}, nil
	}
	reply, err := s.channelClient.CheckRoute(ctx, &channelv1.CheckRouteRequest{Group: group, Model: model, SourceKind: source.Kind, SourceId: source.ID})
	if err != nil {
		return routing.Permission{}, err
	}
	return routing.Permission{Allowed: reply.GetAllowed(), UpstreamModelID: reply.GetUpstreamModelId()}, nil
}

// refreshStoredResponseRoute rechecks local response caches as well as Redis
// bindings. A cached account projection is not evidence of current permission.
func (s *HTTPServer) refreshStoredResponseRoute(ctx context.Context, auth *identityv1.GetAuthSnapshotReply, clientModel string, route responseRoute) (responseRoute, bool) {
	if auth == nil || (route.UserID != 0 && route.UserID != auth.UserId) {
		return responseRoute{}, false
	}
	model := strings.TrimSpace(clientModel)
	if model == "" {
		model = strings.TrimSpace(route.Model)
	}
	if model == "" || !authAllowsModel(auth.AllowedModels, model) || (route.Model != "" && !authAllowsModel(auth.AllowedModels, route.Model)) {
		return responseRoute{}, false
	}
	source := openAIWSStickySource{kind: relaybiz.UpstreamRouteChannel, id: route.Channel.ID}
	accountID := route.SubscriptionAccountID
	if accountID == 0 {
		accountID = route.Channel.SubscriptionAccountID
	}
	if accountID > 0 {
		source = openAIWSStickySource{kind: relaybiz.UpstreamRouteSubscription, id: accountID}
	}
	if route.Model != "" && route.Model != model {
		kind := routing.Channel
		if accountID > 0 {
			kind = routing.Subscription
		}
		permission, err := s.checkStoredSource(ctx, auth.Group, route.Model, routing.Source{Kind: kind, ID: source.id})
		if err != nil || !permission.Allowed {
			return responseRoute{}, false
		}
	}
	var refreshed responseRoute
	if !s.materializeWSStickySource(ctx, auth, model, source, &refreshed) {
		return responseRoute{}, false
	}
	refreshed.Model = model
	refreshed.GlobalModel = model
	if s.relayUsecase != nil {
		refreshed.GlobalModel = s.relayUsecase.ResolveModel(model)
	}
	refreshed.ResolvedModel = relaybiz.ResolveChannelModel(&refreshed.Channel, refreshed.GlobalModel)
	return refreshed, true
}
