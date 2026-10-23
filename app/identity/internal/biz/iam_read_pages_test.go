package biz

import (
	"testing"

	"github.com/stretchr/testify/require"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

func TestIAMReadPagesReleaseManifestIsRootOnly(t *testing.T) {
	grants := IAMMigrationBuiltinGrants()
	for _, operation := range []string{"billing.vendor_report.read", "channel.upstream_contract.read"} {
		require.True(t, IAMExecutionBound(operation))
		for role, roleGrants := range grants {
			found := false
			for _, grant := range roleGrants {
				if grant.Operation == operation && grant.Effect == authorization.Allow {
					found = true
				}
			}
			require.Equal(t, role == "root", found, "%s grants %s", role, operation)
		}
	}
	for _, grant := range grants["root"] {
		// Migration 140 separately releases report import; other writes stay absent.
		require.NotContains(t, []string{"billing.vendor_report.attribute", "channel.upstream_contract.create", "channel.upstream_contract.publish", "channel.upstream_contract.quarantine"}, grant.Operation)
	}
}

func TestIAMVendorImportReleaseManifestIsRootOnly(t *testing.T) {
	require.True(t, IAMExecutionBound("billing.vendor_report.import"))
	for role, grants := range IAMMigrationBuiltinGrants() {
		found := false
		for _, grant := range grants {
			if grant.Operation == "billing.vendor_report.import" && grant.Effect == authorization.Allow {
				found = true
				require.Equal(t, authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, grant.Scope)
			}
		}
		require.Equal(t, role == "root", found, "unexpected import authority for %s", role)
	}
}

func TestIAMReadPagesMenuRouteIsSupported(t *testing.T) {
	require.NoError(t, iamValidateMenu(m.Menu{Name: "上游契约", RouteKey: "upstream-contracts", IconKey: "file-text", RequiredAll: []string{"channel.upstream_contract.read"}}, nil))
}
