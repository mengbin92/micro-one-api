package server

import (
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	a "micro-one-api/api/admin/v1"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"strconv"
)

// New concurrency and mask fields use protobuf JSON, preserving uint64 strings
// and FieldMask semantics. Numeric legacy IDs remain accepted by protojson.
func decodeManagedUserPatch(w http.ResponseWriter, r *http.Request, req *a.AdminUpdateUserRequest) bool {
	return decodeManagedUserBody(w, r, req, r.URL.Path == "/api/user" || r.URL.Path == "/api/user/")
}

func decodeManagedUserBody(w http.ResponseWriter, r *http.Request, req proto.Message, aliasID bool) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err == nil && aliasID {
		var fields map[string]jsonx.RawMessage
		if err = jsonx.Unmarshal(body, &fields); err == nil {
			if id, ok := fields["id"]; ok {
				if _, exists := fields["user_id"]; exists {
					err = strconv.ErrSyntax
				} else if _, exists := fields["userId"]; exists {
					err = strconv.ErrSyntax
				} else {
					fields["user_id"] = id
					delete(fields, "id")
					body, err = jsonx.Marshal(fields)
				}
			}
		}
	}
	if err != nil || len(body) > 1<<20 || (protojson.UnmarshalOptions{DiscardUnknown: !isIAMBusinessContext(r.Context())}).Unmarshal(body, req) != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid user update"})
		return false
	}
	return true
}

func managedUserDeletionRequest(w http.ResponseWriter, r *http.Request, id int64) (*a.AdminDeleteUserRequest, bool) {
	req := &a.AdminDeleteUserRequest{UserId: id, Reason: r.URL.Query().Get("reason")}
	if !isIAMBusinessContext(r.Context()) {
		return req, true
	}
	var err error
	req.ExpectedRevision, err = strconv.ParseUint(r.URL.Query().Get("expected_revision"), 10, 64)
	if err != nil || req.ExpectedRevision == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected_revision required"})
		return nil, false
	}
	req.ExpectedPolicyRevision, err = strconv.ParseUint(r.URL.Query().Get("expected_policy_revision"), 10, 64)
	if err != nil || req.ExpectedPolicyRevision == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "expected_policy_revision required"})
		return nil, false
	}
	return req, true
}

func writeIAMProto(w http.ResponseWriter, value proto.Message) {
	body, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(value)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "response encoding failed"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	// #nosec G705 -- body is protojson output served as application/json, not HTML.
	_, _ = w.Write(body)
}
