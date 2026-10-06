package serviceidentity

import (
	"context"
	"micro-one-api/domain/authorization"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	admin "micro-one-api/api/admin/v1"
	billing "micro-one-api/api/billing/v1"
	channel "micro-one-api/api/channel/v1"
	config "micro-one-api/api/config/v1"
	identity "micro-one-api/api/identity/v1"
	logs "micro-one-api/api/log/v1"
	monitor "micro-one-api/api/monitor/v1"
	notify "micro-one-api/api/notify/v1"
	relay "micro-one-api/api/relay/v1"
)

func TestIndependentCredentials(t *testing.T) {
	for _, tokens := range []map[string]string{{"relay": "shared"}, {"relay": "same", "admin": "same"}, {"attacker": "opaque"}, {"relay": ""}} {
		_, err := NewVerifier(tokens, "shared")
		require.Error(t, err)
	}
	v, err := NewVerifier(map[string]string{"relay": "relay-private", "admin": "admin-private"}, "shared")
	require.NoError(t, err)
	legacy, err := v.Authenticate("shared")
	require.NoError(t, err)
	require.False(t, legacy.SystemCapability("/api.billing.v1.BillingService/CommitQuota"))
	p, err := v.Authenticate("relay-private")
	require.NoError(t, err)
	require.True(t, p.SystemCapability("/api.billing.v1.BillingService/CommitQuota"))
	require.False(t, p.CanCall("/api.identity.v1.IdentityService/DeleteUser"))
	require.False(t, p.CanCall("/api.billing.v1.BillingService/Unknown"))
	_, err = v.Authenticate(`{"service_name":"relay"}`)
	require.Error(t, err)
	require.Equal(t, p, FromContext(WithPrincipal(context.Background(), p)))
}

func TestEveryRegisteredRPCClassified(t *testing.T) {
	for _, desc := range []*grpc.ServiceDesc{&admin.AdminService_ServiceDesc, &admin.IAMAdminService_ServiceDesc, &identity.IdentityService_ServiceDesc, &identity.IAMService_ServiceDesc, &billing.BillingService_ServiceDesc, &channel.ChannelService_ServiceDesc, &config.ConfigService_ServiceDesc, &logs.LogService_ServiceDesc, &monitor.MonitorService_ServiceDesc, &notify.NotifyService_ServiceDesc, &relay.RelayService_ServiceDesc} {
		for _, method := range desc.Methods {
			full := "/" + desc.ServiceName + "/" + method.MethodName
			policy, ok := Lookup(full)
			require.True(t, ok, "unclassified registered method %s", full)
			require.NotEmpty(t, policy.Owner)
		}
		require.Empty(t, desc.Streams, "new streams require separate capability review")
	}
}

func TestHTTPOnlyPoliciesCannotConferSystemCapability(t *testing.T) {
	entry := "/api.log.v1.LogService/DeleteLogs"
	admin := Principal{Name: "admin", Dedicated: true}
	if !admin.CanCallHTTP(entry) || admin.SystemCapability(entry) {
		t.Fatal("HTTP user policy must be independent of system capability")
	}
	if (Principal{Name: "admin"}).CanCallHTTP(entry) || (Principal{Name: "relay", Dedicated: true}).CanCallHTTP(entry) || admin.CanCallHTTP(entry+"/unknown") {
		t.Fatal("unverified or unknown HTTP caller allowed")
	}
}

func TestUserOperatorNeverReceivesSystemBypass(t *testing.T) {
	const method = "/api.channel.v1.ChannelService/GetRoutingGroup"
	ctx := WithRPCMethod(WithPrincipal(context.Background(), Principal{Name: "admin", Dedicated: true}), method)
	require.True(t, HasSystemCapability(ctx, method))
	require.False(t, HasSystemCapability(authorization.WithCredential(ctx, "verified-session"), method))
}
