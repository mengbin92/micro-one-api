package routingtest

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"micro-one-api/pkg/jsonx"
)

func (s *suite) sessions() {
	group := s.state.Groups["p0-wallet"]
	for i, key := range []string{s.state.Fixed, s.state.Ordered} {
		prefix := fmt.Sprintf("routing-session-%d", i)
		responseID := s.response(key, prefix, "", false)
		continued := s.response(key, prefix+"-sse", responseID, true)
		calls := s.calls()
		s.access(group, "revoke", "fixture")
		status, _, err := send("POST", s.relay+"/v1/responses", key, object{"model": "gpt-3.5-turbo", "input": "continue", "previous_response_id": continued, "max_output_tokens": 32}, "")
		require.NoError(s.t, err)
		require.GreaterOrEqual(s.t, status, 400)
		require.Equal(s.t, calls, s.calls())
		s.access(group, "grant", "fixture")
		s.stateGroup(group, "disabled")
		status, _, err = send("POST", s.relay+"/v1/responses", key, object{"model": "gpt-3.5-turbo", "input": "continue", "previous_response_id": continued, "max_output_tokens": 32}, "")
		require.NoError(s.t, err)
		require.GreaterOrEqual(s.t, status, 400)
		require.Equal(s.t, calls, s.calls())
		s.stateGroup(group, "enabled")
		s.websocketSession(key, prefix)
	}
}

func (s *suite) response(key, id, previous string, stream bool) string {
	s.t.Helper()
	after := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
	body := object{"model": "gpt-3.5-turbo", "input": "routing session", "max_output_tokens": 32, "stream": stream}
	if previous != "" {
		body["previous_response_id"] = previous
	}
	status, raw, err := send("POST", s.relay+"/v1/responses", key, body, id)
	require.NoError(s.t, err)
	require.Equal(s.t, 200, status, "responses: %s", raw)
	var response object
	if stream {
		for _, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(line, "data: ") {
				var event object
				if jsonx.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) == nil && event["type"] == "response.completed" {
					response, _ = event["response"].(map[string]any)
				}
			}
		}
	} else {
		require.NoError(s.t, jsonx.Unmarshal(raw, &response))
	}
	require.NotEmpty(s.t, response["id"], "completed response required")
	s.trackRequest(id, after)
	require.Equal(s.t, "p0-wallet", s.settled(id)["GroupKey"])
	return response["id"].(string)
}

func (s *suite) websocketSession(key, prefix string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, strings.Replace(s.relay, "http://", "ws://", 1)+"/v1/responses", &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + key}}})
	require.NoError(s.t, err)
	defer c.CloseNow()
	body := object{"type": "response.create", "model": "gpt-3.5-turbo", "input": "websocket turn", "max_output_tokens": 32}
	for turn := 0; turn < 2; turn++ {
		after := s.scalar("SELECT COALESCE(MAX(id),0) FROM billing_reservations")
		payload, _ := jsonx.Marshal(body)
		require.NoError(s.t, c.Write(ctx, websocket.MessageText, payload))
		completed := false
		for i := 0; i < 20; i++ {
			_, raw, err := c.Read(ctx)
			require.NoError(s.t, err)
			var event object
			require.NoError(s.t, jsonx.Unmarshal(raw, &event))
			if event["type"] == "response.completed" {
				body["previous_response_id"] = event["response"].(map[string]any)["id"]
				completed = true
				break
			}
		}
		require.True(s.t, completed, "WS turn completes")
		id := fmt.Sprintf("%s-ws-%d", prefix, turn)
		s.trackRequest(id, after)
		require.Equal(s.t, "p0-wallet", s.settled(id)["GroupKey"])
	}
	calls := s.calls()
	s.access(s.state.Groups["p0-wallet"], "revoke", "fixture")
	payload, _ := jsonx.Marshal(body)
	if err := c.Write(ctx, websocket.MessageText, payload); err == nil {
		_, raw, err := c.Read(ctx)
		if err == nil {
			require.Contains(s.t, string(raw), "error")
		}
	}
	require.Equal(s.t, calls, s.calls(), "revoked WS turn must not reach upstream")
	s.access(s.state.Groups["p0-wallet"], "grant", "fixture")
}
