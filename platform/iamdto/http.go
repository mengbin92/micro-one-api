package iamdto

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-kratos/kratos/v3/errors"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/protobuf/encoding/protojson"
	v "micro-one-api/api/identity/v1"
)

// Kratos v3's default JSON codec uses ordinary struct JSON. IAM requires
// protobuf JSON so int64/uint64 never become unsafe browser numbers and field
// masks/timestamps follow their public protobuf contract. Other routes retain
// the server's existing codec.
func DecodeRequest(req *http.Request, dst any) error {
	message, ok := dst.(*v.IAMRequest)
	if !ok {
		return khttp.DefaultRequestDecoder(req, dst)
	}
	if !strings.HasPrefix(req.Header.Get("Content-Type"), "application/json") {
		return errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_CONSTRAINT_VIOLATION.String(), "application/json required")
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_CONSTRAINT_VIOLATION.String(), "invalid request body")
	}
	if err = (protojson.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(body, message); err != nil {
		return errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_CONSTRAINT_VIOLATION.String(), "invalid protobuf JSON request")
	}
	return nil
}
func EncodeResponse(w http.ResponseWriter, req *http.Request, src any) error {
	message, ok := src.(*v.IAMReply)
	if !ok {
		return khttp.DefaultResponseEncoder(w, req, src)
	}
	body, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(message)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(body)
	return err
}
