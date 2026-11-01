package server

import (
	"errors"
	kerrors "github.com/go-kratos/kratos/v3/errors"
	v "micro-one-api/api/admin/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/pkg/jsonx"
	"net/http"
)

func handleBackupPolicy(w http.ResponseWriter, r *http.Request, s *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		out, err := s.GetBackupPolicy(r.Context(), &v.GetBackupPolicyRequest{})
		writeBackupResponse(w, out, err)
	case http.MethodPut:
		var req v.UpdateBackupPolicyRequest
		if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid backup policy"})
			return
		}
		out, err := s.UpdateBackupPolicy(r.Context(), &req)
		writeBackupResponse(w, out, err)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
func handleBackups(w http.ResponseWriter, r *http.Request, s *service.AdminService) {
	switch r.Method {
	case http.MethodGet:
		out, err := s.ListBackups(r.Context(), &v.ListBackupsRequest{})
		writeBackupResponse(w, out, err)
	case http.MethodPost:
		var req v.CreateBackupRequest
		if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&req); err != nil {
			writeJSON(w, 400, map[string]string{"error": "invalid backup request"})
			return
		}
		out, err := s.CreateBackup(r.Context(), &req)
		writeBackupResponse(w, out, err)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func writeBackupResponse(w http.ResponseWriter, out any, err error) {
	if err != nil {
		var e *kerrors.Error
		if errors.As(err, &e) && e.Code >= 400 && e.Code <= 599 {
			writeJSON(w, int(e.Code), map[string]any{"success": false, "message": e.Message, "reason": e.Reason})
			return
		}
	}
	writeServiceResponse(w, out, err)
}
