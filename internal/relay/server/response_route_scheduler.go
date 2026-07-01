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

func (s *HTTPServer) lookupResponseRouteWithSticky(ctx context.Context, token, responseID string) (responseRoute, bool) {
	responseID = strings.TrimSpace(responseID)
	if !isOpenAIResponseID(responseID) {
		return responseRoute{}, false
	}
	if s == nil {
		return responseRoute{}, false
	}
	if route, ok := s.lookupResponseRoute(responseID); ok {
		return route, true
	}
	if s.wsSticky != nil {
		var route responseRoute
		if s.lookupWSStickyRoute(ctx, token, responseID, &route) {
			return route, true
		}
	}
	return responseRoute{}, false
}
