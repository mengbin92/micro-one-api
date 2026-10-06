package testutil

import (
	kgrpc "github.com/go-kratos/kratos/v3/transport/grpc"
	khttp "github.com/go-kratos/kratos/v3/transport/http"
	"gorm.io/gorm"
	billingv1 "micro-one-api/api/billing/v1"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/app/identity/internal/data"
	"micro-one-api/app/identity/internal/server"
	"micro-one-api/app/identity/internal/service"
)

// NewSelfBillingHTTP connects the real user adapters to a billing owner client.
func NewSelfBillingHTTP(uc *biz.IdentityUsecase, billing billingv1.BillingServiceClient) *khttp.Server {
	return server.NewHTTPServer(":0", uc, nil, billing)
}

// NewIAMStack builds the real identity adapters over a caller-owned scratch DB.
func NewIAMStack(db *gorm.DB) (*biz.IdentityUsecase, *kgrpc.Server, *khttp.Server) {
	repo := data.NewRoutingBackfillRepository(db)
	identity := biz.NewIdentityUsecase(repo, nil)
	identity.SetIAMRuntime(data.NewIAMRuntimeRepo(repo.Data), data.NewIAMTxRunner(repo.Data))
	governance := biz.NewIAMGovernanceUsecase(data.NewIAMManagementRepo(repo.Data), data.NewIAMTxRunner(repo.Data), identity)
	iam := service.NewIAMService(governance, identity)
	return identity, server.NewGRPCServer(":0", service.NewIdentityService(identity), iam), server.NewHTTPServer(":0", identity, nil)
}
