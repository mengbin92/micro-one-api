package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	billingv1 "micro-one-api/api/billing/v1"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data/channelclient"
	"micro-one-api/app/admin/internal/data/iam"
	"micro-one-api/app/admin/internal/data/routingaccess"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"
)

// NewManagedResourcesHTTP shares the real admin startup adapters for browser
// acceptance. Connections belong to the isolated caller-owned test stack.
func NewManagedResourcesHTTP(identity identityv1.IdentityServiceClient, iamClient identityv1.IAMServiceClient, channel channelv1.ChannelServiceClient, billing billingv1.BillingServiceClient) *khttp.Server {
	svc := service.NewAdminService(billing, identity, channel, nil)
	svc.SetIAMService(service.NewIAMAdminService(biz.NewIAMUsecase(iam.NewRepo(iamClient))))
	reader := channelclient.NewRoutingGroupReader(channel)
	svc.SetRoutingGroupUsecase(biz.NewRoutingGroupUsecase(reader))
	svc.SetRoutingAccessUsecase(biz.NewRoutingAccessUsecase(reader, routingaccess.NewRepo(identity, channel, nil)))
	return server.NewHTTPServer(":0", svc, nil)
}
