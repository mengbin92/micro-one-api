package main

import (
	"os"

	"micro-one-api/internal/channel/biz"
	"micro-one-api/internal/channel/data"
	"micro-one-api/internal/channel/server"
	"micro-one-api/internal/channel/service"

	"github.com/go-kratos/kratos/v2"
)

func main() {
	addr := os.Getenv("CHANNEL_GRPC_ADDR")
	if addr == "" {
		addr = ":9002"
	}
	repo, err := data.NewRepositoryFromEnv()
	if err != nil {
		panic(err)
	}
	uc := biz.NewChannelUsecase(repo)
	svc := service.NewChannelService(uc)
	grpcSrv := server.NewGRPCServer(addr, svc)
	app := kratos.New(
		kratos.Name("channel-service"),
		kratos.Server(grpcSrv),
	)
	if err := app.Run(); err != nil {
		panic(err)
	}
}
