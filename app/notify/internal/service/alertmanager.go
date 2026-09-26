package service

import (
	"fmt"
	"io"
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
	n, err := s.uc.CreateNotification(r.Context(), s.alertmanagerNotifyType, "", fmt.Sprintf("[monitor:%s] %d alerts", payload.Status, len(payload.Alerts)), string(content))
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "could not queue alert group")
		return
	}
	writeJSON(w, http.StatusAccepted, notificationToMap(n))
}
