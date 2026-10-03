package integration

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	identityv1 "micro-one-api/api/identity/v1"
	identitytest "micro-one-api/app/identity/testutil"
	dbtest "micro-one-api/platform/database/testutil"
)

func TestIAMCLegacyAuthorizationHTTP(t *testing.T) {
	t.Setenv("JWT_SECRET_KEY", "c-legacy-isolated")
	t.Setenv("INITIAL_ADMIN_PASSWORD", "c-legacy-root-password")
	t.Setenv("IDENTITY_ROUTING_V2", "false")
	db := dbtest.RoutingContextDB(t, "sqlite")
	identity, _, selfHTTP := identitytest.NewIAMStack(db)
	_, err := identity.EnsureRootAdmin(context.Background())
	require.NoError(t, err)
	member, err := identity.Register(context.Background(), "c-member", "password123", "c-member@example.com", "default")
	require.NoError(t, err)
	call := func(userID int64) *httptest.ResponseRecorder {
		raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"user_id": userID, "role": 100, "token_type": "user_session", "pwd_epoch": 0,
			"jti": fmt.Sprintf("legacy-%d", userID), "sub": fmt.Sprint(userID),
			"iss": "micro-one-api", "aud": "micro-one-api-web", "exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString([]byte("c-legacy-isolated"))
		require.NoError(t, err)
		req := httptest.NewRequest("GET", "/api/user/authorization?context.context_type=platform&context.context_key=platform", nil)
		req.Header.Set("Authorization", "Bearer "+raw)
		response := httptest.NewRecorder()
		selfHTTP.ServeHTTP(response, req)
		return response
	}
	var before, after int64
	require.NoError(t, db.Table("iam_sessions").Count(&before).Error)
	for _, row := range []struct {
		id    int64
		admin bool
	}{{1, true}, {member.ID, false}} {
		response := call(row.id)
		require.Equal(t, 200, response.Code, response.Body.String())
		var reply identityv1.IAMReply
		require.NoError(t, protojson.Unmarshal(response.Body.Bytes(), &reply))
		require.Equal(t, "legacy", reply.AuthorizationMode)
		require.Equal(t, row.admin, reply.LegacyAdmin, "numeric JWT role never replaces the database user")
		require.Empty(t, reply.PermittedOperations)
		require.Empty(t, reply.Sources)
		require.True(t, reply.ValidUntil.AsTime().After(time.Now()))
	}
	require.NoError(t, db.Table("iam_sessions").Count(&after).Error)
	require.Equal(t, before, after, "legacy display does not activate candidate IAM sessions")
	require.NoError(t, db.Table("users").Where("id=1").Update("role", 1).Error)
	response := call(1)
	require.Equal(t, 200, response.Code, response.Body.String())
	var reply identityv1.IAMReply
	require.NoError(t, protojson.Unmarshal(response.Body.Bytes(), &reply))
	require.False(t, reply.LegacyAdmin, "each request rereads the authoritative legacy user")
	require.NoError(t, db.Table("iam_policy_state").Where("id=1").Updates(map[string]any{"cutover_state": "blocked", "cutover_batch_id": "c-legacy-blocked"}).Error)
	response = call(member.ID)
	require.Equal(t, 403, response.Code, "the migration barrier must remain closed")
}
