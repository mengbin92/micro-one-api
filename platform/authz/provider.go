package authz

import (
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	v "micro-one-api/api/identity/v1"
	grpcauth "micro-one-api/platform/grpc"
	"micro-one-api/platform/security/serviceidentity"
)

// FromEnvironment dials the identity endpoint when IDENTITY_GRPC_ENDPOINT is
// configured. The connection authenticates with this process's dedicated
// SERVICE_IDENTITY_TOKEN (falling back to the legacy shared token, which
// identity will reject for owner-only methods once IAM mode is active).
// Without an endpoint, use the standard local identity port. Missing or
// unavailable identity always fails closed; mode is never inferred locally.
func FromEnvironment(owner string) (*Client, error) {
	endpoint := strings.TrimSpace(os.Getenv("IDENTITY_GRPC_ENDPOINT"))
	if endpoint == "" {
		endpoint = "127.0.0.1:9001"
	}
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(grpcauth.NewInsecureTokenAuth(serviceidentity.ClientToken())),
	)
	if err != nil {
		return NewClient(owner, nil), fmt.Errorf("dial identity authorization endpoint: %w", err)
	}
	client := NewClient(owner, v.NewIAMServiceClient(conn))
	client.conn = conn
	return client, nil
}
