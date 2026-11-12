package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	adminbiz "micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/service"
)

type operationAuditResponse struct {
	EventID     string `json:"event_id"`
	ActorUserID int64  `json:"actor_user_id"`
	// ActorUsername is empty when the actor was deleted or the caller lacks
	// user-read permission; the client then falls back to #actor_user_id.
	ActorUsername    string `json:"actor_username"`
	Operation        string `json:"operation"`
	ResourceID       string `json:"resource_id"`
	DecisionVersions string `json:"decision_versions"`
	Reason           string `json:"reason"`
	OccurredAt       int64  `json:"occurred_at"`
	Result           string `json:"result"`
}

type operationAuditPageResponse struct {
	Items    []operationAuditResponse `json:"items"`
	Total    int64                    `json:"total"`
	Page     int                      `json:"page"`
	PageSize int                      `json:"page_size"`
}

func handleOperationAudits(w http.ResponseWriter, r *http.Request, svc *service.AdminService) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, apiResponse(false, "method not allowed", nil))
		return
	}
	q := r.URL.Query()
	actor, err := optionalQueryInt64(q.Get("actor_user_id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, adminbiz.ErrInvalidOperationAuditQuery.Error(), nil))
		return
	}
	start, err := optionalQueryInt64(q.Get("start_time_ms"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, adminbiz.ErrInvalidOperationAuditQuery.Error(), nil))
		return
	}
	end, err := optionalQueryInt64(q.Get("end_time_ms"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, adminbiz.ErrInvalidOperationAuditQuery.Error(), nil))
		return
	}
	page, err := optionalQueryInt(q.Get("page"), 1)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, adminbiz.ErrInvalidOperationAuditQuery.Error(), nil))
		return
	}
	pageSize, err := optionalQueryInt(q.Get("page_size"), 20)
	if err != nil || (q.Get("page") != "" && page <= 0) || (q.Get("page_size") != "" && pageSize <= 0) {
		writeJSON(w, http.StatusBadRequest, apiResponse(false, adminbiz.ErrInvalidOperationAuditQuery.Error(), nil))
		return
	}
	result, err := svc.ListOperationAudits(r.Context(), adminbiz.OperationAuditFilter{
		ActorUserID: actor,
		Operation:   strings.TrimSpace(q.Get("operation")),
		ResourceID:  strings.TrimSpace(q.Get("resource_id")),
		Result:      q.Get("result"),
		StartTimeMS: start,
		EndTimeMS:   end,
	}, page, pageSize)
	if err != nil {
		code := http.StatusInternalServerError
		message := "operation audit unavailable"
		if errors.Is(err, adminbiz.ErrInvalidOperationAuditQuery) {
			code = http.StatusBadRequest
			message = err.Error()
		} else if errors.Is(err, adminbiz.ErrOperationAuditUnavailable) {
			// A missing DB-backed reader is a temporary capacity failure, not a
			// server error; callers must be able to retry without alerting on 5xx.
			code = http.StatusServiceUnavailable
		} else if status, ok := resourceHTTPErrorCode(err); ok {
			code = status
			message = "authorization denied"
		}
		writeJSON(w, code, apiResponse(false, message, nil))
		return
	}
	items := make([]operationAuditResponse, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, operationAuditResponse{
			EventID: item.EventID, ActorUserID: item.ActorUserID, ActorUsername: item.ActorUsername,
			Operation:  item.Operation,
			ResourceID: item.ResourceID, DecisionVersions: item.DecisionVersions,
			Reason: item.Reason, OccurredAt: item.OccurredAt, Result: item.Result,
		})
	}
	writeJSON(w, http.StatusOK, apiResponse(true, "", operationAuditPageResponse{
		Items: items, Total: result.Total, Page: result.Page, PageSize: result.PageSize,
	}))
}

func optionalQueryInt64(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, nil
	}
	return strconv.ParseInt(value, 10, 64)
}

func optionalQueryInt(value string, fallback int) (int, error) {
	if strings.TrimSpace(value) == "" {
		return fallback, nil
	}
	return strconv.Atoi(value)
}
