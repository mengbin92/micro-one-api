package iamdto

import (
	"net/http"
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

// SecurityRequest carries base64url WebAuthn attestation/assertion payloads.
// Pin the decode budget: a full-size envelope must fit, and anything past the
// cap must be rejected before protojson sees it. This guards against silently
// reverting to the 4096 default and breaking passkey enrollment/login.
func TestSecurityRequestDecodeBudget(t *testing.T) {
	const budget = 24576
	const prefix, suffix = `{"attestation":"`, `"}`
	request := func(body string) *http.Request {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return req
	}
	exact := prefix + strings.Repeat("A", budget-len(prefix)-len(suffix)) + suffix
	var p v.SecurityRequest
	require.NoError(t, DecodeRequest(request(exact), &p))
	require.Len(t, p.Attestation, budget-len(prefix)-len(suffix))

	over := prefix + strings.Repeat("A", budget) + suffix
	require.Error(t, DecodeRequest(request(over), &v.SecurityRequest{}))
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
