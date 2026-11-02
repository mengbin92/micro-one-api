package server

import (
	"net/http"
	"strconv"
	"strings"

	v "micro-one-api/api/admin/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

func handleAnnouncements(s *service.AdminService, managed bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !managed {
			if len(r.Header.Values("Authorization")) != 1 || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
				writeJSON(w, 401, map[string]string{"error": "user session required"})
				return
			}
			ctx := service.WithOperatorCredential(authorization.WithExternal(r.Context()), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			r = r.WithContext(ctx)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/user/model-availability":
			out, err := s.GetMyModelAvailability(r.Context(), &v.GetMyModelAvailabilityRequest{})
			writeBackupResponse(w, out, err)
		case r.Method == http.MethodGet && (r.URL.Path == "/api/admin/announcements" || r.URL.Path == "/api/user/announcements"):
			page, e1 := strconv.ParseInt(defaultQuery(r, "page", "1"), 10, 32)
			size, e2 := strconv.ParseInt(defaultQuery(r, "page_size", "20"), 10, 32)
			if e1 != nil || e2 != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid pagination"})
				return
			}
			req := &v.ListAnnouncementsRequest{Page: int32(page), PageSize: int32(size)}
			if managed {
				out, err := s.ListManagedAnnouncements(r.Context(), req)
				writeBackupResponse(w, out, err)
			} else {
				out, err := s.ListMyAnnouncements(r.Context(), req)
				writeBackupResponse(w, out, err)
			}
		case managed && r.Method == http.MethodPut:
			var req v.SaveAnnouncementRequest
			if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 49152)).Decode(&req); err != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid announcement"})
				return
			}
			out, err := s.SaveAnnouncement(r.Context(), &req)
			writeBackupResponse(w, out, err)
		case !managed && r.Method == http.MethodPost && r.URL.Path == "/api/user/announcements/read":
			var req v.ReadAnnouncementRequest
			if err := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
				writeJSON(w, 400, map[string]string{"error": "invalid read receipt"})
				return
			}
			out, err := s.ReadAnnouncement(r.Context(), &req)
			writeBackupResponse(w, out, err)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}
func defaultQuery(r *http.Request, key, fallback string) string {
	if value := r.URL.Query().Get(key); value != "" {
		return value
	}
	return fallback
}
