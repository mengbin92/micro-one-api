package iamdto

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	v "micro-one-api/api/identity/v1"
)

// Kratos v3's default JSON codec uses ordinary struct JSON. IAM requires
// protobuf JSON so int64/uint64 never become unsafe browser numbers and field
// masks/timestamps follow their public protobuf contract. Other routes retain
// the server's existing codec.
func DecodeRequest(req *http.Request, dst any) error {
	var message proto.Message
	limit := int64(1 << 20)
	switch value := dst.(type) {
	case *v.IAMRequest:
		message = value
	case *v.SecurityRequest:
		message = value
		limit = 4096
	default:
		return khttp.DefaultRequestDecoder(req, dst)
	}
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") {
		return errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_CONSTRAINT_VIOLATION.String(), "application/json required")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_CONSTRAINT_VIOLATION.String(), "invalid request body")
	}
	if err = (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, message); err != nil {
		return errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_CONSTRAINT_VIOLATION.String(), "invalid protobuf JSON request")
	}
	return nil
}
func EncodeResponse(w http.ResponseWriter, req *http.Request, src any) error {
	var message proto.Message
	switch value := src.(type) {
	case *v.IAMReply:
		message = value
	case *v.SecurityReply:
		message = value
	default:
		return khttp.DefaultResponseEncoder(w, req, src)
	}
	_, securityReply := src.(*v.SecurityReply)
	body, err := (protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: securityReply}).Marshal(message)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(body)
	return err
}
