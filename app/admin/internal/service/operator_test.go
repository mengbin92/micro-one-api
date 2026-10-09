package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"micro-one-api/domain/authorization"
)

func TestOperatorRPCContextReplacesReasonMetadata(t *testing.T) {
	for _, reason := range []string{"", "上游缺货", "供应不足\n稍后恢复 ✅", "line\nbreak", "tab\tseparated", "delete\x7f"} {
		t.Run(reason, func(t *testing.T) {
			original := metadata.Pairs("x-operator-authorization", "Bearer forged", "x-authorization-reason", "stale", "x-authorization-reason-bin", "stale binary", "trace-id", "trace")
			ctx := metadata.NewOutgoingContext(context.Background(), original)
			ctx = WithOperatorCredential(authorization.WithWriteReason(ctx, reason), "live-session")
			md, _ := metadata.FromOutgoingContext(operatorRPCContext(ctx))
			require.Empty(t, md.Get("x-authorization-reason"))
			if reason == "" {
				require.Empty(t, md.Get("x-authorization-reason-bin"))
			} else {
				require.Equal(t, []string{reason}, md.Get("x-authorization-reason-bin"))
			}
			require.Equal(t, []string{"Bearer live-session"}, md.Get("x-operator-authorization"))
			require.Equal(t, []string{"trace"}, md.Get("trace-id"))
			require.Equal(t, []string{"stale binary"}, original.Get("x-authorization-reason-bin"), "caller metadata must not be mutated")
		})
	}
}

func TestOperatorRPCContextPreservesASCIIReasonForOlderOwners(t *testing.T) {
	for _, reason := range []string{"reviewed change", " ~", "retry #9"} {
		ctx := authorization.WithWriteReason(context.Background(), reason)
		md, _ := metadata.FromOutgoingContext(operatorRPCContext(ctx))
		require.Equal(t, []string{reason}, md.Get("x-authorization-reason"), "older owners only read the text metadata key")
		require.Empty(t, md.Get("x-authorization-reason-bin"), "sending both forms would be ambiguous")
	}
}
