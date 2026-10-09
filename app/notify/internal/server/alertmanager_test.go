package server

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
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
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"monitor":"test-monitor-token"}`)
	t.Setenv("NOTIFY_SQL_DSN", "")
	t.Setenv("SQL_DSN", "")
	repo, err := data.NewRepositoryFromEnv("sqlite3")
	require.NoError(t, err)
	uc := biz.NewNotifyUsecase(repo)
	srv := NewHTTPServer("", service.NewNotifyService(uc, "", ""))
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
		req := httptest.NewRequest(http.MethodPost, "/v1/alerts/alertmanager", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-monitor-token")
		srv.ServeHTTP(rec, req)
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

func TestAlertmanagerNotifyTypeConfigurable(t *testing.T) {
	t.Setenv("PROVIDER_DISABLE_SSRF_CHECK", "true")
	t.Setenv("SERVICE_CALLER_TOKENS", `{"monitor":"test-monitor-token"}`)
	t.Setenv("NOTIFY_SQL_DSN", "")
	t.Setenv("SQL_DSN", "")
	repo, err := data.NewRepositoryFromEnv("sqlite3")
	require.NoError(t, err)
	uc := biz.NewNotifyUsecase(repo)
	srv := NewHTTPServer("", service.NewNotifyService(uc, biz.NotifyTypeWeCom, ""))
	received := make(chan []byte, 2)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer sink.Close()
	dispatch := biz.NewDispatcher(uc, biz.NewMultiSender(biz.SenderConfig{WeComWebhookURL: sink.URL}), time.Second, 20, 1)
	for _, state := range []string{"firing", "resolved"} {
		body := `{"status":"` + state + `","alerts":[{"status":"` + state + `","labels":{"alertname":"Probe"},"annotations":{}}]}`
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/alerts/alertmanager", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-monitor-token")
		srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		var created struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &created))
		require.NoError(t, dispatch.DispatchOnce(context.Background()))
		n, err := uc.GetNotification(context.Background(), created.ID)
		require.NoError(t, err)
		require.Equal(t, biz.NotifyTypeWeCom, n.Type)
		require.Equal(t, biz.NotifyStatusSent, n.Status)
		var message struct {
			MsgType string `json:"msgtype"`
			Text    struct {
				Content string `json:"content"`
			} `json:"text"`
		}
		require.NoError(t, jsonx.Unmarshal(<-received, &message))
		require.Equal(t, "text", message.MsgType)
		require.Contains(t, message.Text.Content, `[monitor:`+state+`]`)
		require.Contains(t, message.Text.Content, `"status":"`+state+`"`)
	}
}

// fakeSMTPServer is a minimal SMTP listener that records every delivered
// message (one smtp.SendMail call per connection) and supports AUTH PLAIN
// (required because smtp.SendMail authenticates whenever credentials are
// configured; 127.0.0.1 counts as localhost so PlainAuth is allowed without
// TLS).
type fakeSMTPServer struct {
	ln      net.Listener
	message chan string
}

func startFakeSMTPServer(t *testing.T) *fakeSMTPServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	s := &fakeSMTPServer{ln: ln, message: make(chan string, 4)}
	go s.serve()
	t.Cleanup(func() { s.ln.Close() })
	return s
}

func (s *fakeSMTPServer) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(conn)
	}
}

func (s *fakeSMTPServer) handle(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	w := bufio.NewWriter(conn)
	write := func(line string) {
		_, _ = w.WriteString(line + "\r\n")
		_ = w.Flush()
	}
	write("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			write("250-127.0.0.1 greets you")
			write("250 AUTH PLAIN")
		case strings.HasPrefix(line, "AUTH PLAIN"):
			write("235 authenticated")
		case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
			write("250 ok")
		case line == "DATA":
			write("354 go ahead")
			var msg strings.Builder
			for {
				dataLine, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if dataLine == ".\r\n" {
					break
				}
				msg.WriteString(dataLine)
			}
			s.message <- msg.String()
			write("250 queued")
		case strings.HasPrefix(line, "QUIT"):
			write("221 bye")
			return
		default:
			write("250 ok")
		}
	}
}

func TestAlertmanagerEmailDelivery(t *testing.T) {
	t.Setenv("SERVICE_CALLER_TOKENS", `{"monitor":"test-monitor-token"}`)
	t.Setenv("NOTIFY_SQL_DSN", "")
	t.Setenv("SQL_DSN", "")
	repo, err := data.NewRepositoryFromEnv("sqlite3")
	require.NoError(t, err)
	uc := biz.NewNotifyUsecase(repo)
	const recipient = "ops@example.com"
	srv := NewHTTPServer("", service.NewNotifyService(uc, biz.NotifyTypeEmail, recipient))
	smtp := startFakeSMTPServer(t)
	host, portStr, err := net.SplitHostPort(smtp.ln.Addr().String())
	require.NoError(t, err)
	var port int
	_, err = fmt.Sscanf(portStr, "%d", &port)
	require.NoError(t, err)
	dispatch := biz.NewDispatcher(uc, biz.NewMultiSender(biz.SenderConfig{
		SMTPHost: host,
		SMTPPort: port,
		SMTPUser: "sender@example.com",
		SMTPPass: "secret",
		SMTPFrom: "sender@example.com",
	}), time.Second, 20, 1)
	for _, state := range []string{"firing", "resolved"} {
		body := `{"status":"` + state + `","alerts":[{"status":"` + state + `","labels":{"alertname":"RedisOutboxBacklog"},"annotations":{"summary":"outbox backlog"}}]}`
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/v1/alerts/alertmanager", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-monitor-token")
		srv.ServeHTTP(rec, req)
		require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
		var created struct {
			ID int64 `json:"id"`
		}
		require.NoError(t, jsonx.Unmarshal(rec.Body.Bytes(), &created))
		require.NoError(t, dispatch.DispatchOnce(context.Background()))
		n, err := uc.GetNotification(context.Background(), created.ID)
		require.NoError(t, err)
		require.Equal(t, biz.NotifyTypeEmail, n.Type)
		require.Equal(t, recipient, n.Recipient)
		require.Equal(t, biz.NotifyStatusSent, n.Status)
		msg := <-smtp.message
		require.Contains(t, msg, "To: "+recipient)
		require.Contains(t, msg, "Subject: [monitor:"+state+"] 1 alerts")
		require.Contains(t, msg, `"status":"`+state+`"`)
	}
}
