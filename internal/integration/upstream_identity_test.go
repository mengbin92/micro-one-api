package integration

import (
	"testing"

	"google.golang.org/grpc"
	appgrpc "micro-one-api/platform/grpc"
	"micro-one-api/platform/grpc/xgrpc"
	"micro-one-api/platform/security/serviceidentity"
)

// Exercise the real dedicated-credential boundary used by execution claims.
func channelRelayCredentials(t *testing.T) (grpc.ServerOption, grpc.DialOption) {
	t.Helper()
	const token = "integration-relay-upstream-controls"
	verifier, err := serviceidentity.NewVerifier(map[string]string{"relay": token}, "")
	if err != nil {
		t.Fatal(err)
	}
	return grpc.ChainUnaryInterceptor(xgrpc.ServiceIdentityUnaryInterceptor(verifier)), grpc.WithPerRPCCredentials(appgrpc.NewInsecureTokenAuth(token))
}
