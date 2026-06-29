package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuditor_Log(t *testing.T) {
	auditor := NewAuditor(true)

	event := AuditEvent{
		EventType: EventTypeCreate,
		Actor: ActorInfo{
			UserID:   123,
			Username: "testuser",
			Role:     "admin",
		},
		Resource: ResourceInfo{
			Type: "channel",
			ID:   "456",
			Name: "test-channel",
		},
		Action:  "channel.created",
		Result:  "success",
		Details: map[string]any{
			"test_key": "test_value",
		},
	}

	// Should not panic
	auditor.Log(context.Background(), event)
}

func TestAuditor_Disabled(t *testing.T) {
	auditor := NewAuditor(false)

	event := AuditEvent{
		EventType: EventTypeCreate,
		Actor: ActorInfo{
			UserID: 123,
		},
		Resource: ResourceInfo{
			Type: "channel",
		},
		Action: "test",
		Result: "success",
	}

	// Should not panic even when disabled
	auditor.Log(context.Background(), event)
}

func TestAuditor_LogSuccess(t *testing.T) {
	auditor := NewAuditor(true)

	actor := ActorInfo{
		UserID:   123,
		Username: "testuser",
	}
	resource := ResourceInfo{
		Type: "channel",
		ID:   "456",
	}

	// Should not panic
	auditor.LogSuccess(context.Background(), EventTypeCreate, actor, resource, "channel.created")
}

func TestAuditor_LogFailure(t *testing.T) {
	auditor := NewAuditor(true)

	actor := ActorInfo{
		UserID:   123,
		Username: "testuser",
	}
	resource := ResourceInfo{
		Type: "channel",
		ID:   "456",
	}

	// Should not panic
	auditor.LogFailure(context.Background(), EventTypeCreate, actor, resource, "channel.created", &testError{msg: "test error"})
}

func TestAuditor_UserLogin(t *testing.T) {
	auditor := NewAuditor(true)

	// Test successful login
	auditor.LogUserLogin(context.Background(), 123, "testuser", "127.0.0.1", true)

	// Test failed login
	auditor.LogUserLogin(context.Background(), 123, "testuser", "127.0.0.1", false)
}

func TestAuditor_UserLogout(t *testing.T) {
	auditor := NewAuditor(true)
	auditor.LogUserLogout(context.Background(), 123, "testuser")
}

func TestAuditor_ChannelEvents(t *testing.T) {
	auditor := NewAuditor(true)

	auditor.LogChannelCreated(context.Background(), 123, 456, "test-channel")
	auditor.LogChannelUpdated(context.Background(), 123, 456, "test-channel")
	auditor.LogChannelDeleted(context.Background(), 123, 456, "test-channel")
}

func TestAuditor_Payment(t *testing.T) {
	auditor := NewAuditor(true)

	auditor.LogPaymentProcessed(context.Background(), 123, "order-123", 99.99, true)
	auditor.LogPaymentProcessed(context.Background(), 123, "order-124", 199.99, false)
}

func TestAuditor_Config(t *testing.T) {
	auditor := NewAuditor(true)
	auditor.LogConfigChanged(context.Background(), 123, "test.key", "old", "new")
}

func TestAuditor_Permission(t *testing.T) {
	auditor := NewAuditor(true)
	auditor.LogPermissionChanged(context.Background(), 123, 456, "admin", "grant")
}

func TestMiddleware(t *testing.T) {
	auditor := NewAuditor(true)
	middleware := NewMiddleware(auditor)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	wrapped := middleware.Handler(handler)

	// Test normal request
	req := httptest.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", rec.Code)
	}

	// Test excluded path
	req = httptest.NewRequest("GET", "/health", nil)
	rec = httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %d", rec.Code)
	}
}

func TestMapMethodToEventType(t *testing.T) {
	tests := []struct {
		method    string
		eventType EventType
	}{
		{"GET", EventTypeRead},
		{"POST", EventTypeCreate},
		{"PUT", EventTypeUpdate},
		{"PATCH", EventTypeUpdate},
		{"DELETE", EventTypeDelete},
		{"UNKNOWN", EventType("unknown")},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			if got := mapMethodToEventType(tt.method); got != tt.eventType {
				t.Errorf("mapMethodToEventType(%s) = %v, want %v", tt.method, got, tt.eventType)
			}
		})
	}
}

func TestExtractIP(t *testing.T) {
	tests := []struct {
		name       string
		headerXFF  string
		headerXRI  string
		remoteAddr string
		want       string
	}{
		{
			name:       "X-Forwarded-For takes precedence",
			headerXFF:  "1.2.3.4",
			headerXRI:  "5.6.7.8",
			remoteAddr: "9.10.11.12",
			want:       "1.2.3.4",
		},
		{
			name:       "X-Real-IP second priority",
			headerXFF:  "",
			headerXRI:  "5.6.7.8",
			remoteAddr: "9.10.11.12",
			want:       "5.6.7.8",
		},
		{
			name:       "RemoteAddr fallback",
			headerXFF:  "",
			headerXRI:  "",
			remoteAddr: "9.10.11.12",
			want:       "9.10.11.12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tt.headerXFF != "" {
				req.Header.Set("X-Forwarded-For", tt.headerXFF)
			}
			if tt.headerXRI != "" {
				req.Header.Set("X-Real-IP", tt.headerXRI)
			}
			req.RemoteAddr = tt.remoteAddr

			if got := extractIP(req); got != tt.want {
				t.Errorf("extractIP() = %v, want %v", got, tt.want)
			}
		})
	}
}

// testError is a simple error implementation for testing.
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
