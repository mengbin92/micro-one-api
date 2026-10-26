package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"micro-one-api/app/admin/internal/server"
)

// NewPublicIdentityHTTP wires the real public BFF proxy and policy reader for
// isolated authentication integration tests using an Identity HTTP endpoint.
func NewPublicIdentityHTTP(endpoint string) *khttp.Server {
	return server.NewHTTPServer(":0", nil, nil, endpoint)
}
