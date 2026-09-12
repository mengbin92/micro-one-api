package server

import (
	"github.com/go-kratos/kratos/v3/errors"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"strconv"
	"strings"
)

func routingGroupError(w http.ResponseWriter, err error) {
	e := errors.FromError(err)
	code, message := int(e.Code), "分组服务暂不可用"
	switch code {
	case http.StatusBadRequest:
		message = "无效的分组查询"
	case http.StatusForbidden:
		message = "无权使用此分组"
	case http.StatusConflict:
		message = "分组设置已变更，请刷新后重试"
	case http.StatusNotFound:
		message = "分组不存在"
	default:
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, apiResponse(false, message, nil))
}
func handleRoutingGroups(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	var size int64
	var err error
	if q.Get("page_size") != "" {
		size, err = strconv.ParseInt(q.Get("page_size"), 10, 32)
	}
	if err != nil {
		routingGroupError(w, errors.BadRequest("ROUTING_GROUP_INVALID", "invalid page size"))
		return
	}
	result, err := svc.ListRoutingGroups(r.Context(), int32(size), q.Get("page_token"), q.Get("filter"), q.Get("order_by"))
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", result))
}
func handleRoutingGroupByID(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/admin/routing-groups/")
	billing := strings.HasSuffix(path, "/billing")
	if billing {
		path = strings.TrimSuffix(path, "/billing")
	}
	if strings.HasSuffix(path, "/resource-overrides") {
		id, err := strconv.ParseInt(strings.TrimSuffix(path, "/resource-overrides"), 10, 64)
		if err != nil || id <= 0 {
			routingGroupError(w, errors.BadRequest("ROUTING_GROUP_INVALID", "invalid resource override path"))
			return
		}
		handleRoutingResourceOverrides(w, r, svc, id)
		return
	}
	if parts := strings.Split(path, "/user-price/"); len(parts) == 2 {
		id, err := strconv.ParseInt(parts[0], 10, 64)
		userID, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || err2 != nil || id <= 0 || userID <= 0 {
			routingGroupError(w, errors.BadRequest("ROUTING_GROUP_INVALID", "invalid user price path"))
			return
		}
		handleRoutingGroupUserPrice(w, r, svc, id, userID)
		return
	}
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil {
		routingGroupError(w, errors.BadRequest("ROUTING_GROUP_INVALID", "invalid id"))
		return
	}
	if billing {
		handleRoutingBillingPolicy(w, r, svc, id)
		return
	}
	if r.Method == http.MethodPatch {
		var body service.RoutingGroupStateRequest
		if jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		err := svc.SetRoutingGroupState(r.Context(), id, body)
		routingMutationAudit(r, 0, "routing_group", strconv.FormatInt(id, 10), "state", err)
		if err != nil {
			routingGroupError(w, err)
			return
		}
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := svc.GetRoutingGroup(r.Context(), id)
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", result))
}

func handleRoutingGroupUserPrice(w http.ResponseWriter, r *http.Request, svc *service.AdminService, id, userID int64) {
	switch r.Method {
	case http.MethodPut:
		var body service.RoutingUserPriceRequest
		if jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		result, err := svc.SetRoutingGroupUserPrice(r.Context(), id, userID, body)
		routingMutationAudit(r, userID, "routing_group", strconv.FormatInt(id, 10), "user_price", err)
		if err != nil {
			routingGroupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", result))
	case http.MethodDelete:
		err := svc.ClearRoutingGroupUserPrice(r.Context(), id, userID)
		routingMutationAudit(r, userID, "routing_group", strconv.FormatInt(id, 10), "user_price_clear", err)
		if err != nil {
			routingGroupError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, apiResponse(true, "", nil))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func handleRoutingBillingPolicy(w http.ResponseWriter, r *http.Request, svc *service.AdminService, id int64) {
	var update *service.RoutingBillingPolicyDTO
	if r.Method == http.MethodPatch {
		update = &service.RoutingBillingPolicyDTO{}
		if jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(update) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	} else if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	result, err := svc.RoutingBillingPolicy(r.Context(), id, update)
	if update != nil {
		routingMutationAudit(r, 0, "routing_group", strconv.FormatInt(id, 10), "billing_policy", err)
	}
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", result))
}

func handleRoutingResourceOverrides(w http.ResponseWriter, r *http.Request, svc *service.AdminService, id int64) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body service.RoutingResourceOverrideRequest
	if jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&body) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	err := svc.SetRoutingGroupResourceOverrides(r.Context(), id, body)
	routingMutationAudit(r, 0, "routing_group", strconv.FormatInt(id, 10), "resource_overrides", err)
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", nil))
}
