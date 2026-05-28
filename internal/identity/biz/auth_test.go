package biz

import (
	"context"
	"errors"
	"testing"
	"time"
)

type mockIdentityRepo struct {
	tokens          map[string]*Token
	users           map[int64]*User
	oauthIdentities map[string]*OAuthIdentity
}

func (m *mockIdentityRepo) FindTokenByKey(ctx context.Context, key string) (*Token, error) {
	token, ok := m.tokens[key]
	if !ok {
		return nil, ErrTokenNotFound
	}
	return token, nil
}

func (m *mockIdentityRepo) FindUserByID(ctx context.Context, userID int64) (*User, error) {
	user, ok := m.users[userID]
	if !ok {
		return nil, ErrUserNotFound
	}
	return user, nil
}

func (m *mockIdentityRepo) FindUserByUsername(ctx context.Context, username string) (*User, error) {
	for _, u := range m.users {
		if u.Username == username {
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *mockIdentityRepo) FindUserByEmail(ctx context.Context, email string) (*User, error) {
	for _, u := range m.users {
		if u.Email == email {
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *mockIdentityRepo) FindUserByAffCode(ctx context.Context, affCode string) (*User, error) {
	for _, u := range m.users {
		if u.AffCode == affCode {
			return u, nil
		}
	}
	return nil, ErrUserNotFound
}

func (m *mockIdentityRepo) FindUserByOAuth(ctx context.Context, provider, oauthID string) (*User, error) {
	for _, u := range m.users {
		if u.OAuthProvider == provider && u.OAuthID == oauthID {
			return u, nil
		}
	}
	return nil, ErrOAuthUserNotFound
}

func (m *mockIdentityRepo) FindOAuthIdentity(ctx context.Context, provider, providerID string) (*OAuthIdentity, error) {
	identity, ok := m.oauthIdentities[provider+":"+providerID]
	if !ok {
		return nil, ErrOAuthUserNotFound
	}
	return identity, nil
}

func (m *mockIdentityRepo) FindOAuthIdentityByUserProvider(ctx context.Context, userID int64, provider string) (*OAuthIdentity, error) {
	for _, identity := range m.oauthIdentities {
		if identity.UserID == userID && identity.Provider == provider {
			return identity, nil
		}
	}
	return nil, ErrOAuthUserNotFound
}

func (m *mockIdentityRepo) CreateOAuthIdentity(ctx context.Context, identity *OAuthIdentity) error {
	if m.oauthIdentities == nil {
		m.oauthIdentities = map[string]*OAuthIdentity{}
	}
	key := identity.Provider + ":" + identity.ProviderID
	if existing, ok := m.oauthIdentities[key]; ok && existing.UserID != identity.UserID {
		return ErrOAuthAlreadyBound
	}
	for _, existing := range m.oauthIdentities {
		if existing.UserID == identity.UserID && existing.Provider == identity.Provider {
			return ErrOAuthAlreadyBound
		}
	}
	identity.ID = int64(len(m.oauthIdentities) + 1)
	m.oauthIdentities[key] = identity
	return nil
}

func (m *mockIdentityRepo) CreateUser(ctx context.Context, user *User) error {
	user.ID = int64(len(m.users) + 1)
	m.users[user.ID] = user
	return nil
}

func (m *mockIdentityRepo) UpdateUser(ctx context.Context, user *User) error {
	m.users[user.ID] = user
	return nil
}

func (m *mockIdentityRepo) DeleteUser(ctx context.Context, userID int64) error {
	delete(m.users, userID)
	return nil
}

func (m *mockIdentityRepo) IncreaseUserQuota(ctx context.Context, userID int64, amount int64) error {
	user, ok := m.users[userID]
	if !ok {
		return ErrUserNotFound
	}
	user.Quota += amount
	return nil
}

func TestIdentityUsecase_BindOAuthIdentityUpdatesCurrentUser(t *testing.T) {
	repo := &mockIdentityRepo{
		users:           map[int64]*User{1: {ID: 1, Username: "alice", Status: UserStatusEnabled}},
		oauthIdentities: map[string]*OAuthIdentity{},
	}
	uc := NewIdentityUsecase(repo)

	user, err := uc.BindOAuthIdentity(context.Background(), 1, "wechat", "openid-1")
	if err != nil {
		t.Fatalf("BindOAuthIdentity() error = %v", err)
	}
	if user.OAuthProvider != "" || user.OAuthID != "" {
		t.Fatalf("legacy user oauth fields should not be overwritten: %+v", user)
	}
	identity := repo.oauthIdentities["wechat:openid-1"]
	if identity == nil || identity.UserID != 1 {
		t.Fatalf("oauth identity was not created: %+v", repo.oauthIdentities)
	}
}

func TestIdentityUsecase_BindOAuthIdentityAllowsMultipleProvidersForSameUser(t *testing.T) {
	repo := &mockIdentityRepo{
		users:           map[int64]*User{1: {ID: 1, Username: "alice", Status: UserStatusEnabled}},
		oauthIdentities: map[string]*OAuthIdentity{},
	}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.BindOAuthIdentity(context.Background(), 1, "github", "gh-1"); err != nil {
		t.Fatalf("BindOAuthIdentity(github) error = %v", err)
	}
	if _, err := uc.BindOAuthIdentity(context.Background(), 1, "oidc", "oidc-1"); err != nil {
		t.Fatalf("BindOAuthIdentity(oidc) error = %v", err)
	}
	if _, err := uc.BindOAuthIdentity(context.Background(), 1, "lark", "lark-1"); err != nil {
		t.Fatalf("BindOAuthIdentity(lark) error = %v", err)
	}
	if _, err := uc.BindOAuthIdentity(context.Background(), 1, "wechat", "wechat-1"); err != nil {
		t.Fatalf("BindOAuthIdentity(wechat) error = %v", err)
	}
	if len(repo.oauthIdentities) != 4 {
		t.Fatalf("identity count = %d, want 4", len(repo.oauthIdentities))
	}
}

func TestIdentityUsecase_BindOAuthIdentityRejectsDuplicateProviderIdentity(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
			2: {ID: 2, Username: "bob", Status: UserStatusEnabled},
		},
		oauthIdentities: map[string]*OAuthIdentity{
			"lark:union-1": {ID: 1, UserID: 2, Provider: "lark", ProviderID: "union-1"},
		},
	}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.BindOAuthIdentity(context.Background(), 1, "lark", "union-1"); !errors.Is(err, ErrOAuthAlreadyBound) {
		t.Fatal("BindOAuthIdentity() expected duplicate identity error")
	}
}

func TestIdentityUsecase_BindOAuthIdentityRejectsDuplicateProviderForSameUser(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
		},
		oauthIdentities: map[string]*OAuthIdentity{
			"github:gh-1": {ID: 1, UserID: 1, Provider: "github", ProviderID: "gh-1"},
		},
	}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.BindOAuthIdentity(context.Background(), 1, "github", "gh-2"); !errors.Is(err, ErrOAuthAlreadyBound) {
		t.Fatal("BindOAuthIdentity() expected duplicate provider error")
	}
}

func TestIdentityUsecase_OAuthLoginFindsUserByOAuthIdentity(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{},
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
		},
		oauthIdentities: map[string]*OAuthIdentity{
			"oidc:sub-1": {ID: 1, UserID: 1, Provider: "oidc", ProviderID: "sub-1"},
		},
	}
	uc := NewIdentityUsecase(repo)

	user, _, err := uc.OAuthLogin(context.Background(), "oidc", "sub-1", "ignored", "ignored@example.com", "Ignored")
	if err != nil {
		t.Fatalf("OAuthLogin() error = %v", err)
	}
	if user.ID != 1 || user.Username != "alice" {
		t.Fatalf("OAuthLogin() user = %+v, want alice", user)
	}
}

func TestIdentityUsecase_OAuthLoginFallsBackToLegacyOAuthFields(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{},
		users: map[int64]*User{
			1: {ID: 1, Username: "legacy", Status: UserStatusEnabled, OAuthProvider: "github", OAuthID: "gh-1"},
		},
		oauthIdentities: map[string]*OAuthIdentity{},
	}
	uc := NewIdentityUsecase(repo)

	user, _, err := uc.OAuthLogin(context.Background(), "github", "gh-1", "ignored", "ignored@example.com", "Ignored")
	if err != nil {
		t.Fatalf("OAuthLogin() error = %v", err)
	}
	if user.ID != 1 || user.Username != "legacy" {
		t.Fatalf("OAuthLogin() user = %+v, want legacy", user)
	}
}

func (m *mockIdentityRepo) CreateToken(ctx context.Context, token *Token) error {
	token.ID = int64(len(m.tokens) + 1)
	m.tokens[token.Key] = token
	return nil
}

func (m *mockIdentityRepo) FindTokenByID(ctx context.Context, userID, tokenID int64) (*Token, error) {
	for _, token := range m.tokens {
		if token.ID == tokenID && token.UserID == userID {
			return token, nil
		}
	}
	return nil, ErrTokenNotFound
}

func (m *mockIdentityRepo) ListTokens(ctx context.Context, userID int64, page, pageSize int32, keyword string) ([]*Token, int64, error) {
	var result []*Token
	for _, token := range m.tokens {
		if token.UserID == userID {
			result = append(result, token)
		}
	}
	return result, int64(len(result)), nil
}

func (m *mockIdentityRepo) UpdateToken(ctx context.Context, token *Token) error {
	for key, existing := range m.tokens {
		if existing.ID == token.ID && existing.UserID == token.UserID {
			if key != token.Key {
				delete(m.tokens, key)
			}
			m.tokens[token.Key] = token
			return nil
		}
	}
	return ErrTokenNotFound
}

func (m *mockIdentityRepo) DeleteToken(ctx context.Context, userID, tokenID int64) error {
	for key, token := range m.tokens {
		if token.ID == tokenID && token.UserID == userID {
			delete(m.tokens, key)
			return nil
		}
	}
	return ErrTokenNotFound
}

func (m *mockIdentityRepo) ListUsers(ctx context.Context, page, pageSize int32, keyword, group string, status int32) ([]*User, int64, error) {
	var result []*User
	for _, u := range m.users {
		result = append(result, u)
	}
	return result, int64(len(result)), nil
}

func (m *mockIdentityRepo) CountUsers(ctx context.Context) (int64, error) {
	return int64(len(m.users)), nil
}

func TestIdentityUsecase_ValidateToken_ValidToken(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"valid-token": {
				ID:             1,
				UserID:         1,
				Key:            "valid-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{"gpt-4o-mini"},
			},
		},
		users: map[int64]*User{
			1: {
				ID:       1,
				Username: "test-user",
				Status:   UserStatusEnabled,
			},
		},
	}

	uc := NewIdentityUsecase(repo)
	token, err := uc.ValidateToken(context.Background(), "valid-token")
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}
	if token.ID != 1 {
		t.Fatalf("unexpected token ID: %d", token.ID)
	}
	if token.UserID != 1 {
		t.Fatalf("unexpected token user ID: %d", token.UserID)
	}
}

func TestIdentityUsecase_ValidateToken_EmptyToken(t *testing.T) {
	repo := &mockIdentityRepo{tokens: make(map[string]*Token), users: make(map[int64]*User)}
	uc := NewIdentityUsecase(repo)

	_, err := uc.ValidateToken(context.Background(), "")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken, got: %v", err)
	}

	_, err = uc.ValidateToken(context.Background(), "   ")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for whitespace token, got: %v", err)
	}
}

func TestIdentityUsecase_ValidateToken_TokenNotFound(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: make(map[string]*Token),
		users:  make(map[int64]*User),
	}
	uc := NewIdentityUsecase(repo)

	_, err := uc.ValidateToken(context.Background(), "nonexistent-token")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound, got: %v", err)
	}
}

func TestIdentityUsecase_ValidateToken_TokenExpired(t *testing.T) {
	expiredTime := time.Now().Add(-time.Hour).Unix()
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"expired-token": {
				ID:             1,
				UserID:         1,
				Key:            "expired-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      expiredTime,
				RemainQuota:    1000,
				UnlimitedQuota: false,
			},
		},
		users: map[int64]*User{
			1: {ID: 1, Username: "test-user", Status: UserStatusEnabled},
		},
	}

	uc := NewIdentityUsecase(repo)
	_, err := uc.ValidateToken(context.Background(), "expired-token")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got: %v", err)
	}
}

func TestIdentityUsecase_ValidateToken_TokenStatusExpired(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"expired-status-token": {
				ID:             1,
				UserID:         1,
				Key:            "expired-status-token",
				Status:         TokenStatusExpired,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
			},
		},
		users: map[int64]*User{
			1: {ID: 1, Username: "test-user", Status: UserStatusEnabled},
		},
	}

	uc := NewIdentityUsecase(repo)
	_, err := uc.ValidateToken(context.Background(), "expired-status-token")
	if !errors.Is(err, ErrTokenExpired) {
		t.Fatalf("expected ErrTokenExpired, got: %v", err)
	}
}

func TestIdentityUsecase_ValidateToken_TokenExhausted(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"exhausted-token": {
				ID:             1,
				UserID:         1,
				Key:            "exhausted-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    0,
				UnlimitedQuota: false,
			},
		},
		users: map[int64]*User{
			1: {ID: 1, Username: "test-user", Status: UserStatusEnabled},
		},
	}

	uc := NewIdentityUsecase(repo)
	_, err := uc.ValidateToken(context.Background(), "exhausted-token")
	if !errors.Is(err, ErrTokenExhausted) {
		t.Fatalf("expected ErrTokenExhausted, got: %v", err)
	}
}

func TestIdentityUsecase_ValidateToken_TokenStatusExhausted(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"exhausted-status-token": {
				ID:             1,
				UserID:         1,
				Key:            "exhausted-status-token",
				Status:         TokenStatusExhausted,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
			},
		},
		users: map[int64]*User{
			1: {ID: 1, Username: "test-user", Status: UserStatusEnabled},
		},
	}

	uc := NewIdentityUsecase(repo)
	_, err := uc.ValidateToken(context.Background(), "exhausted-status-token")
	if !errors.Is(err, ErrTokenExhausted) {
		t.Fatalf("expected ErrTokenExhausted, got: %v", err)
	}
}

func TestIdentityUsecase_ValidateToken_TokenDisabled(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"disabled-token": {
				ID:             1,
				UserID:         1,
				Key:            "disabled-token",
				Status:         TokenStatusDisabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
			},
		},
		users: map[int64]*User{
			1: {ID: 1, Username: "test-user", Status: UserStatusEnabled},
		},
	}

	uc := NewIdentityUsecase(repo)
	_, err := uc.ValidateToken(context.Background(), "disabled-token")
	if !errors.Is(err, ErrTokenDisabled) {
		t.Fatalf("expected ErrTokenDisabled, got: %v", err)
	}
}

func TestIdentityUsecase_ValidateToken_UnlimitedQuota(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"unlimited-token": {
				ID:             1,
				UserID:         1,
				Key:            "unlimited-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    0,
				UnlimitedQuota: true,
			},
		},
		users: map[int64]*User{
			1: {ID: 1, Username: "test-user", Status: UserStatusEnabled},
		},
	}

	uc := NewIdentityUsecase(repo)
	token, err := uc.ValidateToken(context.Background(), "unlimited-token")
	if err != nil {
		t.Fatalf("ValidateToken() unexpected error: %v", err)
	}
	if !token.UnlimitedQuota {
		t.Fatal("expected unlimited quota")
	}
}

func TestIdentityUsecase_GetAuthSnapshot_Valid(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"valid-token": {
				ID:             1,
				UserID:         1,
				Name:           "valid-token-name",
				Key:            "valid-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{"gpt-4o-mini", "gpt-4o"},
			},
		},
		users: map[int64]*User{
			1: {
				ID:          1,
				Username:    "test-user",
				DisplayName: "Test User",
				Group:       "default",
				Status:      UserStatusEnabled,
			},
		},
	}

	uc := NewIdentityUsecase(repo)
	snapshot, err := uc.GetAuthSnapshot(context.Background(), "valid-token")
	if err != nil {
		t.Fatalf("GetAuthSnapshot() error = %v", err)
	}
	if snapshot.UserID != 1 {
		t.Fatalf("unexpected user ID: %d", snapshot.UserID)
	}
	if snapshot.TokenID != 1 {
		t.Fatalf("unexpected token ID: %d", snapshot.TokenID)
	}
	if snapshot.TokenName != "valid-token-name" {
		t.Fatalf("unexpected token name: %q", snapshot.TokenName)
	}
	if snapshot.Group != "default" {
		t.Fatalf("unexpected group: %s", snapshot.Group)
	}
	if len(snapshot.AllowedModels) != 2 {
		t.Fatalf("unexpected models count: %d", len(snapshot.AllowedModels))
	}
	if !snapshot.UserEnabled {
		t.Fatal("expected user enabled")
	}
	if !snapshot.TokenEnabled {
		t.Fatal("expected token enabled")
	}
}

func TestIdentityUsecase_GetAuthSnapshot_UserDisabled(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"valid-token": {
				ID:             1,
				UserID:         1,
				Key:            "valid-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
			},
		},
		users: map[int64]*User{
			1: {
				ID:       1,
				Username: "test-user",
				Group:    "default",
				Status:   UserStatusDisabled,
			},
		},
	}

	uc := NewIdentityUsecase(repo)
	_, err := uc.GetAuthSnapshot(context.Background(), "valid-token")
	if !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("expected ErrUserDisabled, got: %v", err)
	}
}

func TestIdentityUsecase_GetAuthSnapshot_ModelWhitelist(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"restricted-token": {
				ID:             1,
				UserID:         1,
				Key:            "restricted-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{"gpt-4o-mini"},
			},
		},
		users: map[int64]*User{
			1: {
				ID:       1,
				Username: "test-user",
				Group:    "default",
				Status:   UserStatusEnabled,
			},
		},
	}

	uc := NewIdentityUsecase(repo)
	snapshot, err := uc.GetAuthSnapshot(context.Background(), "restricted-token")
	if err != nil {
		t.Fatalf("GetAuthSnapshot() error = %v", err)
	}
	if len(snapshot.AllowedModels) != 1 {
		t.Fatalf("expected 1 model, got: %d", len(snapshot.AllowedModels))
	}
	if snapshot.AllowedModels[0] != "gpt-4o-mini" {
		t.Fatalf("unexpected model: %s", snapshot.AllowedModels[0])
	}
}

func TestIdentityUsecase_GetAuthSnapshot_EmptyModelWhitelist(t *testing.T) {
	repo := &mockIdentityRepo{
		tokens: map[string]*Token{
			"all-models-token": {
				ID:             1,
				UserID:         1,
				Key:            "all-models-token",
				Status:         TokenStatusEnabled,
				ExpiredAt:      time.Now().Add(time.Hour).Unix(),
				RemainQuota:    1000,
				UnlimitedQuota: false,
				Models:         []string{},
			},
		},
		users: map[int64]*User{
			1: {
				ID:       1,
				Username: "test-user",
				Group:    "default",
				Status:   UserStatusEnabled,
			},
		},
	}

	uc := NewIdentityUsecase(repo)
	snapshot, err := uc.GetAuthSnapshot(context.Background(), "all-models-token")
	if err != nil {
		t.Fatalf("GetAuthSnapshot() error = %v", err)
	}
	if len(snapshot.AllowedModels) != 0 {
		t.Fatalf("expected empty models (no restrictions), got: %v", snapshot.AllowedModels)
	}
}

func TestSplitCSVPtr(t *testing.T) {
	tests := []struct {
		name     string
		input    *string
		expected []string
	}{
		{"nil input", nil, nil},
		{"empty string", strPtr(""), []string{}},
		{"single item", strPtr("gpt-4o-mini"), []string{"gpt-4o-mini"}},
		{"multiple items", strPtr("gpt-4o-mini,gpt-4o"), []string{"gpt-4o-mini", "gpt-4o"}},
		{"items with spaces", strPtr("gpt-4o-mini, gpt-4o, gpt-3.5-turbo"), []string{"gpt-4o-mini", "gpt-4o", "gpt-3.5-turbo"}},
		{"items with extra spaces", strPtr("  gpt-4o-mini  ,  gpt-4o  "), []string{"gpt-4o-mini", "gpt-4o"}},
		{"empty items", strPtr("gpt-4o-mini,,gpt-4o"), []string{"gpt-4o-mini", "gpt-4o"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SplitCSVPtr(tt.input)
			if !equalStringSlices(result, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"empty string", "", []string{}},
		{"single item", "gpt-4o-mini", []string{"gpt-4o-mini"}},
		{"multiple items", "gpt-4o-mini,gpt-4o", []string{"gpt-4o-mini", "gpt-4o"}},
		{"items with spaces", "gpt-4o-mini, gpt-4o, gpt-3.5-turbo", []string{"gpt-4o-mini", "gpt-4o", "gpt-3.5-turbo"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitCSV(tt.input)
			if !equalStringSlices(result, tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// ========== Login Tests ==========

func TestIdentityUsecase_Login_Success(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled, Group: "default", PasswordHash: "$2a$10$kn5rAQzaqKF4ncbcPWkllOMaoDBbTuwHgDJ6jobkei0tGyB8ICeWm"},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	user, token, err := uc.Login(context.Background(), "alice", "secret123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("unexpected username: %s", user.Username)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}
}

func TestIdentityUsecase_Login_EmptyCredentials(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	_, _, err := uc.Login(context.Background(), "", "secret")
	if !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected ErrInvalidPassword, got: %v", err)
	}

	_, _, err = uc.Login(context.Background(), "alice", "")
	if !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("expected ErrInvalidPassword, got: %v", err)
	}
}

func TestIdentityUsecase_Login_UserNotFound(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	_, _, err := uc.Login(context.Background(), "nobody", "secret")
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestIdentityUsecase_Login_UserDisabled(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusDisabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	_, _, err := uc.Login(context.Background(), "alice", "secret")
	if !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("expected ErrUserDisabled, got: %v", err)
	}
}

func TestIdentityUsecase_Login_CreatesToken(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled, PasswordHash: "$2a$10$kn5rAQzaqKF4ncbcPWkllOMaoDBbTuwHgDJ6jobkei0tGyB8ICeWm"},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	_, token, err := uc.Login(context.Background(), "alice", "secret123")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if len(repo.tokens) != 1 {
		t.Fatalf("expected 1 token created, got: %d", len(repo.tokens))
	}
	if repo.tokens[token] == nil {
		t.Fatal("token not found in repo")
	}
}

// ========== Register Tests ==========

func TestIdentityUsecase_Register_Success(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	user, err := uc.Register(context.Background(), "bob", "password123", "bob@example.com", "vip")
	if err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if user.Username != "bob" {
		t.Fatalf("unexpected username: %s", user.Username)
	}
	if user.Email != "bob@example.com" {
		t.Fatalf("unexpected email: %s", user.Email)
	}
	if user.Group != "vip" {
		t.Fatalf("unexpected group: %s", user.Group)
	}
	if user.Status != UserStatusEnabled {
		t.Fatalf("expected enabled status, got: %d", user.Status)
	}
	if user.AffCode == "" {
		t.Fatal("expected aff code to be generated")
	}
}

func TestIdentityUsecase_RegisterWithAffCode_SetsInviterID(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled, AffCode: "INVITE01"},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	user, err := uc.RegisterWithAffCode(context.Background(), "bob", "password123", "bob@example.com", "default", "INVITE01")
	if err != nil {
		t.Fatalf("RegisterWithAffCode() error = %v", err)
	}
	if user.InviterID != 1 {
		t.Fatalf("inviter id = %d, want 1", user.InviterID)
	}
	if user.AffCode == "" {
		t.Fatal("expected new user aff code")
	}
}

func TestIdentityUsecase_RegisterWithAffCode_InvalidCode(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	_, err := uc.RegisterWithAffCode(context.Background(), "bob", "password123", "bob@example.com", "default", "NONE")
	if err == nil || err.Error() != "invalid aff code" {
		t.Fatalf("expected invalid aff code, got: %v", err)
	}
}

func TestIdentityUsecase_GetOrCreateAffCode_ReturnsExisting(t *testing.T) {
	repo := &mockIdentityRepo{
		users:  map[int64]*User{1: {ID: 1, Username: "alice", Status: UserStatusEnabled, AffCode: "EXISTING"}},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	code, err := uc.GetOrCreateAffCode(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetOrCreateAffCode() error = %v", err)
	}
	if code != "EXISTING" {
		t.Fatalf("code = %q, want EXISTING", code)
	}
}

func TestIdentityUsecase_GetOrCreateAffCode_GeneratesWhenMissing(t *testing.T) {
	repo := &mockIdentityRepo{
		users:  map[int64]*User{1: {ID: 1, Username: "alice", Status: UserStatusEnabled}},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	code, err := uc.GetOrCreateAffCode(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetOrCreateAffCode() error = %v", err)
	}
	if code == "" {
		t.Fatal("expected generated code")
	}
	if repo.users[1].AffCode != code {
		t.Fatalf("persisted code = %q, want %q", repo.users[1].AffCode, code)
	}
}

func TestIdentityUsecase_RegisterWithAffCode_DoesNotApplyBonusesAtBizLayer(t *testing.T) {
	t.Setenv("INVITEE_BONUS_QUOTA", "25")
	t.Setenv("INVITER_BONUS_QUOTA", "50")
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled, AffCode: "INVITE01", Quota: 100},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	user, err := uc.RegisterWithAffCode(context.Background(), "bob", "password123", "bob@example.com", "default", "INVITE01")
	if err != nil {
		t.Fatalf("RegisterWithAffCode() error = %v", err)
	}
	if repo.users[1].Quota != 100 {
		t.Fatalf("inviter quota = %d, want unchanged 100 (bonus credit happens in billing, not biz)", repo.users[1].Quota)
	}
	if user.Quota != 0 {
		t.Fatalf("invitee quota at biz layer = %d, want 0 (bonus credit happens in billing, not biz)", user.Quota)
	}
	if user.InviterID != 1 {
		t.Fatalf("invitee InviterID = %d, want 1", user.InviterID)
	}
}

func TestIdentityUsecase_Register_UserExists(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	_, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if !errors.Is(err, ErrUserExists) {
		t.Fatalf("expected ErrUserExists, got: %v", err)
	}
}

// ========== CreateAccessToken Tests ==========

func TestIdentityUsecase_CreateAccessToken_Success(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	expireAt := time.Now().Add(time.Hour).Unix()
	token, err := uc.CreateAccessToken(context.Background(), 1, "work-token", []string{"gpt-4o"}, expireAt)
	if err != nil {
		t.Fatalf("CreateAccessToken() error = %v", err)
	}
	if token.UserID != 1 {
		t.Fatalf("unexpected user ID: %d", token.UserID)
	}
	if token.ExpiredAt != expireAt {
		t.Fatalf("unexpected expireAt: %d", token.ExpiredAt)
	}
	if len(token.Models) != 1 || token.Models[0] != "gpt-4o" {
		t.Fatalf("unexpected models: %v", token.Models)
	}
}

func TestIdentityUsecase_CreateAccessToken_UserNotFound(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	_, err := uc.CreateAccessToken(context.Background(), 999, "token", nil, 0)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestIdentityUsecase_CreateAccessToken_UserDisabled(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusDisabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	_, err := uc.CreateAccessToken(context.Background(), 1, "token", nil, 0)
	if !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("expected ErrUserDisabled, got: %v", err)
	}
}

// ========== ListUsers Tests ==========

func TestIdentityUsecase_ListUsers_Success(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled, Group: "default"},
			2: {ID: 2, Username: "bob", Status: UserStatusEnabled, Group: "vip"},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	users, total, err := uc.ListUsers(context.Background(), 1, 10, "", "", 0)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if total != 2 {
		t.Fatalf("expected total 2, got: %d", total)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got: %d", len(users))
	}
}

func TestIdentityUsecase_ListUsers_Empty(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	users, total, err := uc.ListUsers(context.Background(), 1, 10, "", "", 0)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if total != 0 {
		t.Fatalf("expected total 0, got: %d", total)
	}
	if len(users) != 0 {
		t.Fatalf("expected 0 users, got: %d", len(users))
	}
}

// ========== CreateUser Tests ==========

func TestIdentityUsecase_CreateUser_Success(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	user, err := uc.CreateUser(context.Background(), "alice", "Alice User", "alice@example.com", "secret", "vip", 1000)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("unexpected username: %s", user.Username)
	}
	if user.DisplayName != "Alice User" {
		t.Fatalf("unexpected display name: %s", user.DisplayName)
	}
	if user.Email != "alice@example.com" {
		t.Fatalf("unexpected email: %s", user.Email)
	}
	if user.Group != "vip" {
		t.Fatalf("unexpected group: %s", user.Group)
	}
}

func TestIdentityUsecase_CreateUser_AlreadyExists(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	_, err := uc.CreateUser(context.Background(), "alice", "", "", "", "", 0)
	if !errors.Is(err, ErrUserExists) {
		t.Fatalf("expected ErrUserExists, got: %v", err)
	}
}

// ========== UpdateUser Tests ==========

func TestIdentityUsecase_UpdateUser_Success(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", DisplayName: "Old Name", Group: "default", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	err := uc.UpdateUser(context.Background(), 1, "New Name", "alice@example.com", "vip", UserStatusDisabled)
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if repo.users[1].DisplayName != "New Name" {
		t.Fatalf("unexpected display name: %s", repo.users[1].DisplayName)
	}
	if repo.users[1].Email != "alice@example.com" {
		t.Fatalf("unexpected email: %s", repo.users[1].Email)
	}
	if repo.users[1].Group != "vip" {
		t.Fatalf("unexpected group: %s", repo.users[1].Group)
	}
	if repo.users[1].Status != UserStatusDisabled {
		t.Fatalf("unexpected status: %d", repo.users[1].Status)
	}
}

func TestIdentityUsecase_UpdateUser_NotFound(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	err := uc.UpdateUser(context.Background(), 999, "name", "", "", 0)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestIdentityUsecase_UpdateUser_PartialUpdate(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", DisplayName: "Old Name", Email: "old@example.com", Group: "default", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	// Only update display name, leave others unchanged
	err := uc.UpdateUser(context.Background(), 1, "New Name", "", "", UserStatusEnabled)
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if repo.users[1].DisplayName != "New Name" {
		t.Fatalf("display name not updated: %s", repo.users[1].DisplayName)
	}
	if repo.users[1].Email != "old@example.com" {
		t.Fatalf("email changed unexpectedly: %s", repo.users[1].Email)
	}
}

func TestIdentityUsecase_UpdateSelf_UpdatesProfile(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", DisplayName: "Old Name", Email: "alice@example.com", Group: "default", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	err := uc.UpdateSelf(context.Background(), 1, "alice2", "Alice Two", "")
	if err != nil {
		t.Fatalf("UpdateSelf() error = %v", err)
	}
	if repo.users[1].Username != "alice2" {
		t.Fatalf("username = %s, want alice2", repo.users[1].Username)
	}
	if repo.users[1].DisplayName != "Alice Two" {
		t.Fatalf("display name = %s, want Alice Two", repo.users[1].DisplayName)
	}
	if repo.users[1].Email != "alice@example.com" || repo.users[1].Group != "default" {
		t.Fatalf("unexpected preserved fields: email=%s group=%s", repo.users[1].Email, repo.users[1].Group)
	}
}

func TestIdentityUsecase_UpdateSelf_RejectsDuplicateUsername(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", DisplayName: "Alice", Status: UserStatusEnabled},
			2: {ID: 2, Username: "bob", DisplayName: "Bob", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	err := uc.UpdateSelf(context.Background(), 1, "bob", "", "")
	if !errors.Is(err, ErrUserExists) {
		t.Fatalf("expected ErrUserExists, got: %v", err)
	}
}

func TestIdentityUsecase_UpdateSelf_UpdatesPassword(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	user, err := uc.Register(context.Background(), "alice", "password123", "alice@example.com", "default")
	if err != nil {
		t.Fatal(err)
	}

	if err := uc.UpdateSelf(context.Background(), user.ID, "", "", "newpass123"); err != nil {
		t.Fatalf("UpdateSelf() error = %v", err)
	}
	if _, _, err := uc.Login(context.Background(), "alice", "newpass123"); err != nil {
		t.Fatalf("login with new password failed: %v", err)
	}
}

func TestIdentityUsecase_UpdateSelfEmail_UpdatesEmail(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", DisplayName: "Alice", Email: "old@example.com", Group: "default", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	if err := uc.UpdateSelfEmail(context.Background(), 1, "new@example.com"); err != nil {
		t.Fatalf("UpdateSelfEmail() error = %v", err)
	}
	if repo.users[1].Email != "new@example.com" {
		t.Fatalf("email = %s, want new@example.com", repo.users[1].Email)
	}
}

func TestIdentityUsecase_UpdateSelfEmail_PreservesOtherFields(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", DisplayName: "Alice", Email: "old@example.com", Group: "vip", Status: UserStatusDisabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)

	if err := uc.UpdateSelfEmail(context.Background(), 1, "new@example.com"); err != nil {
		t.Fatalf("UpdateSelfEmail() error = %v", err)
	}
	if repo.users[1].Username != "alice" || repo.users[1].DisplayName != "Alice" || repo.users[1].Group != "vip" || repo.users[1].Status != UserStatusDisabled {
		t.Fatalf("unexpected preserved fields: %+v", repo.users[1])
	}
}

// ========== DeleteUser Tests ==========

func TestIdentityUsecase_DeleteUser_Success(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	err := uc.DeleteUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	if len(repo.users) != 0 {
		t.Fatalf("expected 0 users, got: %d", len(repo.users))
	}
}

func TestIdentityUsecase_DeleteUser_NotFound(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	// DeleteUser mock doesn't return error for not found
	err := uc.DeleteUser(context.Background(), 999)
	if err != nil {
		t.Fatalf("DeleteUser() unexpected error: %v", err)
	}
}

// ========== GetUser Tests ==========

func TestIdentityUsecase_GetUser_Success(t *testing.T) {
	repo := &mockIdentityRepo{
		users: map[int64]*User{
			1: {ID: 1, Username: "alice", Status: UserStatusEnabled},
		},
		tokens: make(map[string]*Token),
	}
	uc := NewIdentityUsecase(repo)
	user, err := uc.GetUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("unexpected username: %s", user.Username)
	}
}

func TestIdentityUsecase_GetUser_NotFound(t *testing.T) {
	repo := &mockIdentityRepo{users: make(map[int64]*User), tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)
	_, err := uc.GetUser(context.Background(), 999)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestIdentityUsecase_SetRole_PromoteCommonToAdmin(t *testing.T) {
	repo := &mockIdentityRepo{users: map[int64]*User{
		7: {ID: 7, Username: "alice", Role: RoleCommonUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	got, err := uc.SetRole(context.Background(), nil, 7, RoleAdminUser)
	if err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if got.Role != RoleAdminUser {
		t.Fatalf("returned role = %d, want %d", got.Role, RoleAdminUser)
	}
	if !got.IsAdmin() {
		t.Fatalf("IsAdmin() should be true after promotion")
	}
	if repo.users[7].Role != RoleAdminUser {
		t.Fatalf("repo role = %d, want %d", repo.users[7].Role, RoleAdminUser)
	}
}

func TestIdentityUsecase_SetRole_DemoteAdminToCommon(t *testing.T) {
	repo := &mockIdentityRepo{users: map[int64]*User{
		7: {ID: 7, Username: "alice", Role: RoleAdminUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	got, err := uc.SetRole(context.Background(), nil, 7, RoleCommonUser)
	if err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if got.Role != RoleCommonUser || got.IsAdmin() {
		t.Fatalf("demote did not clear admin role: got %+v", got)
	}
}

func TestIdentityUsecase_SetRole_RootCannotBeChanged(t *testing.T) {
	repo := &mockIdentityRepo{users: map[int64]*User{
		1: {ID: 1, Username: "admin", Role: RoleRootUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.SetRole(context.Background(), nil, 1, RoleCommonUser); !errors.Is(err, ErrCannotChangeRootRole) {
		t.Fatalf("expected ErrCannotChangeRootRole, got %v", err)
	}
	if repo.users[1].Role != RoleRootUser {
		t.Fatalf("root role mutated to %d", repo.users[1].Role)
	}
}

func TestIdentityUsecase_SetRole_RejectsRootValueAndInvalid(t *testing.T) {
	repo := &mockIdentityRepo{users: map[int64]*User{
		7: {ID: 7, Username: "alice", Role: RoleCommonUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	// Granting root via SetRole is not allowed — root is only set by bootstrap.
	if _, err := uc.SetRole(context.Background(), nil, 7, RoleRootUser); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole for root, got %v", err)
	}
	if _, err := uc.SetRole(context.Background(), nil, 7, 42); !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("expected ErrInvalidRole for 42, got %v", err)
	}
}

func TestIdentityUsecase_SetRole_OperatorMustBeAdmin(t *testing.T) {
	common := &User{ID: 5, Username: "bob", Role: RoleCommonUser, Status: UserStatusEnabled}
	repo := &mockIdentityRepo{users: map[int64]*User{
		7: {ID: 7, Username: "alice", Role: RoleCommonUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.SetRole(context.Background(), common, 7, RoleAdminUser); !errors.Is(err, ErrOperatorNotAdmin) {
		t.Fatalf("expected ErrOperatorNotAdmin, got %v", err)
	}
}

func TestIdentityUsecase_SetRole_OperatorCannotChangeSelf(t *testing.T) {
	op := &User{ID: 9, Username: "ops", Role: RoleAdminUser, Status: UserStatusEnabled}
	repo := &mockIdentityRepo{users: map[int64]*User{
		9: op,
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.SetRole(context.Background(), op, 9, RoleCommonUser); !errors.Is(err, ErrCannotChangeSelf) {
		t.Fatalf("expected ErrCannotChangeSelf, got %v", err)
	}
}

func TestIdentityUsecase_SetRole_OperatorCannotOutrankTarget(t *testing.T) {
	// Admin tries to demote another admin → both at role 10, operator
	// does not strictly outrank, so this must be refused.
	op := &User{ID: 9, Username: "ops", Role: RoleAdminUser, Status: UserStatusEnabled}
	repo := &mockIdentityRepo{users: map[int64]*User{
		8: {ID: 8, Username: "peer", Role: RoleAdminUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.SetRole(context.Background(), op, 8, RoleCommonUser); !errors.Is(err, ErrCannotOutrankOperator) {
		t.Fatalf("expected ErrCannotOutrankOperator, got %v", err)
	}
}

func TestIdentityUsecase_SetRole_OperatorCannotPromoteToOwnRole(t *testing.T) {
	// Admin tries to promote a common user to admin (its own rank) →
	// refused: new role must be strictly below operator role.
	op := &User{ID: 9, Username: "ops", Role: RoleAdminUser, Status: UserStatusEnabled}
	repo := &mockIdentityRepo{users: map[int64]*User{
		7: {ID: 7, Username: "alice", Role: RoleCommonUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	if _, err := uc.SetRole(context.Background(), op, 7, RoleAdminUser); !errors.Is(err, ErrCannotOutrankOperator) {
		t.Fatalf("expected ErrCannotOutrankOperator, got %v", err)
	}
}

func TestIdentityUsecase_SetRole_RootCanPromoteAdmin(t *testing.T) {
	root := &User{ID: 1, Username: "root", Role: RoleRootUser, Status: UserStatusEnabled}
	repo := &mockIdentityRepo{users: map[int64]*User{
		7: {ID: 7, Username: "alice", Role: RoleCommonUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	got, err := uc.SetRole(context.Background(), root, 7, RoleAdminUser)
	if err != nil {
		t.Fatalf("root should be able to promote common → admin: %v", err)
	}
	if got.Role != RoleAdminUser {
		t.Fatalf("returned role = %d, want %d", got.Role, RoleAdminUser)
	}
}

func TestIdentityUsecase_SetRole_AdminCanDemoteCommonUserNoOp(t *testing.T) {
	// Admin demoting a common user to common is a no-op but should
	// succeed (defensive: avoids 500s on idempotent UI clicks).
	op := &User{ID: 9, Username: "ops", Role: RoleAdminUser, Status: UserStatusEnabled}
	repo := &mockIdentityRepo{users: map[int64]*User{
		7: {ID: 7, Username: "alice", Role: RoleCommonUser, Status: UserStatusEnabled},
	}, tokens: make(map[string]*Token)}
	uc := NewIdentityUsecase(repo)

	got, err := uc.SetRole(context.Background(), op, 7, RoleCommonUser)
	if err != nil {
		t.Fatalf("admin should be able to set common→common (no-op): %v", err)
	}
	if got.Role != RoleCommonUser {
		t.Fatalf("role = %d, want %d", got.Role, RoleCommonUser)
	}
}
