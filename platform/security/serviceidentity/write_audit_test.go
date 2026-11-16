package serviceidentity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
)

func TestAllWriteAuditOwnersRequireAdminSystemIdentity(t *testing.T) {
	for _, method := range []string{
		"/api.channel.v1.ChannelService/ListWriteAudits",
		"/api.billing.v1.BillingService/ListWriteAudits",
		"/api.config.v1.ConfigService/ListWriteAudits",
		"/api.log.v1.LogService/ListWriteAudits",
		"/api.monitor.v1.MonitorService/ListWriteAudits",
		"/api.notify.v1.NotifyService/ListWriteAudits",
	} {
		t.Run(method, func(t *testing.T) {
			policy, ok := Lookup(method)
			require.True(t, ok, "every owner that writes resource_write_audits needs a read source")
			require.Empty(t, policy.UserCallers)
			require.Equal(t, []string{"admin"}, policy.SystemCallers)
			ctx := WithRPCMethod(WithPrincipal(context.Background(), Principal{Name: "admin", Dedicated: true}), method)
			require.True(t, HasSystemCapability(ctx, method))
			require.False(t, HasSystemCapability(authorization.WithCredential(ctx, "user-session"), method))
			require.False(t, (Principal{Name: "relay", Dedicated: true}).CanCall(method))
		})
	}
}
