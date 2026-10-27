package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data/iam"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"
)

// NewPublicIdentityHTTP wires the real public BFF proxy and policy reader for
// isolated authentication integration tests using an Identity HTTP endpoint.
func NewPublicIdentityHTTP(endpoint string, clients ...v.IAMServiceClient) *khttp.Server {
	var svc *service.AdminService
	if len(clients) > 0 {
		svc = service.NewAdminService(nil, nil, nil, nil)
		svc.SetIAMService(service.NewIAMAdminService(biz.NewIAMUsecase(iam.NewRepo(clients[0]))))
	}
	return server.NewHTTPServer(":0", svc, nil, endpoint)
}
