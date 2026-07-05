package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserRPMLimitFromEnv(t *testing.T) {
	t.Setenv("SUBSCRIPTION_USER_RPM_LIMIT", "")
	if got := userRPMLimitFromEnv(); got != 3 {
		t.Fatalf("default user rpm limit = %d, want 3", got)
	}
	t.Setenv("SUBSCRIPTION_USER_RPM_LIMIT", "8")
	if got := userRPMLimitFromEnv(); got != 8 {
		t.Fatalf("configured user rpm limit = %d, want 8", got)
	}
	t.Setenv("SUBSCRIPTION_USER_RPM_LIMIT", "bad")
	if got := userRPMLimitFromEnv(); got != 3 {
		t.Fatalf("invalid user rpm limit fallback = %d, want 3", got)
	}
}

func TestHandleUserLimits(t *testing.T) {
	t.Setenv("SUBSCRIPTION_USER_RPM_LIMIT", "5")
	req := httptest.NewRequest(http.MethodGet, "/api/user/limits", nil)
	rec := httptest.NewRecorder()

	handleUserLimits(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool `json:"success"`
		Data    struct {
			UserRPMLimit int32 `json:"user_rpm_limit"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !body.Success || body.Data.UserRPMLimit != 5 {
		t.Fatalf("response = %+v, want success user_rpm_limit=5", body)
	}
}
