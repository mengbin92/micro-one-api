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
