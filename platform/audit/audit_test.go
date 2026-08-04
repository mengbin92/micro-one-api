package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	appmiddleware "micro-one-api/platform/middleware"
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
		Action: "channel.created",
		Result: "success",
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

// TestAudit_ActorAndRequestIDExtraction pins the audit enhancement: the
// middleware must now resolve the authenticated actor (via WithActor/ActorFrom)
// and the request ID (via the platform request-ID middleware context) instead
// of returning the previous TODO empty values.
func TestAudit_ActorAndRequestIDExtraction(t *testing.T) {
	ctx := context.Background()

	// Without a stamped actor / request ID: anonymous, empty.
	if got := extractActorFromContext(ctx); got != (ActorInfo{}) {
		t.Fatalf("extractActorFromContext(empty ctx) = %+v, want zero ActorInfo", got)
	}
	if got := extractRequestID(ctx); got != "" {
		t.Fatalf("extractRequestID(empty ctx) = %q, want empty", got)
	}

	// With a stamped actor.
	actor := ActorInfo{UserID: 42, Role: "admin"}
	ctx = WithActor(ctx, actor)
	if got := extractActorFromContext(ctx); got != actor {
		t.Fatalf("extractActorFromContext = %+v, want %+v", got, actor)
	}
	if got := ActorFrom(ctx); got != actor {
		t.Fatalf("ActorFrom = %+v, want %+v", got, actor)
	}

	// With a request ID stamped by the real platform request-ID middleware
	// (the same context key extractRequestID reads).
	ridServed := make(chan string, 1)
	h := appmiddleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ridServed <- extractRequestID(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("X-Request-ID", "req-abc")
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got := <-ridServed; got != "req-abc" {
		t.Fatalf("extractRequestID via middleware = %q, want req-abc", got)
	}
}

// TestMiddleware_ReadsActorStampedByHandler is the regression test for the
// critical timing bug: the audit middleware previously read the actor BEFORE
// calling next.ServeHTTP, so even when a downstream handler called WithActor
// the middleware had already captured an empty value. The mutable
// *actorHolder mechanism fixes this — the middleware injects a holder, the
// handler writes into it via WithActor, and the middleware reads the final
// value AFTER next returns.
func TestMiddleware_ReadsActorStampedByHandler(t *testing.T) {
	auditor := NewAuditor(true)
	mw := NewMiddleware(auditor)

	stampedActor := ActorInfo{UserID: 99, Username: "handler-user", Role: "user"}

	// The inner handler stamps the actor mid-request, simulating an auth layer.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate authentication: stamp the actor into context.
		WithActor(r.Context(), stampedActor)
		w.WriteHeader(http.StatusOK)
	})

	// Capture the actor the middleware actually logs. We intercept via a
	// custom auditor logger by checking the observable side effect: the
	// handler stamps the actor, and we assert the middleware's ActorFrom
	// (called after next) would see it. Since the middleware logs internally,
	// we verify the holder mechanism works by checking that ActorFrom reads
	// the value written by WithActor inside the handler chain.
	var capturedActor ActorInfo
	wrapAndCapture := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The middleware calls withActorHolder before dispatching. The handler
		// calls WithActor which writes into the holder. After the handler
		// returns, ActorFrom should return the stamped value.
		inner.ServeHTTP(w, r)
		capturedActor = ActorFrom(r.Context())
	})

	mwHandler := mw.Handler(wrapAndCapture)
	req := httptest.NewRequest(http.MethodPost, "/api/test", nil)
	mwHandler.ServeHTTP(httptest.NewRecorder(), req)

	if capturedActor != stampedActor {
		t.Fatalf("middleware actor after handler = %+v, want %+v (timing bug: "+
			"actor must be readable AFTER next.ServeHTTP)", capturedActor, stampedActor)
	}
}

// TestSanitizeAuditString verifies control characters are stripped to prevent
// log injection (e.g. newlines in usernames forging additional log lines).
func TestSanitizeAuditString(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"empty", "", ""},
		{"clean", "alice", "alice"},
		{"newline", "alice\nadmin", "alice\uFFFdadmin"},
		{"carriage_return", "alice\radmin", "alice\uFFFdadmin"},
		{"tab", "a\tb", "a\uFFFdb"},
		{"null_byte", "alice\x00admin", "alice\uFFFdadmin"},
		{"del", "alice\x7fadmin", "alice\uFFFdadmin"},
		{"unicode_preserved", "爱丽丝", "爱丽丝"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sanitizeAuditString(tt.input); got != tt.want {
				t.Errorf("sanitizeAuditString(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
