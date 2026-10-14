package server

import (
	v "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/service"
	"net/http"
	"strconv"
)

func handleUpstreamContracts(w http.ResponseWriter, r *http.Request, s *service.AdminService) {
	if r.URL.Path == "/api/admin/upstream-contracts/transition" {
		if r.Method != http.MethodPost {
			writeJSON(w, 405, map[string]string{"error": "method not allowed"})
			return
		}
		var p v.TransitionUpstreamContractRequest
		if !decodeBody(w, r, &p) {
			return
		}
		reply, err := s.TransitionUpstreamContract(r.Context(), &p)
		if err != nil {
			writeServiceResponse(w, nil, err)
			return
		}
		writeModelResponse(w, r, reply)
		return
	}
	switch r.Method {
	case http.MethodPost:
		var p v.CreateUpstreamContractRequest
		if !decodeBody(w, r, &p) {
			return
		}
		reply, err := s.CreateUpstreamContract(r.Context(), &p)
		if err != nil {
			writeServiceResponse(w, nil, err)
			return
		}
		writeModelResponse(w, r, reply)
	case http.MethodGet:
		id, err := strconv.ParseInt(r.URL.Query().Get("source_id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, 400, map[string]string{"error": "source_id required"})
			return
		}
		reply, err := s.ListUpstreamContracts(r.Context(), &v.ListUpstreamContractsRequest{Scope: &v.ContractScope{SourceKind: r.URL.Query().Get("source_kind"), SourceId: id, UpstreamModelId: r.URL.Query().Get("upstream_model_id"), Protocol: r.URL.Query().Get("protocol")}, PageSize: getQueryInt32(r, "page_size", 20)})
		if err != nil {
			writeServiceResponse(w, nil, err)
			return
		}
		writeModelResponse(w, r, reply)
	default:
		writeJSON(w, 405, map[string]string{"error": "method not allowed"})
	}
}
