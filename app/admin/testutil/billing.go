package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	billingv1 "micro-one-api/api/billing/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data/iam"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"
)

func NewManagedBillingHTTP(identity identityv1.IdentityServiceClient, iamClient identityv1.IAMServiceClient, billing billingv1.BillingServiceClient) *khttp.Server {
	svc := service.NewAdminService(billing, identity, nil, nil)
	svc.SetIAMService(service.NewIAMAdminService(biz.NewIAMUsecase(iam.NewRepo(iamClient))))
	return server.NewHTTPServer(":0", svc, nil)
}
