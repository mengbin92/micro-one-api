// Package testutil exposes only constructors for cross-service integration tests.
package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	v "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data/iam"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"
)

func NewIAMHTTP(client v.IAMServiceClient) *khttp.Server {
	admin := service.NewAdminService(nil, nil, nil, nil)
	admin.SetIAMService(service.NewIAMAdminService(biz.NewIAMUsecase(iam.NewRepo(client))))
	return server.NewHTTPServer(":0", admin, nil)
}
