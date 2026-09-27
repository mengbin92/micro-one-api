package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIdentityUsecase_NewUserRoleRoundTrip(t *testing.T) {
	ctx := context.Background()
	for _, entry := range []string{"register", "invitation", "admin", "oauth"} {
		t.Run(entry, func(t *testing.T) {
			operator := &User{ID: 1, Username: "root", Role: RoleRootUser, Status: UserStatusEnabled, AffCode: "INVITE01"}
			repo := &mockIdentityRepo{
				users:  map[int64]*User{operator.ID: operator},
				tokens: map[string]*Token{},
			}
			uc := NewIdentityUsecase(repo, nil)
			var user *User
			var err error
			switch entry {
			case "register":
				user, err = uc.Register(ctx, "alice", "password123", "alice@example.com", "default")
			case "invitation":
				user, err = uc.RegisterWithAffCode(ctx, "alice", "password123", "alice@example.com", "default", operator.AffCode)
			case "admin":
				user, err = uc.CreateUser(ctx, "alice", "Alice", "alice@example.com", "password123", "default", 0)
			case "oauth":
				var created bool
				user, _, created, err = uc.OAuthLogin(ctx, "github", "gh-alice", "alice", "alice@example.com", "Alice")
				require.True(t, created)
			}
			require.NoError(t, err)
			initialRole := user.Role
			if initialRole != RoleCommonUser {
				t.Errorf("new user role = %d, want %d (common user)", initialRole, RoleCommonUser)
			}
			stored, err := repo.FindUserByID(ctx, user.ID)
			require.NoError(t, err)
			if stored.Role != RoleCommonUser {
				t.Errorf("persisted user role = %d, want %d (common user)", stored.Role, RoleCommonUser)
			}

			promoted, err := uc.SetRole(ctx, operator, user.ID, RoleAdminUser)
			require.NoError(t, err)
			require.Equal(t, RoleAdminUser, promoted.Role)
			demoted, err := uc.SetRole(ctx, operator, user.ID, RoleCommonUser)
			require.NoError(t, err)
			require.Equal(t, RoleCommonUser, demoted.Role)
			require.Equal(t, initialRole, demoted.Role, "demotion must restore the new user's original role")
		})
	}
}

func TestIdentityUsecase_OAuthLoginPreservesExistingRole(t *testing.T) {
	for _, role := range []int32{RoleGuestUser, RoleCommonUser, RoleAdminUser, RoleRootUser} {
		repo := &mockIdentityRepo{
			users: map[int64]*User{1: {ID: 1, Username: "alice", Role: role, Status: UserStatusEnabled}},
			oauthIdentities: map[string]*OAuthIdentity{
				"github:gh-alice": {UserID: 1, Provider: "github", ProviderID: "gh-alice"},
			},
		}
		uc := NewIdentityUsecase(repo, nil)
		user, _, created, err := uc.OAuthLogin(context.Background(), "github", "gh-alice", "alice", "", "")
		require.NoError(t, err)
		require.False(t, created)
		require.Equal(t, role, user.Role, "existing user's role must survive login")
	}
}
