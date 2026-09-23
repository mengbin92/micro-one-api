package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStreamKeepaliveHonorsTotalBudgetAndCallerDeadline(t *testing.T) {
	for _, callerEarlier := range []bool{false, true} {
		t.Run(map[bool]string{false: "total", true: "caller"}[callerEarlier], func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						_, _ = io.WriteString(w, ": keepalive\n\n")
						w.(http.Flusher).Flush()
					}
				}
			}))
			defer upstream.Close()
			parent := context.Background()
			var parentCancel context.CancelFunc
			if callerEarlier {
				parent, parentCancel = context.WithTimeout(parent, 100*time.Millisecond)
				defer parentCancel()
			}
			ctx, cancel := context.WithTimeoutCause(parent, 200*time.Millisecond, ErrRequestBudget)
			defer cancel()
			client := StreamHTTPClientWithTimeouts(upstream.Client(), StreamTimeouts{Header: time.Second, Idle: 60 * time.Millisecond})
			req, _ := http.NewRequestWithContext(ctx, "GET", upstream.URL, nil)
			started := time.Now()
			resp, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			want := ErrRequestBudget
			if callerEarlier {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || len(data) == 0 {
				t.Fatalf("body=%q error=%v want=%v", data, err, want)
			}
			if elapsed := time.Since(started); elapsed < 80*time.Millisecond || elapsed > time.Second {
				t.Fatalf("unexpected duration %s", elapsed)
			}
		})
	}
}
func TestStreamCancellationClosesUpstream(t *testing.T) {
	done := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(done)
	}))
	defer upstream.Close()
	ctx, cancel := context.WithCancel(context.Background())
	client := StreamHTTPClientWithTimeouts(upstream.Client(), StreamTimeouts{Header: time.Second, Idle: time.Second})
	req, _ := http.NewRequestWithContext(ctx, "GET", upstream.URL, nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_, err = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("upstream leaked after cancellation")
	}
}
func TestTimeoutConfigurationRejectsInvalidValues(t *testing.T) {
	for _, name := range []string{"RELAY_STREAM_HEADER_TIMEOUT", "RELAY_STREAM_IDLE_TIMEOUT", "RELAY_STREAM_TOTAL_TIMEOUT", "RELAY_REQUEST_TIMEOUT", "RELAY_CONNECT_TIMEOUT"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "-1s")
			if ValidateTimeoutEnvironment() == nil {
				t.Fatal("accepted negative duration")
			}
		})
	}
}
