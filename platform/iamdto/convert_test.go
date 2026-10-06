package iamdto

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	c "micro-one-api/api/common/v1"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

func TestIAMProtoJSONPreservesLargeIDsAndRejectsUnknownFields(t *testing.T) {
	id := int64(9007199254740993)
	body := `{"role":{"code":"sample","name":"Sample"},"expectedPolicyRevision":"9007199254740993","reason":"audit","requestId":"test"}`
	req := httptest.NewRequest("POST", "/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	var p v.IAMRequest
	require.NoError(t, DecodeRequest(req, &p))
	require.Equal(t, uint64(id), p.ExpectedPolicyRevision)
	require.NoError(t, ValidateRequest(&p))
	roundtrip := IAMRequestTo(IAMRequestFrom(&p))
	require.Nil(t, roundtrip.Object)
	require.Nil(t, roundtrip.Role.Context)
	require.True(t, proto.Equal(p.Context, roundtrip.Context))
	w := httptest.NewRecorder()
	require.NoError(t, EncodeResponse(w, req, IAMReplyTo(m.Response{Roles: []m.Role{{ID: id}}, BasePolicyRevision: uint64(id)})))
	require.Contains(t, w.Body.String(), `"id":"9007199254740993"`)
	require.Contains(t, w.Body.String(), `"base_policy_revision":"9007199254740993"`)
	req = httptest.NewRequest("POST", "/", strings.NewReader(`{"actor_user_id":"1"}`))
	req.Header.Set("Content-Type", "application/json")
	require.Error(t, DecodeRequest(req, &v.IAMRequest{}))
}
func TestIAMScopeAndMaskBoundary(t *testing.T) {
	platform := AuthorizationContextTo(authorization.Platform())
	p := &v.IAMRequest{Context: platform, Role: &v.IAMRole{Name: "Name"}, UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"missing_field"}}}
	require.Error(t, ValidateRequest(p))
	p.UpdateMask.Paths = []string{"name"}
	require.NoError(t, ValidateRequest(p))
	p.Role.Context = &c.AuthorizationContext{ContextType: "organization", ContextKey: "organization:1", OrganizationId: 1}
	require.Error(t, ValidateRequest(p))
	p.Role.Context = nil
	p.Assignment = &v.IAMAssignment{Validity: &c.AuthorizationInterval{StartsAt: &timestamppb.Timestamp{Seconds: time.Now().Unix(), Nanos: 1000000000}}}
	require.Error(t, ValidateRequest(p))
	require.Nil(t, AuthorizationScopeFrom(nil).Clauses)
	require.NotNil(t, AuthorizationScopeFrom(&c.AuthorizationScope{}).Clauses)
}
