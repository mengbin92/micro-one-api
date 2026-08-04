// Package audit provides audit logging for sensitive operations.
package audit

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
	applogger "micro-one-api/platform/logging"
	appmiddleware "micro-one-api/platform/middleware"
)

// EventType represents the type of audit event.
type EventType string

const (
	// EventTypeCreate represents resource creation events.
	EventTypeCreate EventType = "create"
	// EventTypeRead represents resource read events.
	EventTypeRead EventType = "read"
	// EventTypeUpdate represents resource update events.
	EventTypeUpdate EventType = "update"
	// EventTypeDelete represents resource deletion events.
	EventTypeDelete EventType = "delete"
	// EventTypeLogin represents login/authentication events.
	EventTypeLogin EventType = "login"
	// EventTypeLogout represents logout events.
	EventTypeLogout EventType = "logout"
	// EventTypePayment represents payment/financial events.
	EventTypePayment EventType = "payment"
	// EventTypeConfig represents configuration change events.
	EventTypeConfig EventType = "config"
	// EventTypePermission represents permission change events.
	EventTypePermission EventType = "permission"
)

// AuditEvent represents an audit log entry.
type AuditEvent struct {
	// Timestamp when the event occurred.
	Timestamp time.Time `json:"timestamp"`
	// EventType is the type of event.
	EventType EventType `json:"event_type"`
	// Actor is the user or system that performed the action.
	Actor ActorInfo `json:"actor"`
	// Resource being acted upon.
	Resource ResourceInfo `json:"resource"`
	// Action performed.
	Action string `json:"action"`
	// Result of the action (success/failure).
	Result string `json:"result"`
	// Details about the event.
	Details map[string]any `json:"details,omitempty"`
	// RequestID for tracing.
	RequestID string `json:"request_id,omitempty"`
	// IP address of the actor.
	IPAddress string `json:"ip_address,omitempty"`
	// User agent of the actor.
	UserAgent string `json:"user_agent,omitempty"`
	// Error message if the action failed.
	Error string `json:"error,omitempty"`
}

// ActorInfo represents information about the actor.
type ActorInfo struct {
	// ID of the user.
	UserID int64 `json:"user_id,omitempty"`
	// Username if available.
	Username string `json:"username,omitempty"`
	// Role of the user.
	Role string `json:"role,omitempty"`
	// Service name if the actor is a service.
	ServiceName string `json:"service_name,omitempty"`
	// Session ID.
	SessionID string `json:"session_id,omitempty"`
}

// ResourceInfo represents information about the resource.
type ResourceInfo struct {
	// Type of resource (user, channel, payment, etc.).
	Type string `json:"type"`
	// ID of the resource.
	ID string `json:"id,omitempty"`
	// Name of the resource (if applicable).
	Name string `json:"name,omitempty"`
}

// Auditor records audit events.
//
// The logger is resolved on EACH event rather than captured at construction
// time (platform-L9): NewAuditor is typically called during wire/DI setup,
// BEFORE applogger.Initialize has swapped the nop logger for the real one.
// Capturing applogger.Log.Named("audit") then pinned the nop logger forever
// and silently dropped every audit event.
type Auditor struct {
	enabled bool
}

// NewAuditor creates a new auditor.
func NewAuditor(enabled bool) *Auditor {
	return &Auditor{enabled: enabled}
}

// logger resolves the current audit sub-logger at call time (platform-L9).
func (a *Auditor) logger() *zap.Logger {
	return applogger.Current().Named("audit")
}

// Log records an audit event.
func (a *Auditor) Log(ctx context.Context, event AuditEvent) {
	if !a.enabled {
		return
	}

	// Set timestamp if not provided
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Build log fields
	fields := []zap.Field{
		zap.String("event_type", string(event.EventType)),
		zap.String("action", event.Action),
		zap.String("result", event.Result),
		zap.Time("timestamp", event.Timestamp),
	}

	// Actor fields
	if event.Actor.UserID != 0 {
		fields = append(fields, zap.Int64("user_id", event.Actor.UserID))
	}
	if event.Actor.Username != "" {
		fields = append(fields, zap.String("username", event.Actor.Username))
	}
	if event.Actor.Role != "" {
		fields = append(fields, zap.String("role", event.Actor.Role))
	}
	if event.Actor.ServiceName != "" {
		fields = append(fields, zap.String("service", event.Actor.ServiceName))
	}

	// Resource fields
	fields = append(fields,
		zap.String("resource_type", event.Resource.Type),
	)
	if event.Resource.ID != "" {
		fields = append(fields, zap.String("resource_id", event.Resource.ID))
	}
	if event.Resource.Name != "" {
		fields = append(fields, zap.String("resource_name", event.Resource.Name))
	}

	// Context fields
	if event.RequestID != "" {
		fields = append(fields, zap.String("request_id", event.RequestID))
	}
	if event.IPAddress != "" {
		fields = append(fields, zap.String("ip_address", event.IPAddress))
	}
	if event.UserAgent != "" {
		fields = append(fields, zap.String("user_agent", event.UserAgent))
	}

	// Error field
	if event.Error != "" {
		fields = append(fields, zap.String("error", event.Error))
	}

	// Details
	for k, v := range event.Details {
		fields = append(fields, zap.Any(k, v))
	}

	a.logger().Info("audit event", fields...)
}

// LogSuccess logs a successful audit event.
func (a *Auditor) LogSuccess(ctx context.Context, eventType EventType, actor ActorInfo, resource ResourceInfo, action string) {
	a.Log(ctx, AuditEvent{
		EventType: eventType,
		Actor:     actor,
		Resource:  resource,
		Action:    action,
		Result:    "success",
	})
}

// LogFailure logs a failed audit event.
func (a *Auditor) LogFailure(ctx context.Context, eventType EventType, actor ActorInfo, resource ResourceInfo, action string, err error) {
	a.Log(ctx, AuditEvent{
		EventType: eventType,
		Actor:     actor,
		Resource:  resource,
		Action:    action,
		Result:    "failure",
		Error:     err.Error(),
	})
}

// Middleware provides HTTP middleware for audit logging.
type Middleware struct {
	auditor      *Auditor
	excludePaths map[string]bool
}

// NewMiddleware creates a new audit middleware.
func NewMiddleware(auditor *Auditor) *Middleware {
	return &Middleware{
		auditor: auditor,
		excludePaths: map[string]bool{
			"/health":      true,
			"/metrics":     true,
			"/favicon.ico": true,
		},
	}
}

// Handler returns the middleware handler.
func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip excluded paths
		if m.excludePaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		// Inject a mutable *actorHolder into the request context. Authentication
		// layers downstream call WithActor, which writes into this holder. We
		// read it AFTER next.ServeHTTP returns so the final authenticated actor
		// is captured even though Go's *http.Request is immutable (the handler
		// cannot mutate the original r that the middleware holds).
		ctx, holder := withActorHolder(r.Context())
		r = r.WithContext(ctx)

		// Request ID is immutable and already in context; read it now.
		requestID := extractRequestID(r.Context())

		// Create a response writer wrapper to capture status
		wrapped := &auditResponseWriter{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		// Call next handler
		next.ServeHTTP(wrapped, r)

		// Read the actor AFTER the handler chain has run, so it reflects
		// whoever the handler authenticated.
		actor := holder.actor

		// Log the request
		result := "success"
		if wrapped.statusCode >= 400 {
			result = "failure"
		}

		m.auditor.Log(ctx, AuditEvent{
			EventType: mapMethodToEventType(r.Method),
			Actor:     actor,
			Resource:  ResourceInfo{Type: "http"},
			Action:    fmt.Sprintf("%s %s", r.Method, r.URL.Path),
			Result:    result,
			RequestID: requestID,
			IPAddress: extractIP(r),
			UserAgent: r.UserAgent(),
			Details: map[string]any{
				"path":   r.URL.Path,
				"query":  r.URL.RawQuery,
				"status": wrapped.statusCode,
			},
		})
	})
}

// auditResponseWriter wraps http.ResponseWriter to capture status code.
type auditResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

// WriteHeader captures the status code.
func (w *auditResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}

// Flush implements http.Flusher so SSE/streaming handlers proxied through the
// audit middleware can flush incremental data to the client (platform-H2:
// without this, the handler's w.(http.Flusher) assertion fails and every
// streaming chat request breaks when audit is enabled).
func (w *auditResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter so that http.Hijacker and other
// optional interfaces remain accessible through the wrapper (platform-H2).
func (w *auditResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

// Helper functions

func mapMethodToEventType(method string) EventType {
	switch method {
	case http.MethodGet:
		return EventTypeRead
	case http.MethodPost:
		return EventTypeCreate
	case http.MethodPut, http.MethodPatch:
		return EventTypeUpdate
	case http.MethodDelete:
		return EventTypeDelete
	default:
		return EventType("unknown")
	}
}

// actorHolder is a mutable container that lets authentication layers stamp
// the verified actor DURING request processing (inside the handler chain),
// while the audit middleware reads it AFTER next.ServeHTTP returns. A plain
// context value cannot serve this purpose because Go's *http.Request is
// immutable: a handler calls r.WithContext(...) which creates a new request
// the middleware never sees. By storing a *actorHolder pointer in context,
// WithActor mutates the shared holder in place and the middleware observes
// the final value after the handler chain completes.
type actorHolder struct {
	actor ActorInfo
}

// actorHolderKey carries the *actorHolder created by the audit middleware.
// WithActor writes into it; ActorFrom reads from it. When no audit middleware
// is present (e.g. in tests), WithActor falls back to a plain context value
// so ActorFrom still works.
type actorHolderKey struct{}

// actorContextKey is the fallback immutable key used when no *actorHolder is
// present in context (tests, non-HTTP code paths).
type actorContextKey struct{}

// WithActor stamps the verified actor into context for audit consumption. It
// is the single place authentication layers write the audit identity. If the
// context carries an audit-middleware-provided *actorHolder (the normal HTTP
// path), the actor is written into the mutable holder so the middleware sees
// it after the handler returns. Otherwise a plain immutable context value is
// set as a fallback.
func WithActor(ctx context.Context, actor ActorInfo) context.Context {
	if ctx == nil {
		return ctx
	}
	if h, ok := ctx.Value(actorHolderKey{}).(*actorHolder); ok && h != nil {
		h.actor = actor
		return ctx
	}
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFrom reads the actor stamped by WithActor. Returns the zero ActorInfo
// when no authentication layer stamped one (anonymous request). It checks the
// mutable *actorHolder first (HTTP path), then falls back to the immutable
// context value.
func ActorFrom(ctx context.Context) ActorInfo {
	if ctx == nil {
		return ActorInfo{}
	}
	if h, ok := ctx.Value(actorHolderKey{}).(*actorHolder); ok && h != nil {
		return h.actor
	}
	actor, _ := ctx.Value(actorContextKey{}).(ActorInfo)
	return actor
}

// withActorHolder injects a fresh *actorHolder into ctx. Called by the audit
// middleware before dispatching to the handler chain.
func withActorHolder(ctx context.Context) (context.Context, *actorHolder) {
	h := &actorHolder{}
	return context.WithValue(ctx, actorHolderKey{}, h), h
}

func extractActorFromContext(ctx context.Context) ActorInfo {
	return ActorFrom(ctx)
}

func extractRequestID(ctx context.Context) string {
	return appmiddleware.GetRequestID(ctx)
}

// sanitizeAuditString removes control characters (newlines, carriage returns,
// tabs, null bytes) from a caller-supplied string before it is written to the
// audit log. This prevents log-injection / log-forgery attacks where an
// attacker embeds newlines in a username to forge additional log lines or
// break log parsers. Non-printable runes are replaced with the Unicode
// replacement character.
func sanitizeAuditString(s string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteRune('\uFFFD')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func extractIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// Common audit event builders

// LogUserLogin logs a user login event.
func (a *Auditor) LogUserLogin(ctx context.Context, userID int64, username, ipAddress string, success bool) {
	result := "success"
	if !success {
		result = "failure"
	}

	a.Log(ctx, AuditEvent{
		EventType: EventTypeLogin,
		Actor: ActorInfo{
			UserID:   userID,
			Username: sanitizeAuditString(username),
		},
		Resource:  ResourceInfo{Type: "auth"},
		Action:    "login",
		Result:    result,
		IPAddress: ipAddress,
	})
}

// LogUserLogout logs a user logout event.
func (a *Auditor) LogUserLogout(ctx context.Context, userID int64, username string) {
	a.Log(ctx, AuditEvent{
		EventType: EventTypeLogout,
		Actor: ActorInfo{
			UserID:   userID,
			Username: username,
		},
		Resource: ResourceInfo{Type: "auth"},
		Action:   "logout",
		Result:   "success",
	})
}

// LogChannelCreated logs a channel creation event.
func (a *Auditor) LogChannelCreated(ctx context.Context, userID int64, channelID int64, channelName string) {
	a.Log(ctx, AuditEvent{
		EventType: EventTypeCreate,
		Actor: ActorInfo{
			UserID: userID,
		},
		Resource: ResourceInfo{
			Type: "channel",
			ID:   fmt.Sprintf("%d", channelID),
			Name: channelName,
		},
		Action: "channel.created",
		Result: "success",
	})
}

// LogChannelUpdated logs a channel update event.
func (a *Auditor) LogChannelUpdated(ctx context.Context, userID int64, channelID int64, channelName string) {
	a.Log(ctx, AuditEvent{
		EventType: EventTypeUpdate,
		Actor: ActorInfo{
			UserID: userID,
		},
		Resource: ResourceInfo{
			Type: "channel",
			ID:   fmt.Sprintf("%d", channelID),
			Name: channelName,
		},
		Action: "channel.updated",
		Result: "success",
	})
}

// LogChannelDeleted logs a channel deletion event.
func (a *Auditor) LogChannelDeleted(ctx context.Context, userID int64, channelID int64, channelName string) {
	a.Log(ctx, AuditEvent{
		EventType: EventTypeDelete,
		Actor: ActorInfo{
			UserID: userID,
		},
		Resource: ResourceInfo{
			Type: "channel",
			ID:   fmt.Sprintf("%d", channelID),
			Name: channelName,
		},
		Action: "channel.deleted",
		Result: "success",
	})
}

// LogPaymentProcessed logs a payment processing event.
func (a *Auditor) LogPaymentProcessed(ctx context.Context, userID int64, orderID string, amount float64, success bool) {
	result := "success"
	if !success {
		result = "failure"
	}

	a.Log(ctx, AuditEvent{
		EventType: EventTypePayment,
		Actor: ActorInfo{
			UserID: userID,
		},
		Resource: ResourceInfo{
			Type: "payment",
			ID:   orderID,
		},
		Action: "payment.processed",
		Result: result,
		Details: map[string]any{
			"amount": amount,
		},
	})
}

// LogConfigChanged logs a configuration change event.
func (a *Auditor) LogConfigChanged(ctx context.Context, userID int64, configKey, oldValue, newValue string) {
	a.Log(ctx, AuditEvent{
		EventType: EventTypeConfig,
		Actor: ActorInfo{
			UserID: userID,
		},
		Resource: ResourceInfo{
			Type: "config",
		},
		Action: "config.changed",
		Result: "success",
		Details: map[string]any{
			"key":       configKey,
			"old_value": oldValue,
			"new_value": newValue,
		},
	})
}

// LogPermissionChanged logs a permission change event.
func (a *Auditor) LogPermissionChanged(ctx context.Context, actorUserID, targetUserID int64, permission, action string) {
	a.Log(ctx, AuditEvent{
		EventType: EventTypePermission,
		Actor: ActorInfo{
			UserID: actorUserID,
		},
		Resource: ResourceInfo{
			Type: "permission",
			ID:   fmt.Sprintf("%d", targetUserID),
		},
		Action: "permission.changed",
		Result: "success",
		Details: map[string]any{
			"target_user_id": targetUserID,
			"permission":     permission,
			"action":         action,
		},
	})
}
