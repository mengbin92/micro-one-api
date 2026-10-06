package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data/channelclient"
	"micro-one-api/app/admin/internal/data/iam"
	"micro-one-api/app/admin/internal/data/routingaccess"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"
)

func NewManagedChannelHTTP(identity identityv1.IdentityServiceClient, iamClient identityv1.IAMServiceClient, channel channelv1.ChannelServiceClient) *khttp.Server {
	svc := service.NewAdminService(nil, identity, channel, nil)
	svc.SetIAMService(service.NewIAMAdminService(biz.NewIAMUsecase(iam.NewRepo(iamClient))))
	reader := channelclient.NewRoutingGroupReader(channel)
	svc.SetRoutingGroupUsecase(biz.NewRoutingGroupUsecase(reader))
	svc.SetRoutingAccessUsecase(biz.NewRoutingAccessUsecase(reader, routingaccess.NewRepo(identity, channel, nil)))
	return server.NewHTTPServer(":0", svc, nil)
}
