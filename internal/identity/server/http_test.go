package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"micro-one-api/internal/identity/biz"
	identitydata "micro-one-api/internal/identity/data"
	"micro-one-api/internal/pkg/oauth"
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
