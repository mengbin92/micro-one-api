package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/domain/authorization"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

type subscriptionAccountPreconditionClient struct {
	*adminHTTPChannelClient
	revisions map[int64]int64
	seen      map[int64]int64
	reasons   map[int64]string
}

func (c *subscriptionAccountPreconditionClient) check(ctx context.Context, id, revision int64, reason string) error {
	c.seen[id] = revision
	c.reasons[id] = reason
	ctx = authorization.WithExpectedRevision(authorization.WithWriteReason(ctx, reason), "account", id, revision)
	return authorization.CheckWriteRevision(ctx, "account", id, c.revisions[id])
}

func (c *subscriptionAccountPreconditionClient) ResetSubscriptionAccountQuota(ctx context.Context, req *channelv1.ResetSubscriptionAccountQuotaRequest, _ ...grpc.CallOption) (*channelv1.ResetSubscriptionAccountQuotaResponse, error) {
	if err := c.check(ctx, req.AccountId, req.ExpectedRevision, req.Reason); err != nil {
		return nil, err
	}
	return c.adminHTTPChannelClient.ResetSubscriptionAccountQuota(ctx, req)
}

func (c *subscriptionAccountPreconditionClient) UpdateSubscriptionAccount(ctx context.Context, req *channelv1.UpdateSubscriptionAccountRequest, _ ...grpc.CallOption) (*channelv1.UpdateSubscriptionAccountResponse, error) {
	if err := c.check(ctx, req.Id, req.ExpectedRevision, req.Reason); err != nil {
		return nil, err
	}
	return c.adminHTTPChannelClient.UpdateSubscriptionAccount(ctx, req)
}

func TestBatchSubscriptionAccountQuotaForwardsWritePreconditions(t *testing.T) {
	t.Setenv("ADMIN_TOKEN", "admin-token")
	for _, route := range []struct {
		path   string
		fields string
	}{
		{"/api/subscription-accounts/batch-reset-quota", `"scope":"daily"`},
		{"/v1/subscription-accounts/batch-quota-template", `"template":{"quota_daily_limit_usd":25}`},
	} {
		for _, tc := range []struct {
			name      string
			revisions string
			success   bool
			updated   int
		}{
			{"current", `{"201":7,"202":9}`, true, 2},
			{"stale", `{"201":6,"202":9}`, false, 1},
			{"missing", `{"202":9}`, false, 1},
		} {
			t.Run(route.path+"/"+tc.name, func(t *testing.T) {
				client := &subscriptionAccountPreconditionClient{
					adminHTTPChannelClient: &adminHTTPChannelClient{},
					revisions:              map[int64]int64{201: 7, 202: 9},
					seen:                   map[int64]int64{},
					reasons:                map[int64]string{},
				}
				srv := newAdminHTTPTestServer(&adminHTTPIdentityClient{}, client, &adminHTTPBillingClient{})
				body := fmt.Sprintf(`{"account_ids":[201,202,201],%s,"expected_revisions":%s,"reason":"上游到期"}`, route.fields, tc.revisions)
				req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer admin-token")
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), fmt.Sprintf(`"success":%t`, tc.success))
				require.Contains(t, rec.Body.String(), fmt.Sprintf(`"updated_count":%d`, tc.updated))
				require.Equal(t, map[int64]string{201: "上游到期", 202: "上游到期"}, client.reasons)
				require.Equal(t, int64(9), client.seen[202])
				if tc.success {
					require.Equal(t, int64(7), client.seen[201])
				} else {
					require.Contains(t, rec.Body.String(), `"failed_ids":[201]`)
				}
			})
		}
	}
}
