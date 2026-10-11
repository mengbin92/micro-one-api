package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

type modelFilterChannelClient struct {
	adminHTTPModelChannelClient
	listReq *channelv1.ListModelsRequest
}

func TestAdminHTTPModelStatusFilterRejectsInvalidValues(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	for _, path := range []string{"/api/admin/models", "/api/admin/models/export"} {
		for _, value := range []string{"-1", "3", "invalid", "4294967296"} {
			t.Run(path+"?status="+value, func(t *testing.T) {
				client := &modelFilterChannelClient{}
				srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, client, &adminHTTPBillingClient{})
				req := httptest.NewRequest(http.MethodGet, path+"?status="+value, nil)
				req.Header.Set("Authorization", "Bearer admin-token")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Nil(t, client.listReq)
				require.Nil(t, client.exportReq)
			})
		}
	}
}

func (c *modelFilterChannelClient) ListModels(ctx context.Context, req *channelv1.ListModelsRequest, opts ...grpc.CallOption) (*channelv1.ListModelsResponse, error) {
	c.listReq = req
	return c.adminHTTPModelChannelClient.ListModels(ctx, req, opts...)
}

func TestAdminHTTPModelStatusFilterPreservesDisabled(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	for _, path := range []string{"/api/admin/models", "/api/admin/models/export"} {
		for _, filter := range []struct {
			query   string
			present bool
			status  int32
		}{
			{query: ""},
			{query: "?status=0", present: true},
			{query: "?status=1", present: true, status: 1},
			{query: "?status=2", present: true, status: 2},
		} {
			t.Run(path+filter.query, func(t *testing.T) {
				client := &modelFilterChannelClient{}
				srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, client, &adminHTTPBillingClient{})
				req := httptest.NewRequest(http.MethodGet, path+filter.query, nil)
				req.Header.Set("Authorization", "Bearer admin-token")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				var forwarded proto.Message = client.listReq
				if path == "/api/admin/models/export" {
					forwarded = client.exportReq
				}
				require.NotNil(t, forwarded)
				message := forwarded.ProtoReflect()
				field := message.Descriptor().Fields().ByName("status")
				require.Equal(t, filter.present, message.Has(field), "disabled and omitted filters must remain distinct through RPC")
				require.Equal(t, int64(filter.status), message.Get(field).Int())
			})
		}
	}
}
