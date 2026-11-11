package biz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundOAuthProviders(t *testing.T) {
	for _, tc := range []struct {
		name       string
		user       *User
		identities map[string]*OAuthIdentity
		want       []string
	}{
		{"unbound", &User{ID: 1}, nil, []string{}},
		{"stored binding", &User{ID: 1}, map[string]*OAuthIdentity{"github:subject": {UserID: 1, Provider: "github"}}, []string{"github"}},
		{"original user binding", &User{ID: 1, OAuthProvider: "github", OAuthID: "subject"}, nil, []string{"github"}},
		{"other user", &User{ID: 1}, map[string]*OAuthIdentity{"github:other": {UserID: 2, Provider: "github"}}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := NewIdentityUsecase(&mockIdentityRepo{oauthIdentities: tc.identities}, nil)
			got, err := uc.BoundOAuthProviders(context.Background(), tc.user, []string{"github", "wechat"})
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
	uc := NewIdentityUsecase(&mockIdentityRepo{}, nil)
	_, err := uc.BoundOAuthProviders(context.Background(), nil, []string{"github"})
	require.ErrorIs(t, err, ErrUserNotFound)
}
