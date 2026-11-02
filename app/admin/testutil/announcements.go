package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc"
	"gorm.io/gorm"
	channelv1 "micro-one-api/api/channel/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data"
	"micro-one-api/app/admin/internal/data/iam"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"
)

func NewUserOperationsStack(db *gorm.DB, identity identityv1.IdentityServiceClient, identityIAM identityv1.IAMServiceClient, channels channelv1.ChannelServiceClient) (*khttp.Server, *grpc.Server) {
	svc := service.NewAdminService(nil, identity, channels, nil)
	svc.SetIAMService(service.NewIAMAdminService(biz.NewIAMUsecase(iam.NewRepo(identityIAM))))
	svc.SetAnnouncementUsecase(biz.NewAnnouncementUsecase(data.NewAnnouncementRepo(&data.Data{DB: db}), svc.ResourceAuthorizer()))
	return server.NewHTTPServer(":0", svc, nil), server.NewGRPCServer(svc)
}
