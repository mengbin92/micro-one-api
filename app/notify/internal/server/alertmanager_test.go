package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/notify/internal/biz"
	"micro-one-api/app/notify/internal/data"
	"micro-one-api/app/notify/internal/service"
	"micro-one-api/pkg/jsonx"
)

func TestAlertmanagerDeliveryAndRecovery(t *testing.T) {
	t.Setenv("NOTIFY_SQL_DSN", "")
	t.Setenv("SQL_DSN", "")
	repo, err := data.NewRepositoryFromEnv("sqlite3")
	require.NoError(t, err)
	uc := biz.NewNotifyUsecase(repo)
	srv := NewHTTPServer("", service.NewNotifyService(uc))
	received := make(chan string, 2)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Content string `json:"content"`
		}
		require.NoError(t, jsonx.NewDecoder(r.Body).Decode(&body))
		received <- body.Content
		w.WriteHeader(http.StatusNoContent)
	}))
	defer sink.Close()
	dispatch := biz.NewDispatcher(uc, biz.NewMultiSender(biz.SenderConfig{WebhookURL: sink.URL}), time.Second, 20, 1)
	for _, state := range []string{"firing", "resolved"} {
		body := `{"status":"` + state + `","alerts":[{"status":"` + state + `","labels":{"alertname":"CredentialPersistenceDelayed"},"annotations":{"summary":"credential persistence"}}]}`
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/alerts/alertmanager", strings.NewReader(body)))
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		var created struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &created))
		require.NoError(t, dispatch.DispatchOnce(context.Background()))
		n, err := uc.GetNotification(context.Background(), created.ID)
		require.NoError(t, err)
		require.Equal(t, biz.NotifyStatusSent, n.Status)
		require.Contains(t, <-received, `"status":"`+state+`"`)
	}
	_, err = uc.CreateNotification(context.Background(), biz.NotifyTypeWebhook, "", "unconfigured", "test")
	require.NoError(t, err)
	unconfigured := biz.NewDispatcher(uc, biz.NewMultiSender(biz.SenderConfig{}), time.Second, 20, 1)
	require.NoError(t, unconfigured.DispatchOnce(context.Background()))
	failed, total, err := uc.ListNotifications(context.Background(), 1, 20, "", biz.NotifyStatusFailed)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Contains(t, failed[0].LastError, "not configured")
}
