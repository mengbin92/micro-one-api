package server

import (
	"context"
	"strings"
)

func isOpenAIResponseID(responseID string) bool {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		return false
	}
	return !strings.HasPrefix(responseID, "msg_")
}

func (s *HTTPServer) lookupResponseRouteWithSticky(ctx context.Context, token, clientModel, responseID string) (responseRoute, bool) {
	responseID = strings.TrimSpace(responseID)
	if !isOpenAIResponseID(responseID) {
		return responseRoute{}, false
	}
	if s == nil {
		return responseRoute{}, false
	}
	if route, ok := s.lookupResponseRoute(responseID); ok {
		if s.identityClient == nil {
			return responseRoute{}, false
		}
		auth, err := s.getAuthSnapshot(ctx, token)
		if err != nil {
			return responseRoute{}, false
		}
		return s.refreshStoredResponseRoute(ctx, auth, clientModel, route)
	}
	if s.wsSticky != nil {
		var route responseRoute
		if s.lookupWSStickyRoute(ctx, token, clientModel, responseID, &route) {
			return route, true
		}
	}
	return responseRoute{}, false
}
