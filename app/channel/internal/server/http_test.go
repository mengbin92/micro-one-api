package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"micro-one-api/app/channel/internal/biz"
)

func TestAuthorizeAdmin_FailClosedWhenTokenUnset(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/channels/selector/stats", nil)
	req.Header.Set("Authorization", "Bearer anything")
	if authorizeAdmin(req) {
		t.Fatal("authorizeAdmin should fail-closed when ADMIN_TOKEN is unset")
	}
}

func TestAuthorizeAdmin_TokenCompare(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "secret-admin")
	for _, tc := range []struct {
		name string
		auth string
		want bool
	}{
		{"matching bearer", "Bearer secret-admin", true},
		{"wrong bearer", "Bearer nope", false},
		{"missing header", "", false},
		{"non-bearer", "secret-admin", false},
		{"empty token", "Bearer ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.auth != "" {
				req.Header.Set("Authorization", tc.auth)
			}
			if got := authorizeAdmin(req); got != tc.want {
				t.Fatalf("authorizeAdmin=%v want %v", got, tc.want)
			}
		})
	}
}

func TestSelectorStatsPayloadShape(t *testing.T) {
	stats := map[int64]biz.ChannelStats{
		1: {ChannelID: 1, Weight: 100, CurrentWeight: -7, Inflight: 2},
	}
	payload := selectorStatsPayload(stats)
	channels, ok := payload["channels"].(map[int64]biz.ChannelStats)
	if !ok {
		t.Fatalf("payload[\"channels\"] type=%T", payload["channels"])
	}
	if got := channels[1].Weight; got != 100 {
		t.Fatalf("channels[1].Weight=%d want 100", got)
	}
}

// TestRegisterSelectorStatsRoute_AuthAndPayload exercises the wired route
// handler shape against an in-process ChannelUsecase: unauthenticated requests
// are rejected, wrong methods are rejected, and authenticated GET returns the
// selector stats snapshot under the documented JSON shape.
func TestRegisterSelectorStatsRoute_AuthAndPayload(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "root-token")

	uc := biz.NewChannelUsecase(nil, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/admin/channels/selector/stats", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		if !authorizeAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin credentials"})
			return
		}
		if uc == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "channel usecase not wired"})
			return
		}
		writeJSON(w, http.StatusOK, selectorStatsPayload(uc.SelectorStats()))
	})

	t.Run("rejects unauthenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/channels/selector/stats", nil)
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status=%d want 401", rr.Code)
		}
	})

	t.Run("rejects wrong method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/channels/selector/stats", nil)
		req.Header.Set("Authorization", "Bearer root-token")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status=%d want 405", rr.Code)
		}
	})

	t.Run("returns selector stats shape when authenticated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/channels/selector/stats", nil)
		req.Header.Set("Authorization", "Bearer root-token")
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status=%d want 200 body=%s", rr.Code, rr.Body.String())
		}
		var resp struct {
			Channels map[int64]biz.ChannelStats `json:"channels"`
		}
		if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if resp.Channels == nil {
			t.Fatalf("channels map is nil; want non-nil empty map for freshly-wired selector")
		}
	})
}
