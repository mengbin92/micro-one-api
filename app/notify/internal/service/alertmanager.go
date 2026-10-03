package service

import (
	"fmt"
	"io"
	"micro-one-api/platform/authz"
	"net/http"

	"micro-one-api/pkg/jsonx"
)

// Alertmanager sends grouped state changes. Persist the group before acknowledging
// it; delivery and retries remain owned by the existing notification dispatcher.
func (s *NotifyService) HandleAlertmanager(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var payload struct {
		Status string `json:"status"`
		Alerts []struct {
			Status      string            `json:"status"`
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
			StartsAt    string            `json:"startsAt"`
			EndsAt      string            `json:"endsAt"`
			Fingerprint string            `json:"fingerprint"`
		} `json:"alerts"`
	}
	decoder := jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if decoder.Decode(&payload) != nil || (payload.Status != "firing" && payload.Status != "resolved") || len(payload.Alerts) == 0 {
		writeError(w, http.StatusBadRequest, "invalid alert group")
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid alert group")
		return
	}
	for _, alert := range payload.Alerts {
		if (alert.Status != "firing" && alert.Status != "resolved") || alert.Labels["alertname"] == "" {
			writeError(w, http.StatusBadRequest, "invalid alert")
			return
		}
	}
	content, err := jsonx.Marshal(payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid alert group")
		return
	}
	notifications, err := s.uc.DispatchEvent(r.Context(), "alertmanager", fmt.Sprintf("[monitor:%s] %d alerts", payload.Status, len(payload.Alerts)), string(content), s.alertmanagerNotifyType, s.alertmanagerRecipient())
	if err != nil {
		authz.WriteHTTPError(w, err)
		return
	}
	if len(notifications) == 1 {
		writeJSON(w, http.StatusAccepted, notificationToMap(notifications[0]))
		return
	}
	items := make([]map[string]any, 0, len(notifications))
	for _, n := range notifications {
		items = append(items, notificationToMap(n))
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"items": items})
}
