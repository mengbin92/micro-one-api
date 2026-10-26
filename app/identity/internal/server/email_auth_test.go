package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"github.com/stretchr/testify/require"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/identity/internal/biz"
	identitydata "micro-one-api/app/identity/internal/data"
)

// Transport tests fake the domain seam. Redis atomicity/TTL tests exercise the
// actual repository separately; no production transport has a memory fallback.
type httpChallengeRecord struct {
	hash   string
	record biz.EmailChallenge
	active bool
}
type httpChallengeRepo struct {
	sync.Mutex
	records map[string]httpChallengeRecord
}

func newHTTPChallengeRepo() *httpChallengeRepo {
	return &httpChallengeRepo{records: map[string]httpChallengeRecord{}}
}

func waitForResetToken(t *testing.T, mail *capturingCodeDeliverer, email string) string {
	t.Helper()
	var token string
	require.Eventually(t, func() bool {
		mail.mu.Lock()
		token = mail.resetTokens[email]
		mail.mu.Unlock()
		if token == "" {
			return false
		}
		mail.challenges.Lock()
		defer mail.challenges.Unlock()
		normalized, _ := biz.NormalizeEmail(email)
		return mail.challenges.records[biz.ChallengePasswordReset+normalized].active
	}, time.Second, time.Millisecond)
	return token
}
func (s *httpChallengeRepo) Reserve(_ context.Context, purpose, email, hash string, record biz.EmailChallenge, _, _ time.Duration) error {
	s.Lock()
	defer s.Unlock()
	s.records[purpose+email] = httpChallengeRecord{hash: hash, record: record}
	return nil
}
func (s *httpChallengeRepo) Activate(_ context.Context, purpose, email, hash string) error {
	s.Lock()
	defer s.Unlock()
	r, ok := s.records[purpose+email]
	if !ok || r.hash != hash {
		return biz.ErrChallengeInvalid
	}
	r.active = true
	s.records[purpose+email] = r
	return nil
}
func (s *httpChallengeRepo) Cancel(_ context.Context, purpose, email, hash string) error {
	s.Lock()
	defer s.Unlock()
	if r := s.records[purpose+email]; r.hash == hash {
		delete(s.records, purpose+email)
	}
	return nil
}
func (s *httpChallengeRepo) Consume(_ context.Context, purpose, email, hash string, _ int) (*biz.EmailChallenge, error) {
	s.Lock()
	defer s.Unlock()
	r, ok := s.records[purpose+email]
	if !ok || !r.active || r.hash != hash {
		return nil, biz.ErrChallengeInvalid
	}
	delete(s.records, purpose+email)
	return &r.record, nil
}
func newHTTPServerWithVerifiedEmail(addr string, uc *biz.IdentityUsecase, billing ...billingv1.BillingServiceClient) *khttp.Server {
	repo := newHTTPChallengeRepo()
	hash := sha256.Sum256([]byte("verified"))
	repo.records[biz.ChallengeVerification+"bob@example.com"] = httpChallengeRecord{hash: hex.EncodeToString(hash[:]), active: true}
	return NewHTTPServerWithRegistrationPolicy(addr, uc, nil, RegistrationPolicy{Enabled: true, Challenges: biz.NewEmailChallengeUsecase(repo, newCapturingCodeDeliverer())}, billing...)
}

func TestEmailAuthenticationPolicyAndUnavailableDelivery(t *testing.T) {
	uc := biz.NewIdentityUsecase(identitydata.NewMemoryRepositoryForTest(), nil)
	srv := NewHTTPServerWithRegistrationPolicy(":0", uc, nil, RegistrationPolicy{Enabled: true, EmailVerificationEnabled: true, TurnstileSecret: "must-not-leak", TurnstileSiteKey: "public-key"})
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/registration/policy", nil))
	require.Contains(t, w.Body.String(), `"email_delivery_configured":false`)
	require.Contains(t, w.Body.String(), `"email_verification":true`)
	require.NotContains(t, w.Body.String(), "must-not-leak")
	for _, path := range []string{"/api/verification?email=alice@example.com", "/api/reset_password?email=alice@example.com", "/api/reset_password?email=unknown@example.com"} {
		w = httptest.NewRecorder()
		srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		require.Contains(t, w.Body.String(), `"success":false`)
	}
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"alice","password":"password123","email":"alice@example.com"}`)))
	require.Contains(t, w.Body.String(), "email verification code is required")
}

func TestEmailRecoveryAcrossServersRevokesSessionsAndConsumesOnce(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo, nil)
	_, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	require.NoError(t, err)
	_, oldToken, err := uc.Login(context.Background(), "alice", "password123", "")
	require.NoError(t, err)
	shared := newHTTPChallengeRepo()
	mail := newCapturingCodeDeliverer()
	mail.challenges = shared
	server := func() *khttp.Server {
		return NewHTTPServerWithRegistrationPolicy(":0", uc, nil, RegistrationPolicy{Enabled: true, Challenges: biz.NewEmailChallengeUsecase(shared, mail)})
	}
	first, second := server(), server()
	known, unknown := httptest.NewRecorder(), httptest.NewRecorder()
	first.ServeHTTP(known, httptest.NewRequest(http.MethodGet, "/api/reset_password?email=alice@example.com", nil))
	first.ServeHTTP(unknown, httptest.NewRequest(http.MethodGet, "/api/reset_password?email=unknown@example.com", nil))
	require.Equal(t, known.Code, unknown.Code)
	require.Equal(t, known.Body.String(), unknown.Body.String())
	token := waitForResetToken(t, mail, "alice@example.com")
	require.NotEmpty(t, token)
	body := `{"email":"alice@example.com","token":"` + token + `","password":"changed123"}`
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		second.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/user/reset", strings.NewReader(body)))
		if i == 0 {
			require.Contains(t, w.Body.String(), `"success":true`)
		} else {
			require.Contains(t, w.Body.String(), `"success":false`)
		}
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	r.Header.Set("Authorization", "Bearer "+oldToken)
	second.ServeHTTP(w, r)
	require.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	_, _, err = uc.Login(context.Background(), "alice", "changed123", "")
	require.NoError(t, err)
}
