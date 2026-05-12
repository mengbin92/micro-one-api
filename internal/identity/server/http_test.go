package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	billingv1 "micro-one-api/api/billing/v1"
	commonv1 "micro-one-api/api/common/v1"
	"micro-one-api/internal/identity/biz"
	identitydata "micro-one-api/internal/identity/data"
	"micro-one-api/internal/pkg/oauth"

	"google.golang.org/grpc"
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
	if !strings.Contains(listRec.Body.String(), `"total":2`) {
		t.Fatalf("list token response mismatch: %s", listRec.Body.String())
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

	for _, path := range []string{"/api/oauth/oidc", "/api/oauth/lark", "/api/oauth/wechat", "/api/oauth/wechat/bind"} {
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

type identityHTTPBillingClient struct {
	billingv1.BillingServiceClient
	snapshot       *commonv1.AccountSnapshot
	redeemResponse *billingv1.RedeemCodeResponse
	redeemCode     string
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
