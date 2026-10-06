package testutil

import (
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	"gorm.io/gorm"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/app/channel/internal/data"
	"micro-one-api/app/channel/internal/server"
	"micro-one-api/app/channel/internal/service"
	"micro-one-api/platform/authz"
)

func NewIAMStack(db *gorm.DB, resolver *authz.Client) *kgrpc.Server {
	channels, models, modelRoutings, groups := data.NewOwnerRepositoriesWithDB(db)
	svc := service.NewChannelService(biz.NewChannelUsecase(channels, nil))
	svc.SetResourceAuthorization(resolver)
	svc.SetModelUsecase(biz.NewModelUsecase(models))
	svc.SetModelRoutingUsecase(biz.NewModelRoutingUsecase(modelRoutings))
	svc.SetRoutingGroupUsecase(biz.NewRoutingGroupUsecase(groups))
	return server.NewGRPCServer(":0", svc)
}
