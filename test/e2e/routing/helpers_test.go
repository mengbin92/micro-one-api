package routingtest

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/pkg/jsonx"
)

type object = map[string]any
type fixture struct {
	UserID             int64             `json:"user_id"`
	Session            string            `json:"session"`
	Legacy             string            `json:"legacy"`
	LegacySubscription int64             `json:"legacy_subscription"`
	LegacyPlan         int64             `json:"legacy_plan"`
	Fixed              string            `json:"fixed"`
	FixedID            int64             `json:"fixed_id"`
	Ordered            string            `json:"ordered"`
	Groups             map[string]int64  `json:"groups"`
	Requests           map[string]string `json:"requests"`
}
type suite struct {
	t            *testing.T
	db           *sql.DB
	state        *fixture
	admin, relay string
}

func newSuite(t *testing.T) *suite {
	db, err := sql.Open(os.Getenv("DATABASE_DRIVER"), os.Getenv("DATABASE_DSN"))
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(2)
	require.Eventually(t, func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		return db.PingContext(ctx) == nil
	}, 90*time.Second, time.Second, "database readiness")
	s := &suite{t: t, db: db, state: &fixture{Groups: map[string]int64{}}, admin: os.Getenv("ADMIN_HTTP_BASE"), relay: os.Getenv("RELAY_HTTP_BASE")}
	if raw, err := os.ReadFile(os.Getenv("ROUTING_STATE")); err == nil {
		require.NoError(t, jsonx.Unmarshal(raw, s.state))
	}
	if s.state.Requests == nil {
		s.state.Requests = map[string]string{}
	}
	require.Eventually(t, func() bool {
		status, _, err := send("GET", s.relay+"/healthz", "", nil, "")
		return err == nil && status == 200
	}, 90*time.Second, time.Second, "relay readiness")
	// Fault phases reuse the established session: login itself requires Redis.
	if os.Getenv("ROUTING_PHASE") != "redis-down" {
		require.Eventually(t, func() bool {
			status, body, err := send("POST", s.admin+"/api/user/login", "", object{"username": "admin", "password": os.Getenv("INITIAL_ADMIN_PASSWORD")}, "")
			return err == nil && status == 200 && bytes.Contains(body, []byte(`"success":true`))
		}, 90*time.Second, time.Second, "admin and identity readiness")
	}
	// A capability outage must be tested against a reachable service, not a
	// socket that is still restarting after Compose changed its gate.
	ctx, conn := s.conn("BILLING_GRPC_ENDPOINT")
	version := int32(2)
	if phase := os.Getenv("ROUTING_PHASE"); phase == "legacy" || phase == "missing-capability" {
		version = 0
	}
	require.Eventually(t, func() bool {
		reply, err := billingv1.NewBillingServiceClient(conn).GetRoutingCapabilities(ctx, &billingv1.GetRoutingCapabilitiesRequest{})
		return err == nil && reply.RequestSnapshotVersion == version
	}, 10*time.Second, 100*time.Millisecond, "billing capability readiness")
	return s
}
func (s *suite) save() {
	raw, err := jsonx.Marshal(s.state)
	require.NoError(s.t, err)
	require.NoError(s.t, os.WriteFile(os.Getenv("ROUTING_STATE"), raw, 0600))
}
func send(method, url, token string, body any, id string) (int, []byte, error) {
	var raw []byte
	var err error
	if body != nil {
		raw, err = jsonx.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if id != "" {
		r.Header.Set("X-Request-ID", id)
		r.Header.Set("Idempotency-Key", id)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, raw, err
}
func (s *suite) api(method, path, token string, body any, id string) object {
	s.t.Helper()
	code, raw, err := send(method, s.admin+path, token, body, id)
	require.NoError(s.t, err)
	var out object
	require.NoError(s.t, jsonx.Unmarshal(raw, &out), "%s %s status=%d", method, path, code)
	require.Contains(s.t, []int{200, 201}, code, "%s %s: %v", method, path, out["message"])
	require.NotEqual(s.t, false, out["success"], "%s %s: %v", method, path, out["message"])
	if data, ok := out["data"].(map[string]any); ok {
		return data
	}
	return out
}
func (s *suite) adminAPI(method, path string, body any) object {
	return s.api(method, path, os.Getenv("ADMIN_TOKEN"), body, "")
}
func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	default:
		panic(fmt.Sprintf("not a number: %T", v))
	}
}
func (s *suite) scalar(q string, args ...any) int64 {
	s.t.Helper()
	var n int64
	require.NoError(s.t, s.db.QueryRow(q, args...).Scan(&n))
	return n
}
func (s *suite) balance() int64 {
	return s.scalar("SELECT balance FROM users WHERE id = ?", s.state.UserID)
}
func (s *suite) calls() int64 {
	_, raw, err := send("GET", "http://mock-upstream:9999/health", "", nil, "")
	require.NoError(s.t, err)
	var out object
	require.NoError(s.t, jsonx.Unmarshal(raw, &out))
	return num(out["calls"])
}
func (s *suite) conn(endpoint string) (context.Context, *grpc.ClientConn) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	s.t.Cleanup(cancel)
	ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+os.Getenv("SERVICE_TOKEN"))
	c, err := grpc.NewClient(os.Getenv(endpoint), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(s.t, err)
	s.t.Cleanup(func() { c.Close() })
	return ctx, c
}
func (s *suite) chat(token, id string, stream bool) []byte {
	s.t.Helper()
	after := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
	status, raw, err := send("POST", s.relay+"/v1/chat/completions", token, object{"model": "gpt-3.5-turbo", "messages": []any{object{"role": "user", "content": id}}, "max_tokens": 32, "stream": stream}, id)
	require.NoError(s.t, err)
	require.Equal(s.t, 200, status, "relay: %s", raw)
	if stream {
		require.Contains(s.t, string(raw), "[DONE]")
	} else {
		require.Contains(s.t, string(raw), "routing e2e")
	}
	if s.state.Requests[id] == "" {
		s.trackRequest(id, after)
	}
	return raw
}

// Relay creates its own billing attempt ID. The fixture is sequential and links
// each case to its new reservation; X-Request-ID is only a tracing header.
func (s *suite) trackRequest(id string, after int64) {
	s.t.Helper()
	var requestID string
	require.Eventually(s.t, func() bool {
		return s.db.QueryRow("SELECT request_id FROM billing_reservations WHERE user_id = ? AND id > ? ORDER BY id LIMIT 1", fmt.Sprint(s.state.UserID), after).Scan(&requestID) == nil
	}, 5*time.Second, 20*time.Millisecond, "reservation for %s", id)
	s.state.Requests[id] = requestID
}
func (s *suite) settled(id string) object {
	s.t.Helper()
	var status string
	var raw sql.NullString
	require.Eventually(s.t, func() bool {
		return s.db.QueryRow("SELECT status,request_snapshot FROM billing_reservations WHERE request_id = ? ORDER BY id DESC LIMIT 1", s.state.Requests[id]).Scan(&status, &raw) == nil && status == "committed"
	}, 15*time.Second, 100*time.Millisecond, "settlement %s", id)
	var out object
	if raw.Valid && raw.String != "" {
		require.NoError(s.t, jsonx.Unmarshal([]byte(raw.String), &out))
	}
	return out
}
func (s *suite) reject(token, id string) {
	s.t.Helper()
	before, calls := s.balance(), s.calls()
	status, _, err := send("POST", s.relay+"/v1/chat/completions", token, object{"model": "gpt-3.5-turbo", "messages": []any{object{"role": "user", "content": "deny"}}, "max_tokens": 32}, id)
	require.NoError(s.t, err)
	require.GreaterOrEqual(s.t, status, 400)
	require.Less(s.t, status, 600)
	require.Equal(s.t, calls, s.calls(), "rejected admission must not call upstream")
	require.Equal(s.t, before, s.balance(), "rejected admission must not charge wallet")
}
func (s *suite) group(name, mode string, resources bool) int64 {
	s.t.Helper()
	g := s.adminAPI("POST", "/api/v1/admin/routing-groups", object{"key": name, "display_name": name, "access_mode": "restricted"})["group"].(map[string]any)
	require.Equal(s.t, "disabled", g["status"])
	id := num(g["id"])
	s.state.Groups[name] = id
	if resources {
		s.adminAPI("POST", "/api/channel", object{"name": "upstream-" + name, "type": 1, "base_url": "http://mock-upstream:9999", "key": "sk-routing-fixture", "models": "gpt-3.5-turbo,text-embedding-3-small", "group": name, "priority": 1, "weight": 1})
	}
	s.adminAPI("PATCH", fmt.Sprintf("/api/v1/admin/routing-groups/%d/billing", id), object{"version": 0, "billing_mode": mode, "price_ratio": 1})
	s.stateGroup(id, "enabled")
	return id
}
func (s *suite) stateGroup(id int64, status string) {
	g := s.adminAPI("GET", fmt.Sprintf("/api/v1/admin/routing-groups/%d", id), nil)["group"].(map[string]any)
	s.adminAPI("PATCH", fmt.Sprintf("/api/v1/admin/routing-groups/%d", id), object{"expected_revision": g["revision"], "status": status, "access_mode": "restricted"})
}
func (s *suite) access(id int64, op, source string) {
	path := fmt.Sprintf("/api/v1/admin/routing-access/%d", s.state.UserID)
	facts := s.adminAPI("GET", path, nil)
	s.adminAPI("PATCH", path, object{"expected_revision": facts["revision"], "operation": op, "routing_group_id": id, "source_type": "admin", "source_ref": source})
}
func (s *suite) token(mode string, group int64, groups []int64) object {
	return s.api("POST", "/api/v1/routing-tokens", s.state.Session, object{"name": "e2e-" + mode, "routing_mode": mode, "routing_group_id": group, "routing_group_ids": groups}, "")
}
