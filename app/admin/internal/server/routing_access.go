package server

import (
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/routing"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/audit"
	"net/http"
	"strconv"
	"strings"
)

// The principal always comes from the authenticated session, never a body ID.
func routingPrincipal(w http.ResponseWriter, r *http.Request, s *service.AdminService) (int64, bool) {
	user, _, err := s.AuthorizeAdminToken(r.Context(), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	if err != nil || user <= 0 {
		writeJSON(w, http.StatusUnauthorized, apiResponse(false, "请先登录", nil))
		return 0, false
	}
	return user, true
}
func handleRoutingAvailable(w http.ResponseWriter, r *http.Request, s *service.AdminService) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	user, ok := routingPrincipal(w, r, s)
	if !ok {
		return
	}
	q := r.URL.Query()
	size := int64(50)
	var err error
	if q.Get("page_size") != "" {
		size, err = strconv.ParseInt(q.Get("page_size"), 10, 32)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	out, err := s.AvailableGroups(r.Context(), user, routing.GroupListRequest{PageSize: int32(size), PageToken: q.Get("page_token"), Filter: q.Get("filter"), OrderBy: q.Get("order_by")})
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", out))
}
func handleRoutingAccess(w http.ResponseWriter, r *http.Request, s *service.AdminService, user int64, self bool) {
	if r.Method == http.MethodGet {
		out, err := s.RoutingFacts(r.Context(), user)
		if err != nil {
			routingGroupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", out))
		return
	}
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body service.RoutingAccessRequest
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	out, err := s.ChangeRoutingAccess(r.Context(), user, body, self)
	routingMutationAudit(r, user, "routing_access", strconv.FormatInt(user, 10), body.Operation+":"+body.SourceType+":"+body.SourceRef+":"+strconv.FormatInt(body.GroupID, 10), err)
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", out))
}
func handleRoutingToken(w http.ResponseWriter, r *http.Request, s *service.AdminService) {
	user, ok := routingPrincipal(w, r, s)
	if !ok {
		return
	}
	create := r.URL.Path == "/api/v1/routing-tokens"
	if create && r.Method != http.MethodPost || !create && r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body service.RoutingTokenRequest
	if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	var out map[string]any
	var err error
	if create {
		out, err = s.CreateRoutingToken(r.Context(), user, body)
	} else {
		token, parseErr := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/routing-tokens/"), 10, 64)
		if parseErr != nil || token <= 0 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		out, err = s.SetRoutingToken(r.Context(), user, token, body)
	}
	routingMutationAudit(r, user, "token_routing", r.URL.Path, r.Method, err)
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", out))
}

func routingMutationAudit(r *http.Request, userID int64, resource, id, operation string, err error) {
	actor := adminActorFromRequest(r)
	if actor.UserID == 0 && actor.Username == "" {
		actor.UserID = userID
	}
	target := audit.ResourceInfo{Type: resource, ID: id}
	if err != nil {
		adminAuditor().LogFailure(r.Context(), audit.EventTypePermission, actor, target, operation, err)
	} else {
		adminAuditor().LogSuccess(r.Context(), audit.EventTypePermission, actor, target, operation)
	}
}
