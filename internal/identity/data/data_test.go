package data

import (
	"context"
	"testing"

	"micro-one-api/internal/identity/biz"
)

// newTestRepo creates an in-memory repository for testing.
func newTestRepo() *Repository {
	return &Repository{
		usersByID:           make(map[int64]*biz.User),
		tokensByKey:         make(map[string]*biz.Token),
		oauthIdentities:     make(map[string]*biz.OAuthIdentity),
		nextOAuthIdentityID: 1,
	}
}

// ========== FindUserByID Tests ==========

func TestFindUserByID_Success(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", Status: biz.UserStatusEnabled}

	user, err := repo.FindUserByID(context.Background(), 1)
	if err != nil {
		t.Fatalf("FindUserByID() error = %v", err)
	}
	if user.Username != "alice" {
		t.Fatalf("unexpected username: %s", user.Username)
	}
}

func TestFindUserByID_NotFound(t *testing.T) {
	repo := newTestRepo()
	_, err := repo.FindUserByID(context.Background(), 999)
	if err != biz.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestFindUserByID_ReturnsClonedUser(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", DisplayName: "Alice"}

	user1, _ := repo.FindUserByID(context.Background(), 1)
	user1.DisplayName = "Modified"
	user2, _ := repo.FindUserByID(context.Background(), 1)
	if user2.DisplayName == "Modified" {
		t.Fatal("expected original user to be unaffected by mutation")
	}
}

// ========== FindUserByUsername Tests ==========

func TestFindUserByUsername_Success(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", Status: biz.UserStatusEnabled}

	user, err := repo.FindUserByUsername(context.Background(), "alice")
	if err != nil {
		t.Fatalf("FindUserByUsername() error = %v", err)
	}
	if user.ID != 1 {
		t.Fatalf("unexpected user ID: %d", user.ID)
	}
}

func TestFindUserByUsername_NotFound(t *testing.T) {
	repo := newTestRepo()
	_, err := repo.FindUserByUsername(context.Background(), "nobody")
	if err != biz.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

// ========== FindUserByAffCode Tests ==========

func TestFindUserByAffCode_Success(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", AffCode: "AFF12345", Status: biz.UserStatusEnabled}

	user, err := repo.FindUserByAffCode(context.Background(), "AFF12345")
	if err != nil {
		t.Fatalf("FindUserByAffCode() error = %v", err)
	}
	if user.ID != 1 {
		t.Fatalf("unexpected user ID: %d", user.ID)
	}
}

func TestFindUserByAffCode_NotFound(t *testing.T) {
	repo := newTestRepo()
	_, err := repo.FindUserByAffCode(context.Background(), "NONE")
	if err != biz.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

func TestOAuthIdentityMemoryRepositoryCreatesAndFindsIdentity(t *testing.T) {
	repo := newTestRepo()
	identity := &biz.OAuthIdentity{UserID: 1, Provider: "github", ProviderID: "gh-1"}

	if err := repo.CreateOAuthIdentity(context.Background(), identity); err != nil {
		t.Fatalf("CreateOAuthIdentity() error = %v", err)
	}
	if identity.ID == 0 {
		t.Fatal("expected identity ID to be assigned")
	}
	found, err := repo.FindOAuthIdentity(context.Background(), "github", "gh-1")
	if err != nil {
		t.Fatalf("FindOAuthIdentity() error = %v", err)
	}
	if found.UserID != 1 {
		t.Fatalf("identity user id = %d, want 1", found.UserID)
	}
	byUser, err := repo.FindOAuthIdentityByUserProvider(context.Background(), 1, "github")
	if err != nil {
		t.Fatalf("FindOAuthIdentityByUserProvider() error = %v", err)
	}
	if byUser.ProviderID != "gh-1" {
		t.Fatalf("identity provider id = %q, want gh-1", byUser.ProviderID)
	}
}

func TestOAuthIdentityMemoryRepositoryRejectsDuplicateIdentity(t *testing.T) {
	repo := newTestRepo()
	if err := repo.CreateOAuthIdentity(context.Background(), &biz.OAuthIdentity{UserID: 1, Provider: "oidc", ProviderID: "sub-1"}); err != nil {
		t.Fatalf("CreateOAuthIdentity() error = %v", err)
	}
	if err := repo.CreateOAuthIdentity(context.Background(), &biz.OAuthIdentity{UserID: 2, Provider: "oidc", ProviderID: "sub-1"}); err == nil {
		t.Fatal("CreateOAuthIdentity() expected duplicate provider identity error")
	}
}

func TestOAuthIdentityMemoryRepositoryRejectsDuplicateUserProvider(t *testing.T) {
	repo := newTestRepo()
	if err := repo.CreateOAuthIdentity(context.Background(), &biz.OAuthIdentity{UserID: 1, Provider: "wechat", ProviderID: "openid-1"}); err != nil {
		t.Fatalf("CreateOAuthIdentity() error = %v", err)
	}
	if err := repo.CreateOAuthIdentity(context.Background(), &biz.OAuthIdentity{UserID: 1, Provider: "wechat", ProviderID: "openid-2"}); err == nil {
		t.Fatal("CreateOAuthIdentity() expected duplicate user provider error")
	}
}

// ========== CreateUser Tests ==========

func TestCreateUser_Success(t *testing.T) {
	repo := newTestRepo()
	user := &biz.User{Username: "alice", Status: biz.UserStatusEnabled}
	err := repo.CreateUser(context.Background(), user)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.ID == 0 {
		t.Fatal("expected user ID to be assigned")
	}
	if len(repo.usersByID) != 1 {
		t.Fatalf("expected 1 user, got: %d", len(repo.usersByID))
	}
}

func TestCreateUser_IDAssigned(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "existing"}

	user := &biz.User{Username: "alice", Status: biz.UserStatusEnabled}
	repo.CreateUser(context.Background(), user)
	if user.ID != 2 {
		t.Fatalf("expected ID=2, got: %d", user.ID)
	}
}

// ========== UpdateUser Tests ==========

func TestUpdateUser_Success(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", DisplayName: "Old Name", Status: biz.UserStatusEnabled}

	updated := &biz.User{ID: 1, Username: "alice", DisplayName: "New Name", Status: biz.UserStatusDisabled}
	err := repo.UpdateUser(context.Background(), updated)
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if repo.usersByID[1].DisplayName != "New Name" {
		t.Fatalf("display name not updated: %s", repo.usersByID[1].DisplayName)
	}
	if repo.usersByID[1].Status != biz.UserStatusDisabled {
		t.Fatalf("status not updated: %d", repo.usersByID[1].Status)
	}
}

func TestUpdateUser_PersistsAffFields(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", Status: biz.UserStatusEnabled}

	updated := &biz.User{ID: 1, Username: "alice", Status: biz.UserStatusEnabled, AffCode: "AFF12345", InviterID: 99}
	err := repo.UpdateUser(context.Background(), updated)
	if err != nil {
		t.Fatalf("UpdateUser() error = %v", err)
	}
	if repo.usersByID[1].AffCode != "AFF12345" {
		t.Fatalf("aff code not updated: %s", repo.usersByID[1].AffCode)
	}
	if repo.usersByID[1].InviterID != 99 {
		t.Fatalf("inviter id not updated: %d", repo.usersByID[1].InviterID)
	}
}

func TestUpdateUser_NotFound(t *testing.T) {
	repo := newTestRepo()
	updated := &biz.User{ID: 999, Username: "nobody"}
	err := repo.UpdateUser(context.Background(), updated)
	if err != biz.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

// ========== DeleteUser Tests ==========

func TestDeleteUser_Success(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice"}

	err := repo.DeleteUser(context.Background(), 1)
	if err != nil {
		t.Fatalf("DeleteUser() error = %v", err)
	}
	if len(repo.usersByID) != 0 {
		t.Fatalf("expected 0 users, got: %d", len(repo.usersByID))
	}
}

func TestDeleteUser_NotFound(t *testing.T) {
	repo := newTestRepo()
	err := repo.DeleteUser(context.Background(), 999)
	if err != biz.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

// ========== IncreaseUserQuota Tests ==========

func TestIncreaseUserQuota_Success(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice"}

	if err := repo.IncreaseUserQuota(context.Background(), 1, 250); err != nil {
		t.Fatalf("IncreaseUserQuota() error = %v", err)
	}
	if repo.usersByID[1].Quota != 250 {
		t.Fatalf("quota = %d, want 250", repo.usersByID[1].Quota)
	}
}

func TestIncreaseUserQuota_NotFound(t *testing.T) {
	repo := newTestRepo()
	err := repo.IncreaseUserQuota(context.Background(), 999, 250)
	if err != biz.ErrUserNotFound {
		t.Fatalf("expected ErrUserNotFound, got: %v", err)
	}
}

// ========== FindTokenByKey Tests ==========

func TestFindTokenByKey_Success(t *testing.T) {
	repo := newTestRepo()
	repo.tokensByKey["test-key"] = &biz.Token{
		ID:             1,
		Key:            "test-key",
		UserID:         1,
		Status:         biz.TokenStatusEnabled,
		RemainQuota:    1000,
		UnlimitedQuota: false,
	}

	token, err := repo.FindTokenByKey(context.Background(), "test-key")
	if err != nil {
		t.Fatalf("FindTokenByKey() error = %v", err)
	}
	if token.UserID != 1 {
		t.Fatalf("unexpected user ID: %d", token.UserID)
	}
}

func TestFindTokenByKey_NotFound(t *testing.T) {
	repo := newTestRepo()
	_, err := repo.FindTokenByKey(context.Background(), "nonexistent")
	if err != biz.ErrTokenNotFound {
		t.Fatalf("expected ErrTokenNotFound, got: %v", err)
	}
}

func TestFindTokenByKey_ReturnsClonedToken(t *testing.T) {
	repo := newTestRepo()
	repo.tokensByKey["key"] = &biz.Token{ID: 1, Key: "key", RemainQuota: 100}

	t1, _ := repo.FindTokenByKey(context.Background(), "key")
	t1.RemainQuota = 0
	t2, _ := repo.FindTokenByKey(context.Background(), "key")
	if t2.RemainQuota == 0 {
		t.Fatal("expected original token to be unaffected by mutation")
	}
}

// ========== CreateToken Tests ==========

func TestCreateToken_Success(t *testing.T) {
	repo := newTestRepo()
	token := &biz.Token{Key: "new-token", UserID: 1, Status: biz.TokenStatusEnabled}
	err := repo.CreateToken(context.Background(), token)
	if err != nil {
		t.Fatalf("CreateToken() error = %v", err)
	}
	if token.ID == 0 {
		t.Fatal("expected token ID to be assigned")
	}
	if len(repo.tokensByKey) != 1 {
		t.Fatalf("expected 1 token, got: %d", len(repo.tokensByKey))
	}
}

func TestCreateToken_IDAssigned(t *testing.T) {
	repo := newTestRepo()
	repo.tokensByKey["k1"] = &biz.Token{ID: 1, Key: "k1"}

	token := &biz.Token{Key: "k2", UserID: 1, Status: biz.TokenStatusEnabled}
	repo.CreateToken(context.Background(), token)
	if token.ID != 2 {
		t.Fatalf("expected ID=2, got: %d", token.ID)
	}
}

// ========== ListUsers Tests ==========

func TestListUsers_All(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", Group: "default", Status: biz.UserStatusEnabled}
	repo.usersByID[2] = &biz.User{ID: 2, Username: "bob", Group: "vip", Status: biz.UserStatusEnabled}

	users, total, err := repo.ListUsers(context.Background(), 1, 10, "", "", 0)
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

func TestListUsers_FilterByGroup(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", Group: "default", Status: biz.UserStatusEnabled}
	repo.usersByID[2] = &biz.User{ID: 2, Username: "bob", Group: "vip", Status: biz.UserStatusEnabled}

	users, total, _ := repo.ListUsers(context.Background(), 1, 10, "", "vip", 0)
	if total != 1 {
		t.Fatalf("expected total 1, got: %d", total)
	}
	if users[0].Username != "bob" {
		t.Fatalf("expected bob, got: %s", users[0].Username)
	}
}

func TestListUsers_FilterByStatus(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", Status: biz.UserStatusEnabled}
	repo.usersByID[2] = &biz.User{ID: 2, Username: "bob", Status: biz.UserStatusDisabled}

	users, total, _ := repo.ListUsers(context.Background(), 1, 10, "", "", biz.UserStatusDisabled)
	if total != 1 {
		t.Fatalf("expected total 1, got: %d", total)
	}
	if users[0].Username != "bob" {
		t.Fatalf("expected bob, got: %s", users[0].Username)
	}
}

func TestListUsers_FilterByKeyword(t *testing.T) {
	repo := newTestRepo()
	repo.usersByID[1] = &biz.User{ID: 1, Username: "alice", Status: biz.UserStatusEnabled}
	repo.usersByID[2] = &biz.User{ID: 2, Username: "alex", Status: biz.UserStatusEnabled}

	users, total, _ := repo.ListUsers(context.Background(), 1, 10, "ali", "", 0)
	if total != 1 {
		t.Fatalf("expected total 1, got: %d", total)
	}
	if users[0].Username != "alice" {
		t.Fatalf("expected alice, got: %s", users[0].Username)
	}
}

func TestListUsers_Empty(t *testing.T) {
	repo := newTestRepo()
	users, total, err := repo.ListUsers(context.Background(), 1, 10, "", "", 0)
	if err != nil {
		t.Fatalf("ListUsers() error = %v", err)
	}
	if total != 0 || len(users) != 0 {
		t.Fatalf("expected empty result")
	}
}
