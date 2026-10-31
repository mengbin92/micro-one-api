package testutil

import (
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc"
	v "micro-one-api/api/admin/v1"
	identityv1 "micro-one-api/api/identity/v1"
	"micro-one-api/app/admin/internal/biz"
	"micro-one-api/app/admin/internal/data/iam"
	"micro-one-api/app/admin/internal/server"
	"micro-one-api/app/admin/internal/service"
	b "micro-one-api/domain/backup/biz"
	"micro-one-api/platform/grpc/xgrpc"
)

func NewManagedBackupStack(identity identityv1.IdentityServiceClient, identityIAM identityv1.IAMServiceClient, repo b.Repo, serviceToken string) (*khttp.Server, *grpc.Server) {
	svc := service.NewAdminService(nil, identity, nil, nil)
	svc.SetIAMService(service.NewIAMAdminService(biz.NewIAMUsecase(iam.NewRepo(identityIAM))))
	svc.SetBackupUsecase(b.NewUsecase(repo, svc.ResourceAuthorizer()))
	rpc := grpc.NewServer(grpc.ChainUnaryInterceptor(xgrpc.ServiceTokenUnaryInterceptor(serviceToken), server.IAMOperatorUnaryInterceptor()))
	v.RegisterAdminServiceServer(rpc, svc)
	return server.NewHTTPServer(":0", svc, nil), rpc
}
