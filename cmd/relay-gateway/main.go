package main

import (
	"context"
	"fmt"
	"os"

	"micro-one-api/internal/relay/biz"
	"micro-one-api/internal/relay/data"
	"micro-one-api/internal/relay/service"
)

func main() {
	identityEndpoint := os.Getenv("IDENTITY_GRPC_ENDPOINT")
	if identityEndpoint == "" {
		identityEndpoint = "127.0.0.1:9001"
	}
	channelEndpoint := os.Getenv("CHANNEL_GRPC_ENDPOINT")
	if channelEndpoint == "" {
		channelEndpoint = "127.0.0.1:9002"
	}
	clients, err := data.NewData(identityEndpoint, channelEndpoint)
	if err != nil {
		panic(err)
	}
	uc := biz.NewRelayUsecase(clients.Identity, clients.Channel)
	svc := service.NewOpenAIService(uc)
	plan, err := svc.Plan(context.Background(), "demo-token", "gpt-4o-mini")
	if err != nil {
		panic(err)
	}
	fmt.Printf("relay plan: user=%d token=%d channel=%s base_url=%s\n", plan.Auth.UserID, plan.Auth.TokenID, plan.Channel.Name, plan.Channel.BaseURL)
}
