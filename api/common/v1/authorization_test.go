package commonv1

import (
	"testing"

	"micro-one-api/pkg/jsonx"

	"google.golang.org/protobuf/encoding/protojson"
)

// Public IDs/revisions must survive a browser JSON round trip above 2^53.
func TestAuthorizationJSONUsesStringIDsAndRevisions(t *testing.T) {
	const large = int64(9007199254740993)
	dto := &AuthorizationDecision{Context: &AuthorizationContext{ContextType: "platform", ContextKey: "platform"}, Versions: &AuthorizationVersions{PolicyRevision: uint64(large)}, Sources: []*AuthorizationGrantSource{{AssignmentId: large, RoleScope: &AuthorizationScope{Clauses: []*AuthorizationScopeClause{{ResourceIds: []int64{large}}}}}}}
	data, err := protojson.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	var public struct {
		Versions struct {
			PolicyRevision string `json:"policyRevision"`
		} `json:"versions"`
		Sources []struct {
			AssignmentID string `json:"assignmentId"`
			RoleScope    struct {
				Clauses []struct {
					IDs []string `json:"resourceIds"`
				} `json:"clauses"`
			} `json:"roleScope"`
		} `json:"sources"`
	}
	if err := jsonx.Unmarshal(data, &public); err != nil {
		t.Fatal(err)
	}
	if public.Versions.PolicyRevision != "9007199254740993" || public.Sources[0].AssignmentID != public.Versions.PolicyRevision || public.Sources[0].RoleScope.Clauses[0].IDs[0] != public.Versions.PolicyRevision {
		t.Fatalf("unsafe JSON: %s", data)
	}
	if err := protojson.Unmarshal([]byte(`{"context":{"contextType":"organization","sql":"true"}}`), new(AuthorizationDecision)); err == nil {
		t.Fatal("unknown descriptor accepted")
	}
}
