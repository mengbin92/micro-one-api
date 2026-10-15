package server

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	adminv1 "micro-one-api/api/admin/v1"
)

func TestDecodeCompatibilityWrapperDoesNotTreatPromotedMethodsAsDirectProto(t *testing.T) {
	var payload struct {
		ID int64 `json:"id"`
		*adminv1.AdminUpdateChannelRequest
	}
	req := httptest.NewRequest("PUT", "/api/channel", strings.NewReader(`{"id":11,"type":6,"expected_revision":2,"reason":"repair provider","models":""}`))
	req = req.WithContext(context.WithValue(req.Context(), iamBusinessContextKey{}, true))
	w := httptest.NewRecorder()
	require.NotPanics(t, func() { require.True(t, decodeBody(w, req, &payload)) })
	require.EqualValues(t, 11, payload.ID)
	require.NotNil(t, payload.AdminUpdateChannelRequest)
	require.NotNil(t, payload.Type)
	require.EqualValues(t, 6, *payload.Type)
	require.EqualValues(t, 2, payload.ExpectedRevision)
}

func TestDecodeDirectProtoRetainsProtoJSONInt64Semantics(t *testing.T) {
	var payload adminv1.AdminUpdateChannelRequest
	req := httptest.NewRequest("PUT", "/api/v1/channels/11", strings.NewReader(`{"channelId":"11","expectedRevision":"2","type":6}`))
	req = req.WithContext(context.WithValue(req.Context(), iamBusinessContextKey{}, true))
	require.True(t, decodeBody(httptest.NewRecorder(), req, &payload))
	require.EqualValues(t, 11, payload.ChannelId)
	require.EqualValues(t, 2, payload.ExpectedRevision)
	require.EqualValues(t, 6, payload.GetType())
}
