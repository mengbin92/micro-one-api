package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	identitydata "micro-one-api/app/identity/internal/data"
)

type failingResetDeliverer struct{ noopCodeDeliverer }

func (failingResetDeliverer) DeliverResetToken(context.Context, string, string) error {
	return errors.New("mail unavailable")
}

func TestResetIssuanceDoesNotRevealUnknownEmailOrMailFailure(t *testing.T) {
	uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
	_, err := uc.Register(context.Background(), "reset-user", "password123", "known-review@example.com", "default")
	require.NoError(t, err)
	for _, email := range []string{"known-review@example.com", "unknown-review@example.com"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/reset_password?email="+email, nil)
		handleResetPasswordRequest(w, r, failingResetDeliverer{}, uc)
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"success":true`)
		verificationStore.Lock()
		_, exists := verificationStore.items["r:"+email]
		verificationStore.Unlock()
		require.False(t, exists)
	}
}

func TestPublicIdentityRateLimit(t *testing.T) {
	uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
	h := publicIdentityRateLimit(uc)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	for i := 0; i < 4; i++ {
		r := httptest.NewRequest(http.MethodGet, "/api/verification?email=alice@example.com", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i < 3 {
			require.Equal(t, http.StatusNoContent, w.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code)
		}
	}
	for i := 4; i < 31; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/user/register", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if i < 30 {
			require.Equal(t, http.StatusNoContent, w.Code)
		} else {
			require.Equal(t, http.StatusTooManyRequests, w.Code)
		}
	}
}

func TestIdentityBodyLimitAndOversizedPayment(t *testing.T) {
	uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
	_, token := registerAndLoginForHTTPTest(t, uc)
	billing := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, billing)
	r := httptest.NewRequest(http.MethodPost, "/api/user/amount", strings.NewReader(`{"amount":1e18,"payment_method":"alipay"}`))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Contains(t, w.Body.String(), `"success":false`)
	require.Empty(t, billing.createOrderCalls)
	r = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"`+strings.Repeat("x", 1<<20)+`"}`))
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
