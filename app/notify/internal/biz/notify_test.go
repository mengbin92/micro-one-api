package biz

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockNotifyRepo struct {
	entries map[int64]*Notification
	seq     int64
}

func (m *mockNotifyRepo) Create(ctx context.Context, n *Notification) error {
	m.seq++
	n.ID = m.seq
	m.entries[n.ID] = n
	return nil
}

func (m *mockNotifyRepo) Get(ctx context.Context, id int64) (*Notification, error) {
	n, ok := m.entries[id]
	if !ok {
		return nil, ErrNotificationNotFound
	}
	return n, nil
}

func (m *mockNotifyRepo) List(ctx context.Context, page, pageSize int32, notifyType, status string) ([]*Notification, int64, error) {
	var result []*Notification
	for _, n := range m.entries {
		if notifyType != "" && n.Type != notifyType {
			continue
		}
		if status != "" && n.Status != status {
			continue
		}
		result = append(result, n)
	}
	total := int64(len(result))
	start := int((page - 1) * pageSize)
	if start >= len(result) {
		return nil, total, nil
	}
	end := min(start+int(pageSize), len(result))
	return result[start:end], total, nil
}

func (m *mockNotifyRepo) UpdateStatus(ctx context.Context, id int64, status string) error {
	n, ok := m.entries[id]
	if !ok {
		return ErrNotificationNotFound
	}
	n.Status = status
	if status == NotifyStatusSent {
		n.SentAt = time.Now()
	}
	return nil
}

func (m *mockNotifyRepo) ListPending(ctx context.Context, limit int32, maxRetry int) ([]*Notification, error) {
	items := make([]*Notification, 0)
	for _, n := range m.entries {
		if n.Status != NotifyStatusPending || n.RetryCount >= maxRetry {
			continue
		}
		items = append(items, n)
		if int32(len(items)) >= limit {
			break
		}
	}
	return items, nil
}

func (m *mockNotifyRepo) MarkFailed(ctx context.Context, id int64) error {
	n, ok := m.entries[id]
	if !ok {
		return ErrNotificationNotFound
	}
	n.Status = NotifyStatusFailed
	return nil
}

func (m *mockNotifyRepo) RecordFailure(ctx context.Context, id int64, maxRetry int, lastError string) error {
	n, ok := m.entries[id]
	if !ok {
		return ErrNotificationNotFound
	}
	n.RetryCount++
	if n.RetryCount >= maxRetry {
		n.Status = NotifyStatusFailed
	}
	n.LastError = lastError
	return nil
}

func newMockNotifyRepo() *mockNotifyRepo {
	return &mockNotifyRepo{entries: make(map[int64]*Notification)}
}

func TestNotifyUsecase_CreateNotification(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)

	t.Run("success", func(t *testing.T) {
		n, err := uc.CreateNotification(context.Background(), NotifyTypeWebhook, "https://example.com", "alert", "server down")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n.ID == 0 {
			t.Fatal("expected ID to be set")
		}
		if n.Status != NotifyStatusPending {
			t.Fatalf("expected pending, got %s", n.Status)
		}
		if n.CreatedAt.IsZero() {
			t.Fatal("expected CreatedAt to be set")
		}
		if n.Type != NotifyTypeWebhook {
			t.Fatalf("expected webhook, got %s", n.Type)
		}
		if n.Recipient != "https://example.com" {
			t.Fatalf("unexpected recipient: %s", n.Recipient)
		}
	})

	t.Run("empty type", func(t *testing.T) {
		_, err := uc.CreateNotification(context.Background(), "", "r", "s", "c")
		if err != ErrInvalidNotification {
			t.Fatalf("expected ErrInvalidNotification, got %v", err)
		}
	})

	t.Run("empty recipient", func(t *testing.T) {
		_, err := uc.CreateNotification(context.Background(), NotifyTypeEmail, "", "s", "c")
		if err != ErrInvalidNotification {
			t.Fatalf("expected ErrInvalidNotification, got %v", err)
		}
	})

	t.Run("event allows empty recipient for configured fallback", func(t *testing.T) {
		n, err := uc.CreateNotification(context.Background(), NotifyTypeEvent, "", "s", "c")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n.Recipient != "" {
			t.Fatalf("expected empty recipient, got %s", n.Recipient)
		}
	})
}

func TestNotifyUsecase_GetNotification(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)

	n, _ := uc.CreateNotification(context.Background(), NotifyTypeEmail, "a@b.com", "hi", "hello")

	t.Run("success", func(t *testing.T) {
		got, err := uc.GetNotification(context.Background(), n.ID)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.Subject != "hi" {
			t.Fatalf("expected hi, got %s", got.Subject)
		}
	})

	t.Run("not found", func(t *testing.T) {
		_, err := uc.GetNotification(context.Background(), 999)
		if err != ErrNotificationNotFound {
			t.Fatalf("expected ErrNotificationNotFound, got %v", err)
		}
	})
}

func TestNotifyUsecase_ListNotifications(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)

	uc.CreateNotification(context.Background(), NotifyTypeWebhook, "a", "s1", "c1")
	uc.CreateNotification(context.Background(), NotifyTypeEmail, "b", "s2", "c2")
	uc.CreateNotification(context.Background(), NotifyTypeWebhook, "c", "s3", "c3")

	t.Run("all", func(t *testing.T) {
		items, total, err := uc.ListNotifications(context.Background(), 1, 50, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3, got %d", len(items))
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		items, total, err := uc.ListNotifications(context.Background(), 1, 50, NotifyTypeWebhook, "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 2 {
			t.Fatalf("expected 2, got %d", total)
		}
		if len(items) != 2 {
			t.Fatalf("expected 2, got %d", len(items))
		}
	})

	t.Run("filter by status", func(t *testing.T) {
		items, total, err := uc.ListNotifications(context.Background(), 1, 50, "", NotifyStatusPending)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
		if len(items) != 3 {
			t.Fatalf("expected 3, got %d", len(items))
		}
	})

	t.Run("normalizes page", func(t *testing.T) {
		_, total, err := uc.ListNotifications(context.Background(), 0, 50, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if total != 3 {
			t.Fatalf("expected 3, got %d", total)
		}
	})

	t.Run("normalizes pageSize", func(t *testing.T) {
		_, _, err := uc.ListNotifications(context.Background(), 1, 0, "", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestNotifyUsecase_MarkSent(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)

	n, _ := uc.CreateNotification(context.Background(), NotifyTypeEvent, "svc", "s", "c")

	err := uc.MarkSent(context.Background(), n.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := uc.GetNotification(context.Background(), n.ID)
	if got.Status != NotifyStatusSent {
		t.Fatalf("expected sent, got %s", got.Status)
	}
}

func TestNotifyUsecase_MarkFailed(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)

	n, _ := uc.CreateNotification(context.Background(), NotifyTypeEvent, "svc", "s", "c")

	err := uc.MarkFailed(context.Background(), n.ID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, _ := uc.GetNotification(context.Background(), n.ID)
	if got.Status != NotifyStatusFailed {
		t.Fatalf("expected failed, got %s", got.Status)
	}
}

func TestNotifyUsecase_RecordFailureRetriesBeforeFailed(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)
	n, _ := uc.CreateNotification(context.Background(), NotifyTypeWebhook, "https://example.com", "s", "c")

	if err := uc.RecordFailure(context.Background(), n.ID, 2, "webhook returned status 500"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := uc.GetNotification(context.Background(), n.ID)
	if got.Status != NotifyStatusPending || got.RetryCount != 1 || got.LastError != "webhook returned status 500" {
		t.Fatalf("expected pending retry 1 with error, got status=%s retry=%d error=%q", got.Status, got.RetryCount, got.LastError)
	}

	if err := uc.RecordFailure(context.Background(), n.ID, 2, "timeout"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ = uc.GetNotification(context.Background(), n.ID)
	if got.Status != NotifyStatusFailed || got.RetryCount != 2 || got.LastError != "timeout" {
		t.Fatalf("expected failed retry 2 with error, got status=%s retry=%d error=%q", got.Status, got.RetryCount, got.LastError)
	}
}

type stubSender struct {
	err error
}

func (s stubSender) Send(ctx context.Context, n *Notification) error {
	return s.err
}

func TestDispatcherDispatchOnceMarksSent(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)
	n, _ := uc.CreateNotification(context.Background(), NotifyTypeWebhook, "https://example.com", "s", "c")
	dispatcher := NewDispatcher(uc, stubSender{}, time.Second, 10, 3)

	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := uc.GetNotification(context.Background(), n.ID)
	if got.Status != NotifyStatusSent {
		t.Fatalf("expected sent, got %s", got.Status)
	}
}

func TestDispatcherDispatchOnceRecordsFailure(t *testing.T) {
	repo := newMockNotifyRepo()
	uc := NewNotifyUsecase(repo)
	n, _ := uc.CreateNotification(context.Background(), NotifyTypeWebhook, "https://example.com", "s", "c")
	dispatcher := NewDispatcher(uc, stubSender{err: errors.New("send failed")}, time.Second, 10, 2)

	if err := dispatcher.DispatchOnce(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, _ := uc.GetNotification(context.Background(), n.ID)
	if got.Status != NotifyStatusPending || got.RetryCount != 1 || got.LastError != "send failed" {
		t.Fatalf("expected pending retry 1 with error, got status=%s retry=%d error=%q", got.Status, got.RetryCount, got.LastError)
	}
}

func TestMultiSenderWebhook(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected json content type, got %s", r.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	sender := NewMultiSender(SenderConfig{})
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeWebhook,
		Recipient: srv.URL,
		Subject:   "alert",
		Content:   "content",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("expected webhook to be called")
	}
}

func TestNotifyConstants(t *testing.T) {
	if NotifyTypeWebhook != "webhook" {
		t.Fatalf("expected webhook, got %s", NotifyTypeWebhook)
	}
	if NotifyTypeEmail != "email" {
		t.Fatalf("expected email, got %s", NotifyTypeEmail)
	}
	if NotifyTypeEvent != "event" {
		t.Fatalf("expected event, got %s", NotifyTypeEvent)
	}
	if NotifyTypeWeCom != "wecom" {
		t.Fatalf("expected wecom, got %s", NotifyTypeWeCom)
	}
	if NotifyTypeDingTalk != "dingtalk" {
		t.Fatalf("expected dingtalk, got %s", NotifyTypeDingTalk)
	}
	if NotifyTypeFeishu != "feishu" {
		t.Fatalf("expected feishu, got %s", NotifyTypeFeishu)
	}
	if NotifyTypeSlack != "slack" {
		t.Fatalf("expected slack, got %s", NotifyTypeSlack)
	}
	if NotifyStatusPending != "pending" {
		t.Fatalf("expected pending, got %s", NotifyStatusPending)
	}
	if NotifyStatusSent != "sent" {
		t.Fatalf("expected sent, got %s", NotifyStatusSent)
	}
	if NotifyStatusFailed != "failed" {
		t.Fatalf("expected failed, got %s", NotifyStatusFailed)
	}
}

// TestMultiSenderWeCom tests enterprise wecom notification sending.
func TestMultiSenderWeCom(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("expected json content type, got %s", r.Header.Get("Content-Type"))
		}
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewMultiSender(SenderConfig{WeComWebhookURL: srv.URL})
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeWeCom,
		Recipient: "",
		Subject:   "Test Alert",
		Content:   "This is a test message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload["msgtype"] != "text" {
		t.Fatalf("expected msgtype text, got %v", payload["msgtype"])
	}
}

// TestMultiSenderDingTalk tests dingtalk notification sending.
func TestMultiSenderDingTalk(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewMultiSender(SenderConfig{DingTalkWebhookURL: srv.URL})
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeDingTalk,
		Recipient: "",
		Subject:   "Test Alert",
		Content:   "This is a test message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload["msgtype"] != "text" {
		t.Fatalf("expected msgtype text, got %v", payload["msgtype"])
	}
}

// TestMultiSenderFeishu tests feishu notification sending.
func TestMultiSenderFeishu(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewMultiSender(SenderConfig{FeishuWebhookURL: srv.URL})
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeFeishu,
		Recipient: "",
		Subject:   "Test Alert",
		Content:   "This is a test message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload["msg_type"] != "text" {
		t.Fatalf("expected msg_type text, got %v", payload["msg_type"])
	}
}

// TestMultiSenderSlack tests slack notification sending.
func TestMultiSenderSlack(t *testing.T) {
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		var err error
		receivedBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	sender := NewMultiSender(SenderConfig{SlackWebhookURL: srv.URL})
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeSlack,
		Recipient: "",
		Subject:   "Test Alert",
		Content:   "This is a test message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("failed to parse payload: %v", err)
	}
	if payload["text"] == nil {
		t.Fatalf("expected text field in payload")
	}
}

// TestNormalizeWeComWebhookURL tests the WeCom URL normalization function.
func TestNormalizeWeComWebhookURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bare key",
			input:    "test-key-12345",
			expected: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=test-key-12345",
		},
		{
			name:     "full URL",
			input:    "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=existing",
			expected: "https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=existing",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeWeComWebhookURL(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeWeComWebhookURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestNormalizeDingTalkWebhookURL tests the DingTalk URL normalization function.
func TestNormalizeDingTalkWebhookURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "bare token",
			input:    "test-token-67890",
			expected: "https://oapi.dingtalk.com/robot/send?access_token=test-token-67890",
		},
		{
			name:     "full URL",
			input:    "https://oapi.dingtalk.com/robot/send?access_token=existing",
			expected: "https://oapi.dingtalk.com/robot/send?access_token=existing",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeDingTalkWebhookURL(tt.input)
			if result != tt.expected {
				t.Errorf("normalizeDingTalkWebhookURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestMultiSenderWeComRecipientOverride tests recipient overriding config with full URL.
func TestMultiSenderWeComRecipientOverride(t *testing.T) {
	var hitCount int
	var receivedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		receivedURL = r.URL.String()
		// Verify the URL contains the recipient's key parameter, not config's
		if !strings.Contains(receivedURL, "key=recipient-key") {
			t.Errorf("expected recipient key parameter in URL, got %s", receivedURL)
		}
		if strings.Contains(receivedURL, "key=config-key") {
			t.Errorf("should not contain config key parameter in URL, got %s", receivedURL)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Config has one key
	sender := NewMultiSender(SenderConfig{WeComWebhookURL: srv.URL + "?key=config-key"})
	// Recipient overrides with different key
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeWeCom,
		Recipient: srv.URL + "?key=recipient-key",
		Subject:   "Test",
		Content:   "Message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify our server was actually hit
	if hitCount != 1 {
		t.Fatalf("expected httptest server to be hit once, got %d", hitCount)
	}
}

// TestMultiSenderWeComRecipientOnly tests sending with recipient but no global config.
func TestMultiSenderWeComRecipientOnly(t *testing.T) {
	var hitCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// No global config configured
	sender := NewMultiSender(SenderConfig{})
	// Recipient provides the full URL
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeWeCom,
		Recipient: srv.URL + "?key=test-key",
		Subject:   "Test",
		Content:   "Message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hitCount != 1 {
		t.Fatalf("expected httptest server to be hit once, got %d", hitCount)
	}
}

// TestMultiSenderDingTalkRecipientOnly tests sending with recipient but no global config.
func TestMultiSenderDingTalkRecipientOnly(t *testing.T) {
	var hitCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// No global config configured
	sender := NewMultiSender(SenderConfig{})
	// Recipient provides the full URL
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeDingTalk,
		Recipient: srv.URL + "?access_token=test-token",
		Subject:   "Test",
		Content:   "Message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hitCount != 1 {
		t.Fatalf("expected httptest server to be hit once, got %d", hitCount)
	}
}

// TestMultiSenderFeishuRecipientOnly tests sending with recipient but no global config.
func TestMultiSenderFeishuRecipientOnly(t *testing.T) {
	var hitCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// No global config configured
	sender := NewMultiSender(SenderConfig{})
	// Recipient provides the full URL
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeFeishu,
		Recipient: srv.URL,
		Subject:   "Test",
		Content:   "Message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hitCount != 1 {
		t.Fatalf("expected httptest server to be hit once, got %d", hitCount)
	}
}

// TestMultiSenderSlackRecipientOnly tests sending with recipient but no global config.
func TestMultiSenderSlackRecipientOnly(t *testing.T) {
	var hitCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// No global config configured
	sender := NewMultiSender(SenderConfig{})
	// Recipient provides the full URL
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeSlack,
		Recipient: srv.URL,
		Subject:   "Test",
		Content:   "Message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hitCount != 1 {
		t.Fatalf("expected httptest server to be hit once, got %d", hitCount)
	}
}
func TestMultiSenderDingTalkRecipientOverride(t *testing.T) {
	var hitCount int
	var receivedURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount++
		receivedURL = r.URL.String()
		// Verify the URL contains the recipient's token parameter, not config's
		if !strings.Contains(receivedURL, "access_token=recipient-token") {
			t.Errorf("expected recipient token parameter in URL, got %s", receivedURL)
		}
		if strings.Contains(receivedURL, "access_token=config-token") {
			t.Errorf("should not contain config token parameter in URL, got %s", receivedURL)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Config has one token
	sender := NewMultiSender(SenderConfig{DingTalkWebhookURL: srv.URL + "?access_token=config-token"})
	// Recipient overrides with different token
	err := sender.Send(context.Background(), &Notification{
		ID:        1,
		Type:      NotifyTypeDingTalk,
		Recipient: srv.URL + "?access_token=recipient-token",
		Subject:   "Test",
		Content:   "Message",
		CreatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify our server was actually hit
	if hitCount != 1 {
		t.Fatalf("expected httptest server to be hit once, got %d", hitCount)
	}
}
