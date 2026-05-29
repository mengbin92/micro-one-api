package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/internal/identity/biz"
	identitydata "micro-one-api/internal/identity/data"
	"micro-one-api/internal/pkg/oauth"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestIdentityHTTPRegisterLoginAndSelf(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"alice","password":"password123","email":"alice@example.com"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)
	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":true`) {
		t.Fatalf("register failed: %s", registerRec.Body.String())
	}

	loginReq := httptest.NewRequest(http.MethodPost, "/api/user/login", strings.NewReader(`{"username":"alice","password":"password123"}`))
	loginRec := httptest.NewRecorder()
	srv.ServeHTTP(loginRec, loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", loginRec.Code, loginRec.Body.String())
	}
	body := loginRec.Body.String()
	if !strings.Contains(body, `"token"`) {
		t.Fatalf("login response missing token: %s", body)
	}

	token := extractJSONField(body, "token")
	selfReq := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	selfReq.Header.Set("Authorization", "Bearer "+token)
	selfRec := httptest.NewRecorder()
	srv.ServeHTTP(selfRec, selfReq)
	if selfRec.Code != http.StatusOK {
		t.Fatalf("self status = %d, body=%s", selfRec.Code, selfRec.Body.String())
	}
	if !strings.Contains(selfRec.Body.String(), `"username":"alice"`) {
		t.Fatalf("self response mismatch: %s", selfRec.Body.String())
	}
}

func TestIdentityHTTPAffCodeRequiresAuth(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user/aff", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPAffCodeReturnsUserCode(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	if _, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default"); err != nil {
		t.Fatal(err)
	}
	_, authToken, err := uc.Login(context.Background(), "alice", "password123")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user/aff", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) || extractJSONField(rec.Body.String(), "data") == "" {
		t.Fatalf("aff response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPAffTransferReturnsDisabledCompatibilityResponse(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/user/aff_transfer", strings.NewReader(`{"quota":500000}`))
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) || !strings.Contains(rec.Body.String(), "aff transfer is not supported") {
		t.Fatalf("aff transfer response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPRegisterAcceptsAffCode(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	inviter, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewHTTPServer(":0", uc, nil)

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"bob","password":"password123","email":"bob@example.com","aff_code":"`+inviter.AffCode+`"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":true`) {
		t.Fatalf("register failed: %s", registerRec.Body.String())
	}
	bob, err := repo.FindUserByUsername(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	if bob.InviterID != inviter.ID {
		t.Fatalf("inviter id = %d, want %d", bob.InviterID, inviter.ID)
	}
}

func TestIdentityHTTPRegisterWithAffCodeCreditsInvitationBonusViaBilling(t *testing.T) {
	t.Setenv("INVITEE_BONUS_QUOTA", "25")
	t.Setenv("INVITER_BONUS_QUOTA", "50")
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	inviter, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	billingClient := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"bob","password":"password123","email":"bob@example.com","aff_code":"`+inviter.AffCode+`"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	bob, err := repo.FindUserByUsername(context.Background(), "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(billingClient.topUpCalls) != 2 {
		t.Fatalf("topup calls = %d, want 2: %#v", len(billingClient.topUpCalls), billingClient.topUpCalls)
	}
	invitee := billingClient.topUpCalls[0]
	if invitee.UserID != strconv.FormatInt(bob.ID, 10) || invitee.Amount != 25 || invitee.OperatorID != "system_invitation" || invitee.Remark != "invitation invitee bonus" {
		t.Fatalf("invitee credit = %#v", invitee)
	}
	inviterCall := billingClient.topUpCalls[1]
	wantRemark := "invitation inviter bonus, invitee=" + strconv.FormatInt(bob.ID, 10)
	if inviterCall.UserID != strconv.FormatInt(inviter.ID, 10) || inviterCall.Amount != 50 || inviterCall.OperatorID != "system_invitation" || inviterCall.Remark != wantRemark {
		t.Fatalf("inviter credit = %#v", inviterCall)
	}
}

func TestIdentityHTTPRegisterWithAffCodeSkipsCreditWhenBonusesZero(t *testing.T) {
	t.Setenv("INVITEE_BONUS_QUOTA", "")
	t.Setenv("INVITER_BONUS_QUOTA", "0")
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	inviter, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	billingClient := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"bob","password":"password123","email":"bob@example.com","aff_code":"`+inviter.AffCode+`"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if len(billingClient.topUpCalls) != 0 {
		t.Fatalf("expected no topup calls, got %#v", billingClient.topUpCalls)
	}
}

func TestIdentityHTTPRegisterWithoutAffCodeSkipsBillingCredit(t *testing.T) {
	t.Setenv("INVITEE_BONUS_QUOTA", "25")
	t.Setenv("INVITER_BONUS_QUOTA", "50")
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	billingClient := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"carol","password":"password123","email":"carol@example.com"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if len(billingClient.topUpCalls) != 0 {
		t.Fatalf("expected no topup calls without aff_code, got %#v", billingClient.topUpCalls)
	}
}

func TestIdentityHTTPRegisterRejectsInvalidAffCode(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"bob","password":"password123","email":"bob@example.com","aff_code":"NONE"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":false`) {
		t.Fatalf("expected failed registration: %s", registerRec.Body.String())
	}
}

func TestIdentityHTTPRegisterCanBeDisabled(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServerWithRegistrationPolicy(":0", uc, nil, RegistrationPolicy{Enabled: false})

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"alice","password":"password123","email":"alice@example.com"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":false`) || !strings.Contains(registerRec.Body.String(), "registration disabled") {
		t.Fatalf("registration disabled response mismatch: %s", registerRec.Body.String())
	}
}

func TestIdentityHTTPRegisterEnforcesEmailDomainWhitelist(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServerWithRegistrationPolicy(":0", uc, nil, RegistrationPolicy{
		Enabled:                       true,
		EmailDomainRestrictionEnabled: true,
		EmailDomainWhitelist:          []string{"example.com", "corp.test"},
	})

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"alice","password":"password123","email":"alice@blocked.test"}`))
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":false`) || !strings.Contains(registerRec.Body.String(), "email domain is not allowed") {
		t.Fatalf("domain restriction response mismatch: %s", registerRec.Body.String())
	}

	registerReq = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"bob","password":"password123","email":"bob@example.com"}`))
	registerRec = httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":true`) {
		t.Fatalf("allowed domain registration failed: %s", registerRec.Body.String())
	}
}

func TestIdentityHTTPRegisterEnforcesTurnstileWhenEnabled(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	verifier := &fakeTurnstileVerifier{acceptedToken: "pass"}
	srv := NewHTTPServerWithRegistrationPolicy(":0", uc, nil, RegistrationPolicy{
		Enabled:                true,
		TurnstileCheckEnabled:  true,
		TurnstileSecret:        "secret",
		TurnstileVerifyHandler: verifier,
	})

	registerReq := httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"alice","password":"password123","email":"alice@example.com","turnstile_token":"bad"}`))
	registerReq.RemoteAddr = "203.0.113.10:1234"
	registerRec := httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":false`) || !strings.Contains(registerRec.Body.String(), "turnstile verification failed") {
		t.Fatalf("turnstile reject response mismatch: %s", registerRec.Body.String())
	}
	if verifier.remoteIP != "203.0.113.10" {
		t.Fatalf("turnstile remote ip = %q, want 203.0.113.10", verifier.remoteIP)
	}

	registerReq = httptest.NewRequest(http.MethodPost, "/api/user/register", strings.NewReader(`{"username":"bob","password":"password123","email":"bob@example.com","turnstile_token":"pass"}`))
	registerRec = httptest.NewRecorder()
	srv.ServeHTTP(registerRec, registerReq)

	if registerRec.Code != http.StatusOK {
		t.Fatalf("register status = %d, body=%s", registerRec.Code, registerRec.Body.String())
	}
	if !strings.Contains(registerRec.Body.String(), `"success":true`) {
		t.Fatalf("turnstile accepted registration failed: %s", registerRec.Body.String())
	}
}

func TestIdentityHTTPEmailBindRequiresAuth(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/email/bind?email=new@example.com&code=123456", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPEmailBindRejectsInvalidCode(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/email/bind?email=new@example.com&code=bad", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) {
		t.Fatalf("bind response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPEmailBindUpdatesEmail(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	verifyReq := httptest.NewRequest(http.MethodGet, "/api/verification?email=new@example.com", nil)
	verifyRec := httptest.NewRecorder()
	srv.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verification status = %d, body=%s", verifyRec.Code, verifyRec.Body.String())
	}
	code := extractJSONField(verifyRec.Body.String(), "verification_code")
	if code == "" {
		t.Fatalf("verification code missing: %s", verifyRec.Body.String())
	}

	bindReq := httptest.NewRequest(http.MethodGet, "/api/oauth/email/bind?email=new@example.com&code="+code, nil)
	bindReq.Header.Set("Authorization", "Bearer "+authToken)
	bindRec := httptest.NewRecorder()
	srv.ServeHTTP(bindRec, bindReq)
	if bindRec.Code != http.StatusOK {
		t.Fatalf("bind status = %d, body=%s", bindRec.Code, bindRec.Body.String())
	}
	if !strings.Contains(bindRec.Body.String(), `"success":true`) {
		t.Fatalf("bind response mismatch: %s", bindRec.Body.String())
	}

	selfReq := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	selfReq.Header.Set("Authorization", "Bearer "+authToken)
	selfRec := httptest.NewRecorder()
	srv.ServeHTTP(selfRec, selfReq)
	if selfRec.Code != http.StatusOK {
		t.Fatalf("self status = %d, body=%s", selfRec.Code, selfRec.Body.String())
	}
	if !strings.Contains(selfRec.Body.String(), `"email":"new@example.com"`) {
		t.Fatalf("self response missing new email: %s", selfRec.Body.String())
	}
}

func TestIdentityHTTPSelfUpdateRequiresAuth(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(`{"username":"alice2"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPSelfUpdateChangesCurrentUser(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	updateReq := httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(`{"username":"alice2","display_name":"Alice Two","password":"newpass123"}`))
	updateReq.Header.Set("Authorization", "Bearer "+authToken)
	updateRec := httptest.NewRecorder()
	srv.ServeHTTP(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", updateRec.Code, updateRec.Body.String())
	}
	if !strings.Contains(updateRec.Body.String(), `"success":true`) {
		t.Fatalf("update response mismatch: %s", updateRec.Body.String())
	}

	_, newToken, err := uc.Login(context.Background(), "alice2", "newpass123")
	if err != nil {
		t.Fatalf("login with updated credentials failed: %v", err)
	}
	selfReq := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	selfReq.Header.Set("Authorization", "Bearer "+newToken)
	selfRec := httptest.NewRecorder()
	srv.ServeHTTP(selfRec, selfReq)

	if selfRec.Code != http.StatusOK {
		t.Fatalf("self status = %d, body=%s", selfRec.Code, selfRec.Body.String())
	}
	if !strings.Contains(selfRec.Body.String(), `"username":"alice2"`) || !strings.Contains(selfRec.Body.String(), `"display_name":"Alice Two"`) {
		t.Fatalf("self response mismatch: %s", selfRec.Body.String())
	}
}

func TestIdentityHTTPSelfDeleteRequiresAuth(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/user/self", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPSelfDeleteRemovesCurrentUser(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/user/self", nil)
	deleteReq.Header.Set("Authorization", "Bearer "+authToken)
	deleteRec := httptest.NewRecorder()
	srv.ServeHTTP(deleteRec, deleteReq)

	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if !strings.Contains(deleteRec.Body.String(), `"success":true`) {
		t.Fatalf("delete response mismatch: %s", deleteRec.Body.String())
	}

	selfReq := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	selfReq.Header.Set("Authorization", "Bearer "+authToken)
	selfRec := httptest.NewRecorder()
	srv.ServeHTTP(selfRec, selfReq)

	if selfRec.Code != http.StatusUnauthorized {
		t.Fatalf("self status after delete = %d, want 401, body=%s", selfRec.Code, selfRec.Body.String())
	}
}

func TestIdentityHTTPDashboardRequiresAuth(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/api/user/dashboard", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPDashboardRequiresBillingClient(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) {
		t.Fatalf("dashboard response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPUserLogsUsesAuthenticatedUser(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	user, authToken := registerAndLoginForHTTPTest(t, uc)
	billingClient := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/user/logs?page=1&page_size=20&type=consume&user_id=999", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.lastLedgerRequest == nil {
		t.Fatal("expected billing ListLedger to be called")
	}
	if billingClient.lastLedgerRequest.GetUserId() != strconv.FormatInt(user.ID, 10) {
		t.Fatalf("ledger user_id = %q, want authenticated user %d", billingClient.lastLedgerRequest.GetUserId(), user.ID)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"success":true`) || !strings.Contains(body, `"type":"consume"`) {
		t.Fatalf("logs response mismatch: %s", body)
	}
	for _, want := range []string{`"model_name":"mimo-v2.5"`, `"prompt_tokens":10`, `"completion_tokens":15`, `"endpoint":"/v1/chat/completions"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("logs response missing %s: %s", want, body)
		}
	}
	if strings.Contains(body, `"type":"recharge"`) {
		t.Fatalf("logs response should apply type filter: %s", body)
	}
}

func TestIdentityHTTPDashboardReturnsAccountSnapshot(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{
		snapshot: &commonv1.AccountSnapshot{
			Quota:        1000,
			UsedQuota:    100,
			RequestCount: 10,
			Group:        "default",
			GroupRatio:   1,
			FrozenQuota:  0,
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/user/dashboard", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		`"success":true`,
		`"quota":1000`,
		`"used_quota":100`,
		`"request_count":10`,
		`"group":"default"`,
		`"group_ratio":1`,
		`"frozen_quota":0`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard response missing %s: %s", want, body)
		}
	}
}

func TestIdentityHTTPUserReadOnlyCompatibilityAliases(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{
		snapshot: &commonv1.AccountSnapshot{
			Quota:        1000,
			UsedQuota:    100,
			RequestCount: 10,
			Group:        "default",
			GroupRatio:   1,
			FrozenQuota:  0,
		},
	})

	cases := []struct {
		path string
		want string
	}{
		{"/api/user/quota", `"quota":1000`},
		{"/api/user/models", `"data"`},
		{"/api/user/invitation", `"success":true`},
	}
	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+authToken)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s response missing %s: %s", tc.path, tc.want, rec.Body.String())
		}
	}
}

func TestIdentityHTTPAvailableModelsReturnsDefaultsWhenTokenIsUnrestricted(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/user/available_models", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Success bool     `json:"success"`
		Data    []string `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rec.Body.String())
	}
	if !body.Success || !containsString(body.Data, "gpt-4o-mini") || !containsString(body.Data, "deepseek-chat") {
		t.Fatalf("available models response mismatch: %+v body=%s", body, rec.Body.String())
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestIdentityHTTPDashboardBillingUsageReturnsOpenAIShape(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{
		snapshot: &commonv1.AccountSnapshot{
			Quota:     1000,
			UsedQuota: 123,
		},
	})

	for _, path := range []string{"/dashboard/billing/usage", "/v1/dashboard/billing/usage"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+authToken)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{
			`"object":"list"`,
			`"total_usage":12300`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s response missing %s: %s", path, want, body)
			}
		}
	}
}

func TestIdentityHTTPDashboardBillingSubscriptionReturnsOpenAIShape(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{
		snapshot: &commonv1.AccountSnapshot{
			Quota:     1000,
			UsedQuota: 123,
		},
	})

	for _, path := range []string{"/dashboard/billing/subscription", "/v1/dashboard/billing/subscription"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+authToken)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		for _, want := range []string{
			`"object":"billing_subscription"`,
			`"has_payment_method":false`,
			`"soft_limit_usd":1000`,
			`"hard_limit_usd":1000`,
			`"system_hard_limit_usd":1000`,
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("%s response missing %s: %s", path, want, body)
			}
		}
	}
}

func TestIdentityHTTPDashboardBillingSubscriptionRequiresAuth(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodGet, "/dashboard/billing/subscription", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"type":"one_api_error"`) {
		t.Fatalf("error response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPTopUpRequiresAuth(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodPost, "/api/user/topup", strings.NewReader(`{"key":"CODE-1000"}`))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPTopUpRejectsEmptyKey(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{})

	req := httptest.NewRequest(http.MethodPost, "/api/user/topup", strings.NewReader(`{"key":""}`))
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) {
		t.Fatalf("topup response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPTopUpReturnsRedeemedAmount(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	billingClient := &identityHTTPBillingClient{
		redeemResponse: &billingv1.RedeemCodeResponse{Success: true, Amount: 1000, NewQuota: 2000},
	}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	req := httptest.NewRequest(http.MethodPost, "/api/user/topup", strings.NewReader(`{"key":"CODE-1000"}`))
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.redeemCode != "CODE-1000" {
		t.Fatalf("redeem code = %q, want CODE-1000", billingClient.redeemCode)
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"data":1000`) {
		t.Fatalf("topup response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPTopUpReturnsBillingFailure(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil, &identityHTTPBillingClient{
		redeemResponse: &billingv1.RedeemCodeResponse{Success: false, ErrorMessage: "invalid code"},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/user/topup", strings.NewReader(`{"key":"BAD"}`))
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) || !strings.Contains(rec.Body.String(), `"message":"invalid code"`) {
		t.Fatalf("topup response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPOnlinePaymentCompatibilityRoutesAreDisabled(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	billingClient := &identityHTTPBillingClient{
		createOrderResponse: &billingv1.PaymentOrderResponse{
			Success: true,
			Order: &billingv1.PaymentOrder{
				TradeNo:   "PAY-TEST",
				PayUrl:    "mock://payment/PAY-TEST",
				AssetType: "quota",
			},
		},
	}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	for _, tc := range []struct {
		path string
		body string
	}{
		{"/api/user/amount", `{"amount":10,"payment_method":"alipay"}`},
		{"/api/user/pay", `{"amount":10,"payment_method":"alipay"}`},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Authorization", "Bearer "+authToken)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", tc.path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"trade_no":"PAY-TEST"`) {
			t.Fatalf("%s response mismatch: %s", tc.path, rec.Body.String())
		}
	}
	if len(billingClient.createOrderCalls) != 2 {
		t.Fatalf("CreatePaymentOrder calls = %d, want 2", len(billingClient.createOrderCalls))
	}
	for _, call := range billingClient.createOrderCalls {
		if call.GetChannel() != "alipay" || call.GetAssetType() != "quota" || call.GetCurrency() != "CNY" {
			t.Fatalf("payment order request mismatch: %+v", call)
		}
		if call.GetMoneyCents() != 1000 {
			t.Fatalf("money cents = %d, want 1000", call.GetMoneyCents())
		}
		if call.GetAssetAmount() != 50000000 {
			t.Fatalf("asset amount = %d, want 50000000", call.GetAssetAmount())
		}
	}
}

func TestIdentityHTTPUserPaymentOrdersFiltersToAuthenticatedUser(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	user, authToken := registerAndLoginForHTTPTest(t, uc)
	billingClient := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/user/payment/orders?page=2&page_size=50&user_id=999&status=paid&channel=alipay&trade_no=PAY", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	got := billingClient.lastPaymentListReq
	if got == nil {
		t.Fatal("ListPaymentOrders was not called")
	}
	if got.GetUserId() != strconv.FormatInt(user.ID, 10) {
		t.Fatalf("user_id = %q, want authenticated user %d", got.GetUserId(), user.ID)
	}
	if got.GetPage() != 2 || got.GetPageSize() != 50 || got.GetStatus() != "paid" || got.GetChannel() != "alipay" || got.GetTradeNo() != "PAY" {
		t.Fatalf("ListPaymentOrders request mismatch: %+v", got)
	}
	if !strings.Contains(rec.Body.String(), `"trade_no":"PAY-USER"`) {
		t.Fatalf("response missing payment order: %s", rec.Body.String())
	}
}

func TestIdentityHTTPAdminUserPaymentOrdersCanListAllUsers(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	user, err := uc.Register(context.Background(), "admin", "password123", "admin@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	user.Role = biz.RoleAdminUser
	if err := repo.UpdateUser(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	_, authToken, err := uc.Login(context.Background(), user.Username, "password123")
	if err != nil {
		t.Fatal(err)
	}
	billingClient := &identityHTTPBillingClient{}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/user/payment/orders?page=1&page_size=20&user_id=999", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	got := billingClient.lastPaymentListReq
	if got == nil {
		t.Fatal("ListPaymentOrders was not called")
	}
	if got.GetUserId() != "" {
		t.Fatalf("user_id = %q, want empty for admin user", got.GetUserId())
	}
}

func TestIdentityHTTPUserPaymentOrderDetailRefreshesAndScopesOrder(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	user, authToken := registerAndLoginForHTTPTest(t, uc)
	billingClient := &identityHTTPBillingClient{
		paymentGetResponse: &billingv1.PaymentOrderResponse{
			Success: true,
			Order: &billingv1.PaymentOrder{
				UserId:           strconv.FormatInt(user.ID, 10),
				TradeNo:          "PAY-DETAIL",
				Channel:          "alipay",
				AssetAmount:      50000000,
				MoneyCents:       1000,
				Currency:         "CNY",
				Status:           "paid",
				AssetIssueStatus: "issued",
			},
		},
	}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/user/payment/orders/PAY-DETAIL", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if billingClient.lastPaymentGetReq == nil || billingClient.lastPaymentGetReq.GetTradeNo() != "PAY-DETAIL" {
		t.Fatalf("GetPaymentOrderByTradeNo request mismatch: %+v", billingClient.lastPaymentGetReq)
	}
	if !strings.Contains(rec.Body.String(), `"trade_no":"PAY-DETAIL"`) || !strings.Contains(rec.Body.String(), `"status":"paid"`) {
		t.Fatalf("response mismatch: %s", rec.Body.String())
	}
}

func TestIdentityHTTPUserPaymentOrderDetailRejectsOtherUser(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	billingClient := &identityHTTPBillingClient{
		paymentGetResponse: &billingv1.PaymentOrderResponse{
			Success: true,
			Order: &billingv1.PaymentOrder{
				UserId:  "999",
				TradeNo: "PAY-OTHER",
			},
		},
	}
	srv := NewHTTPServer(":0", uc, nil, billingClient)

	req := httptest.NewRequest(http.MethodGet, "/api/user/payment/orders/PAY-OTHER", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPTokenCRUD(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	user, err := uc.Register(httptest.NewRequest(http.MethodGet, "/", nil).Context(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	loginUser, authToken, err := uc.Login(httptest.NewRequest(http.MethodGet, "/", nil).Context(), user.Username, "password123")
	if err != nil || loginUser.ID != user.ID {
		t.Fatalf("login error = %v", err)
	}
	srv := NewHTTPServer(":0", uc, nil)

	createReq := httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader(`{"name":"test-token","models":["gpt-4o-mini"]}`))
	createReq.Header.Set("Authorization", "Bearer "+authToken)
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create token status = %d, body=%s", createRec.Code, createRec.Body.String())
	}
	if !strings.Contains(createRec.Body.String(), `"key"`) {
		t.Fatalf("create token response missing key: %s", createRec.Body.String())
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/token/", nil)
	listReq.Header.Set("Authorization", "Bearer "+authToken)
	listRec := httptest.NewRecorder()
	srv.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list token status = %d, body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"total":1`) {
		t.Fatalf("list token response mismatch: %s", listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), authToken) {
		t.Fatalf("list token response exposed session token: %s", listRec.Body.String())
	}
}

func TestIdentityHTTPSessionTokenIsNotAPIToken(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	listReq := httptest.NewRequest(http.MethodGet, "/api/token/", nil)
	listReq.Header.Set("Authorization", "Bearer "+authToken)
	listRec := httptest.NewRecorder()
	srv.ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body=%s", listRec.Code, listRec.Body.String())
	}
	if !strings.Contains(listRec.Body.String(), `"total":0`) {
		t.Fatalf("list should not include the session token as an API key: %s", listRec.Body.String())
	}

	if _, err := uc.GetAuthSnapshot(context.Background(), authToken); err == nil {
		t.Fatal("session token should not be accepted as an API auth snapshot")
	}

	selfReq := httptest.NewRequest(http.MethodGet, "/api/user/self", nil)
	selfReq.Header.Set("Authorization", "Bearer "+authToken)
	selfRec := httptest.NewRecorder()
	srv.ServeHTTP(selfRec, selfReq)
	if selfRec.Code != http.StatusOK {
		t.Fatalf("session should remain valid after rejected delete, status = %d, body=%s", selfRec.Code, selfRec.Body.String())
	}
}

func TestIdentityHTTPTokenPathGetAndDelete(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	createReq := httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader(`{"name":"path-token","models":["gpt-4o-mini"]}`))
	createReq.Header.Set("Authorization", "Bearer "+authToken)
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", createRec.Code, createRec.Body.String())
	}
	tokenID := extractJSONNumberField(createRec.Body.String(), "id")
	if tokenID == "" {
		t.Fatalf("token id missing: %s", createRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/token/"+tokenID, nil)
	getReq.Header.Set("Authorization", "Bearer "+authToken)
	getRec := httptest.NewRecorder()
	srv.ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status = %d, body=%s", getRec.Code, getRec.Body.String())
	}
	if !strings.Contains(getRec.Body.String(), `"name":"path-token"`) {
		t.Fatalf("get response mismatch: %s", getRec.Body.String())
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/token/"+tokenID, nil)
	deleteReq.Header.Set("Authorization", "Bearer "+authToken)
	deleteRec := httptest.NewRecorder()
	srv.ServeHTTP(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body=%s", deleteRec.Code, deleteRec.Body.String())
	}
	if !strings.Contains(deleteRec.Body.String(), `"success":true`) {
		t.Fatalf("delete response mismatch: %s", deleteRec.Body.String())
	}
}

func TestIdentityHTTPTokenSearchRoute(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	for _, name := range []string{"alpha-token", "beta-token"} {
		req := httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader(`{"name":"`+name+`"}`))
		req.Header.Set("Authorization", "Bearer "+authToken)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("create %s status = %d, body=%s", name, rec.Code, rec.Body.String())
		}
	}

	searchReq := httptest.NewRequest(http.MethodGet, "/api/token/search?keyword=alpha", nil)
	searchReq.Header.Set("Authorization", "Bearer "+authToken)
	searchRec := httptest.NewRecorder()
	srv.ServeHTTP(searchRec, searchReq)
	if searchRec.Code != http.StatusOK {
		t.Fatalf("search status = %d, body=%s", searchRec.Code, searchRec.Body.String())
	}
	body := searchRec.Body.String()
	if !strings.Contains(body, `"name":"alpha-token"`) || strings.Contains(body, `"name":"beta-token"`) {
		t.Fatalf("search response mismatch: %s", body)
	}
}

func TestIdentityHTTPTokenUpdateAcceptsBodyID(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	createReq := httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader(`{"name":"old-name"}`))
	createReq.Header.Set("Authorization", "Bearer "+authToken)
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	tokenID := extractJSONNumberField(createRec.Body.String(), "id")
	if tokenID == "" {
		t.Fatalf("token id missing: %s", createRec.Body.String())
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/token/", strings.NewReader(`{"id":`+tokenID+`,"name":"new-name","models":["gpt-4o-mini"],"status":1}`))
	updateReq.Header.Set("Authorization", "Bearer "+authToken)
	updateRec := httptest.NewRecorder()
	srv.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", updateRec.Code, updateRec.Body.String())
	}
	if !strings.Contains(updateRec.Body.String(), `"success":true`) || !strings.Contains(updateRec.Body.String(), `"name":"new-name"`) {
		t.Fatalf("update response mismatch: %s", updateRec.Body.String())
	}
}

func TestIdentityHTTPTokenOneAPIFields(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, nil)

	createReq := httptest.NewRequest(http.MethodPost, "/api/token/", strings.NewReader(`{
		"name":"field-token",
		"models":["gpt-4o-mini"],
		"remain_quota":500,
		"unlimited_quota":true,
		"subnet":"192.168.0.0/16"
	}`))
	createReq.Header.Set("Authorization", "Bearer "+authToken)
	createRec := httptest.NewRecorder()
	srv.ServeHTTP(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", createRec.Code, createRec.Body.String())
	}
	body := createRec.Body.String()
	for _, want := range []string{
		`"created_time":`,
		`"accessed_time":`,
		`"used_quota":0`,
		`"remain_quota":500`,
		`"unlimited_quota":true`,
		`"subnet":"192.168.0.0/16"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("create response missing %s: %s", want, body)
		}
	}

	tokenID := extractJSONNumberField(body, "id")
	updateReq := httptest.NewRequest(http.MethodPut, "/api/token/", strings.NewReader(`{
		"id":`+tokenID+`,
		"name":"field-token-updated",
		"status":1,
		"remain_quota":250,
		"unlimited_quota":false,
		"subnet":"10.0.0.0/8"
	}`))
	updateReq.Header.Set("Authorization", "Bearer "+authToken)
	updateRec := httptest.NewRecorder()
	srv.ServeHTTP(updateRec, updateReq)
	if updateRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body=%s", updateRec.Code, updateRec.Body.String())
	}
	updateBody := updateRec.Body.String()
	for _, want := range []string{
		`"remain_quota":250`,
		`"unlimited_quota":false`,
		`"subnet":"10.0.0.0/8"`,
	} {
		if !strings.Contains(updateBody, want) {
			t.Fatalf("update response missing %s: %s", want, updateBody)
		}
	}
}

func TestIdentityHTTPPasswordReset(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	if _, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default"); err != nil {
		t.Fatal(err)
	}
	srv := NewHTTPServer(":0", uc, nil)

	resetReq := httptest.NewRequest(http.MethodGet, "/api/reset_password?email=alice@example.com", nil)
	resetRec := httptest.NewRecorder()
	srv.ServeHTTP(resetRec, resetReq)
	if resetRec.Code != http.StatusOK {
		t.Fatalf("reset request status = %d, body=%s", resetRec.Code, resetRec.Body.String())
	}
	resetToken := extractJSONField(resetRec.Body.String(), "token")
	if resetToken == "" {
		t.Fatalf("reset token missing: %s", resetRec.Body.String())
	}

	confirmReq := httptest.NewRequest(http.MethodPost, "/api/user/reset", strings.NewReader(`{"email":"alice@example.com","token":"`+resetToken+`","password":"newpass123"}`))
	confirmRec := httptest.NewRecorder()
	srv.ServeHTTP(confirmRec, confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm status = %d, body=%s", confirmRec.Code, confirmRec.Body.String())
	}
	if !strings.Contains(confirmRec.Body.String(), `"success":true`) {
		t.Fatalf("password reset failed: %s", confirmRec.Body.String())
	}

	if _, _, err := uc.Login(context.Background(), "alice", "newpass123"); err != nil {
		t.Fatalf("login with reset password failed: %v", err)
	}
}

func TestIdentityHTTPOAuthLegacyAliasRedirects(t *testing.T) {
	registry := oauth.NewProviderRegistry()
	registry.Register(oauth.NewGitHubProvider(oauth.Config{
		ClientID:    "client-id",
		RedirectURL: "http://localhost/callback",
	}))
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, registry)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/github", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); !strings.Contains(location, "github.com/login/oauth/authorize") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestIdentityHTTPOAuthStateReturnsStateAndCookie(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/state", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":true`) || !strings.Contains(rec.Body.String(), `"state"`) {
		t.Fatalf("state response mismatch: %s", rec.Body.String())
	}
	if cookie := rec.Result().Cookies(); len(cookie) == 0 || cookie[0].Name != "oauth_state" {
		t.Fatalf("oauth state cookie was not set: %+v", cookie)
	}
}

func TestIdentityHTTPOneAPIOAuthAliasesAreStableWhenProviderDisabled(t *testing.T) {
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, nil)

	for _, path := range []string{"/api/oauth/oidc", "/api/oauth/lark", "/api/oauth/lark/bind", "/api/oauth/wechat", "/api/oauth/wechat/bind", "/api/oauth/telegram/login", "/api/oauth/telegram/bind"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200, body=%s", path, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"success":false`) || !strings.Contains(rec.Body.String(), "disabled") {
			t.Fatalf("%s disabled response mismatch: %s", path, rec.Body.String())
		}
	}
}

func TestIdentityHTTPOneAPIOIDCAliasRedirectsWhenProviderEnabled(t *testing.T) {
	registry := oauth.NewProviderRegistry()
	registry.Register(oauth.NewOIDCProvider(oauth.OIDCConfig{
		Config: oauth.Config{
			ClientID:    "client-id",
			RedirectURL: "http://localhost/v1/oauth/oidc/callback",
		},
		AuthorizeURL: "https://idp.example.com/oauth2/authorize",
		TokenURL:     "https://idp.example.com/oauth2/token",
		UserInfoURL:  "https://idp.example.com/oauth2/userinfo",
	}))
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, registry)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/oidc", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302, body=%s", rec.Code, rec.Body.String())
	}
	if location := rec.Header().Get("Location"); !strings.Contains(location, "idp.example.com/oauth2/authorize") {
		t.Fatalf("unexpected redirect location: %s", location)
	}
}

func TestIdentityHTTPOAuthBindRequiresAuthenticatedUser(t *testing.T) {
	registry := oauth.NewProviderRegistry()
	registry.Register(&fakeOAuthProvider{name: "wechat", providerID: "openid-1"})
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	srv := NewHTTPServer(":0", uc, registry)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/wechat/bind?code=oauth-code", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401, body=%s", rec.Code, rec.Body.String())
	}
}

func TestIdentityHTTPOAuthBindUpdatesCurrentUser(t *testing.T) {
	for _, tt := range []struct {
		provider   string
		providerID string
		path       string
	}{
		{provider: "github", providerID: "gh-1", path: "/api/oauth/github/bind?code=oauth-code"},
		{provider: "oidc", providerID: "sub-1", path: "/api/oauth/oidc/bind?code=oauth-code"},
		{provider: "lark", providerID: "open-1", path: "/api/oauth/lark/bind?code=oauth-code"},
		{provider: "wechat", providerID: "openid-1", path: "/api/oauth/wechat/bind?code=oauth-code"},
	} {
		t.Run(tt.provider, func(t *testing.T) {
			registry := oauth.NewProviderRegistry()
			registry.Register(&fakeOAuthProvider{name: tt.provider, providerID: tt.providerID})
			repo := identitydata.NewMemoryRepositoryForTest()
			uc := biz.NewIdentityUsecase(repo)
			_, authToken := registerAndLoginForHTTPTest(t, uc)
			srv := NewHTTPServer(":0", uc, registry)

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+authToken)
			rec := httptest.NewRecorder()
			srv.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), `"success":true`) {
				t.Fatalf("bind response mismatch: %s", rec.Body.String())
			}
			if _, err := repo.FindOAuthIdentity(context.Background(), tt.provider, tt.providerID); err != nil {
				t.Fatalf("bound identity was not persisted: %v", err)
			}
		})
	}
}

func TestIdentityHTTPOAuthBindRejectsDuplicateProviderIdentity(t *testing.T) {
	registry := oauth.NewProviderRegistry()
	registry.Register(&fakeOAuthProvider{name: "lark", providerID: "union-1"})
	repo := identitydata.NewMemoryRepositoryForTest()
	uc := biz.NewIdentityUsecase(repo)
	if _, _, err := uc.OAuthLogin(context.Background(), "lark", "union-1", "bob", "bob@example.com", "Bob"); err != nil {
		t.Fatal(err)
	}
	_, authToken := registerAndLoginForHTTPTest(t, uc)
	srv := NewHTTPServer(":0", uc, registry)

	req := httptest.NewRequest(http.MethodGet, "/api/oauth/lark/bind?code=oauth-code", nil)
	req.Header.Set("Authorization", "Bearer "+authToken)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) || !strings.Contains(rec.Body.String(), "already bound") {
		t.Fatalf("duplicate bind response mismatch: %s", rec.Body.String())
	}
}

type identityHTTPBillingClient struct {
	billingv1.BillingServiceClient
	snapshot            *commonv1.AccountSnapshot
	redeemResponse      *billingv1.RedeemCodeResponse
	createOrderResponse *billingv1.PaymentOrderResponse
	redeemCode          string
	topUpCalls          []capturedTopUp
	createOrderCalls    []billingv1.CreatePaymentOrderRequest
	lastLedgerRequest   *billingv1.ListLedgerRequest
	lastPaymentListReq  *billingv1.ListPaymentOrdersRequest
	lastPaymentGetReq   *billingv1.GetPaymentOrderByTradeNoRequest
	paymentGetResponse  *billingv1.PaymentOrderResponse
}

type capturedTopUp struct {
	UserID     string
	Amount     int64
	OperatorID string
	Remark     string
}

type fakeTurnstileVerifier struct {
	acceptedToken string
	remoteIP      string
}

type fakeOAuthProvider struct {
	name       string
	providerID string
}

func (p *fakeOAuthProvider) Name() string { return p.name }

func (p *fakeOAuthProvider) AuthURL(state string) string {
	return "https://oauth.example.com/authorize?state=" + state
}

func (p *fakeOAuthProvider) Exchange(ctx context.Context, code string) (*oauth.UserInfo, error) {
	return &oauth.UserInfo{
		Provider:    p.name,
		ProviderID:  p.providerID,
		Username:    p.name + "-user",
		Email:       p.name + "@example.com",
		DisplayName: p.name + " user",
	}, nil
}

func (v *fakeTurnstileVerifier) VerifyTurnstile(ctx context.Context, secret, token, remoteIP string) error {
	v.remoteIP = remoteIP
	if token != v.acceptedToken {
		return biz.ErrInvalidToken
	}
	return nil
}

func (c *identityHTTPBillingClient) GetAccountSnapshot(ctx context.Context, req *billingv1.GetAccountSnapshotRequest, opts ...grpc.CallOption) (*billingv1.GetAccountSnapshotResponse, error) {
	return &billingv1.GetAccountSnapshotResponse{Snapshot: c.snapshot}, nil
}

func (c *identityHTTPBillingClient) RedeemCode(ctx context.Context, req *billingv1.RedeemCodeRequest, opts ...grpc.CallOption) (*billingv1.RedeemCodeResponse, error) {
	c.redeemCode = req.Code
	if c.redeemResponse == nil {
		return &billingv1.RedeemCodeResponse{Success: true}, nil
	}
	return c.redeemResponse, nil
}

func (c *identityHTTPBillingClient) CreatePaymentOrder(ctx context.Context, req *billingv1.CreatePaymentOrderRequest, opts ...grpc.CallOption) (*billingv1.PaymentOrderResponse, error) {
	c.createOrderCalls = append(c.createOrderCalls, *req)
	if c.createOrderResponse == nil {
		return &billingv1.PaymentOrderResponse{Success: true, Order: &billingv1.PaymentOrder{TradeNo: "PAY-TEST", PayUrl: "mock://payment/PAY-TEST"}}, nil
	}
	return c.createOrderResponse, nil
}

func (c *identityHTTPBillingClient) GetPaymentOrderByTradeNo(ctx context.Context, req *billingv1.GetPaymentOrderByTradeNoRequest, opts ...grpc.CallOption) (*billingv1.PaymentOrderResponse, error) {
	c.lastPaymentGetReq = req
	if c.paymentGetResponse != nil {
		return c.paymentGetResponse, nil
	}
	return &billingv1.PaymentOrderResponse{
		Success: true,
		Order: &billingv1.PaymentOrder{
			Id:               7,
			UserId:           "1",
			TradeNo:          req.GetTradeNo(),
			Channel:          "alipay",
			AssetType:        "quota",
			AssetAmount:      50000000,
			MoneyCents:       1000,
			Currency:         "CNY",
			Status:           "paid",
			AssetIssueStatus: "issued",
			CreatedAt:        timestamppb.Now(),
		},
	}, nil
}

func (c *identityHTTPBillingClient) MarkPaymentOrderPaid(ctx context.Context, req *billingv1.MarkPaymentOrderPaidRequest, opts ...grpc.CallOption) (*billingv1.PaymentOrderResponse, error) {
	return &billingv1.PaymentOrderResponse{}, nil
}

func (c *identityHTTPBillingClient) ListPaymentOrders(ctx context.Context, req *billingv1.ListPaymentOrdersRequest, opts ...grpc.CallOption) (*billingv1.ListPaymentOrdersResponse, error) {
	c.lastPaymentListReq = req
	return &billingv1.ListPaymentOrdersResponse{
		Orders: []*billingv1.PaymentOrder{
			{
				Id:               7,
				UserId:           req.GetUserId(),
				TradeNo:          "PAY-USER",
				Channel:          "alipay",
				AssetType:        "quota",
				AssetAmount:      50000000,
				MoneyCents:       1000,
				Currency:         "CNY",
				Status:           "paid",
				AssetIssueStatus: "issued",
				CreatedAt:        timestamppb.Now(),
			},
		},
		Total: 1,
	}, nil
}

func (c *identityHTTPBillingClient) TopUpQuota(ctx context.Context, req *billingv1.TopUpQuotaRequest, opts ...grpc.CallOption) (*billingv1.TopUpQuotaResponse, error) {
	c.topUpCalls = append(c.topUpCalls, capturedTopUp{
		UserID:     req.GetUserId(),
		Amount:     req.GetAmount(),
		OperatorID: req.GetOperatorId(),
		Remark:     req.GetRemark(),
	})
	return &billingv1.TopUpQuotaResponse{Success: true, NewQuota: req.GetAmount()}, nil
}

func (c *identityHTTPBillingClient) ListLedger(ctx context.Context, req *billingv1.ListLedgerRequest, opts ...grpc.CallOption) (*billingv1.ListLedgerResponse, error) {
	c.lastLedgerRequest = req
	return &billingv1.ListLedgerResponse{
		Entries: []*commonv1.LedgerEntry{
			{
				Id:               "1",
				UserId:           req.GetUserId(),
				Type:             "consume",
				Amount:           -25,
				BalanceAfter:     975,
				ReferenceId:      "res-1",
				Remark:           "model=mimo-v2.5, tokens=25",
				CreatedAt:        timestamppb.Now(),
				TokenName:        "token-1",
				ModelName:        "mimo-v2.5",
				Quota:            25,
				PromptTokens:     10,
				CompletionTokens: 15,
				ChannelId:        2,
				ElapsedTime:      123,
				Endpoint:         "/v1/chat/completions",
			},
			{
				Id:           "2",
				UserId:       req.GetUserId(),
				Type:         "recharge",
				Amount:       1000,
				BalanceAfter: 1000,
				ReferenceId:  "topup-1",
				Remark:       "manual",
				CreatedAt:    timestamppb.Now(),
			},
		},
		Total: 2,
	}, nil
}

func registerAndLoginForHTTPTest(t *testing.T, uc *biz.IdentityUsecase) (*biz.User, string) {
	t.Helper()
	user, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}
	_, authToken, err := uc.Login(context.Background(), user.Username, "password123")
	if err != nil {
		t.Fatal(err)
	}
	return user, authToken
}

func extractJSONField(body, key string) string {
	prefix := `"` + key + `":"`
	idx := strings.Index(body, prefix)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(prefix):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func extractJSONNumberField(body, key string) string {
	prefix := `"` + key + `":`
	idx := strings.Index(body, prefix)
	if idx < 0 {
		return ""
	}
	rest := body[idx+len(prefix):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	return rest[:end]
}
