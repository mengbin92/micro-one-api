package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	logv1 "micro-one-api/api/log/v1"
	"micro-one-api/app/admin/internal/service"
)

func handleRequestTrace(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, apiResponse(false, "method not allowed", nil))
		return
	}
	path := strings.Trim(r.URL.Path, "/")
	if path == "api/log/request-traces" {
		q := r.URL.Query()
		req := &logv1.ListRequestTracesRequest{Outcome: q.Get("outcome"), RootRequestId: q.Get("root_request_id"), PageToken: q.Get("page_token")}
		for key, target := range map[string]*int64{"start_time_ms": &req.StartTimeMs, "end_time_ms": &req.EndTimeMs} {
			if value := q.Get(key); value != "" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					writeJSON(w, 400, apiResponse(false, "invalid time range", nil))
					return
				}
				*target = n
			}
		}
		if value := q.Get("page_size"); value != "" {
			n, err := strconv.ParseInt(value, 10, 32)
			if err != nil {
				writeJSON(w, 400, apiResponse(false, "invalid page size", nil))
				return
			}
			req.PageSize = int32(n)
		}
		if value := q.Get("user_id"); value != "" {
			n, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				writeJSON(w, 400, apiResponse(false, "invalid user id", nil))
				return
			}
			req.UserId = &n
		}
		view, err := svc.ListRequestTraces(r.Context(), req)
		writeServiceResponse(w, view, err)
		return
	}
	if strings.HasPrefix(path, "api/log/request-traces/") {
		id := strings.TrimPrefix(path, "api/log/request-traces/")
		if _, err := uuid.Parse(id); err != nil {
			writeJSON(w, 400, apiResponse(false, "invalid observation id", nil))
			return
		}
		view, err := svc.GetRequestTrace(r.Context(), id)
		writeServiceResponse(w, view, err)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(path, "api/log/"), "/trace")
	ledgerID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || ledgerID <= 0 {
		writeJSON(w, 400, apiResponse(false, "invalid ledger id", nil))
		return
	}
	view, err := svc.LedgerRequestTraces(r.Context(), ledgerID)
	writeServiceResponse(w, view, err)
}
