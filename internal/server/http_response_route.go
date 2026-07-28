package server

import (
	"time"

	relaybiz "micro-one-api/internal/biz"
)

type responseRoute struct {
	Model                 string
	GlobalModel           string
	ResolvedModel         string
	Channel               relaybiz.Channel
	Account               *relaybiz.SubscriptionAccount
	UserID                int64
	SubscriptionAccountID int64
}

type responseRouteEntry struct {
	route     responseRoute
	expiresAt time.Time
}

func (s *HTTPServer) storeResponseRoute(responseID string, route responseRoute) {
	if responseID == "" {
		return
	}
	now := time.Now()
	s.responsesMu.Lock()
	defer s.responsesMu.Unlock()
	s.responseRoutes[responseID] = responseRouteEntry{route: route, expiresAt: now.Add(responseRouteTTL)}
	// Opportunistically evict expired entries so the map is bounded by live TTL
	// traffic rather than growing for the process lifetime.
	if now.Sub(s.responsesLastSweep) >= responseRouteSweepInterval {
		s.responsesLastSweep = now
		for id, entry := range s.responseRoutes {
			if now.After(entry.expiresAt) {
				delete(s.responseRoutes, id)
			}
		}
	}
}

func (s *HTTPServer) lookupResponseRoute(responseID string) (responseRoute, bool) {
	if responseID == "" {
		return responseRoute{}, false
	}
	s.responsesMu.RLock()
	entry, ok := s.responseRoutes[responseID]
	s.responsesMu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return responseRoute{}, false
	}
	return entry.route, true
}
