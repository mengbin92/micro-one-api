package server

import (
	"github.com/go-kratos/kratos/v3/errors"
	"micro-one-api/app/admin/internal/service"
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
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/admin/routing-groups/"), 10, 64)
	if err != nil {
		routingGroupError(w, errors.BadRequest("ROUTING_GROUP_INVALID", "invalid id"))
		return
	}
	result, err := svc.GetRoutingGroup(r.Context(), id)
	if err != nil {
		routingGroupError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", result))
}
