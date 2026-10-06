package server

import (
	"google.golang.org/protobuf/encoding/protojson"
	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/admin/internal/service"
	"micro-one-api/pkg/jsonx"
	"net/http"
	"strconv"
	"strings"
)

func handleRoutingGroupManagement(w http.ResponseWriter, r *http.Request, svc *service.AdminService, id int64, action string) {
	if action == "archive" && r.Method != http.MethodPost || action == "members" && r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var input struct {
		ExpectedRevision string `json:"expected_revision"`
		Reason           string `json:"reason"`
		Members          []struct {
			SourceKind string `json:"source_kind"`
			SourceID   string `json:"source_id"`
		} `json:"members"`
	}
	if jsonx.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	rev, err := strconv.ParseInt(input.ExpectedRevision, 10, 64)
	if err != nil || rev <= 0 || strings.TrimSpace(input.Reason) == "" {
		writeJSON(w, 400, apiResponse(false, "expected_revision and reason required", nil))
		return
	}
	var out *channelv1.RoutingGroupMutationReply
	if action == "archive" {
		out, err = svc.ArchiveRoutingGroup(r.Context(), &channelv1.ArchiveRoutingGroupRequest{Id: id, ExpectedRevision: rev, Reason: input.Reason})
	} else {
		members := make([]*channelv1.RoutingGroupMemberInput, 0, len(input.Members))
		for _, m := range input.Members {
			source, parseErr := strconv.ParseInt(m.SourceID, 10, 64)
			if parseErr != nil || source <= 0 || (m.SourceKind != "channel" && m.SourceKind != "subscription") {
				writeJSON(w, 400, apiResponse(false, "invalid member", nil))
				return
			}
			members = append(members, &channelv1.RoutingGroupMemberInput{SourceKind: m.SourceKind, SourceId: source})
		}
		out, err = svc.ReplaceRoutingGroupMembers(r.Context(), &channelv1.ReplaceRoutingGroupMembersRequest{Id: id, ExpectedRevision: rev, Reason: input.Reason, Members: members})
	}
	if err != nil {
		routingGroupError(w, err)
		return
	}
	body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}).Marshal(out)
	if err != nil {
		writeServiceResponse(w, nil, err)
		return
	}
	writeJSON(w, 200, apiResponse(true, "", jsonx.RawMessage(body)))
}
