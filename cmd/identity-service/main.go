package main

import (
	"os"

	"micro-one-api/internal/identity/biz"
	"micro-one-api/internal/identity/data"
	"micro-one-api/internal/identity/server"
	"micro-one-api/internal/identity/service"

	"github.com/go-kratos/kratos/v2"
)

func main() {
	addr := os.Getenv("IDENTITY_GRPC_ADDR")
	if addr == "" {
		addr = ":9001"
	}
	repo, err := data.NewRepositoryFromEnv()
	if err != nil {
		panic(err)
	}
	uc := biz.NewIdentityUsecase(repo)
	svc := service.NewIdentityService(uc)
	grpcSrv := server.NewGRPCServer(addr, svc)
	app := kratos.New(
		kratos.Name("identity-service"),
		kratos.Server(grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
