package server

import (
	v "micro-one-api/api/billing/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"strconv"
)

func handleVendorReports(w http.ResponseWriter, r *http.Request, s *service.AdminService) {
	switch r.Method {
	case http.MethodPost:
		var p v.ImportVendorReportRequest
		if !decodeBody(w, r, &p) {
			return
		}
		reply, err := s.ImportVendorReport(r.Context(), &p)
		if err != nil {
			writeServiceResponse(w, nil, err)
			return
		}
		writeJSON(w, 200, map[string]any{"report_id": reply.ReportId, "report": jsonx.RawMessage(reply.ReportJson)})
	case http.MethodGet:
		start, err := strconv.ParseInt(r.URL.Query().Get("start"), 10, 64)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "fixed start/end required"})
			return
		}
		end, err := strconv.ParseInt(r.URL.Query().Get("end"), 10, 64)
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": "fixed start/end required"})
			return
		}
		reply, err := s.ListVendorReports(r.Context(), &v.ListVendorReportsRequest{Start: start, End: end, Limit: getQueryInt32(r, "limit", 20)})
		if err != nil {
			writeServiceResponse(w, nil, err)
			return
		}
		var rows []jsonx.RawMessage
		for _, r := range reply.ReportsJson {
			rows = append(rows, jsonx.RawMessage(r))
		}
		writeJSON(w, 200, map[string]any{"reports": rows, "daily_coverage": jsonx.RawMessage(reply.DailySummaryJson)})
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}
