package biz

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/domain/routing"
	"testing"
)

type registrationRoutingReader struct{ key string }

func (r *registrationRoutingReader) FindRoutingGroup(_ context.Context, key string) (*routing.Group, error) {
	r.key = key
	return &routing.Group{ID: 7, Key: key, Status: "enabled"}, nil
}

func TestRoutingRegistrationUsesServerDefault(t *testing.T) {
	t.Setenv("IDENTITY_ROUTING_V2", "true")
	t.Setenv("IDENTITY_DEFAULT_ROUTING_GROUP", "configured-default")
	repo := &mockIdentityRepo{users: map[int64]*User{}, tokens: map[string]*Token{}}
	uc := NewIdentityUsecase(repo, nil)
	reader := &registrationRoutingReader{}
	uc.SetRoutingGroupReader(reader)
	user, err := uc.Register(context.Background(), "new-user", "password123", "user@example.invalid", "client-selected-vip")
	require.NoError(t, err)
	require.Equal(t, "configured-default", reader.key)
	require.Equal(t, "configured-default", user.Group)
	require.EqualValues(t, 7, user.DefaultRoutingGroupID)
}
