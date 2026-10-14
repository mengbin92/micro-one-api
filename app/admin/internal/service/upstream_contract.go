package service

import (
	"context"
	v "micro-one-api/api/channel/v1"
)

func (s *AdminService) CreateUpstreamContract(ctx context.Context, p *v.CreateUpstreamContractRequest) (*v.CreateUpstreamContractReply, error) {
	return s.channelClient.CreateUpstreamContract(operatorRPCContext(ctx), p)
}
func (s *AdminService) ListUpstreamContracts(ctx context.Context, p *v.ListUpstreamContractsRequest) (*v.ListUpstreamContractsReply, error) {
	return s.channelClient.ListUpstreamContracts(operatorRPCContext(ctx), p)
}
func (s *AdminService) TransitionUpstreamContract(ctx context.Context, p *v.TransitionUpstreamContractRequest) (*v.TransitionUpstreamContractReply, error) {
	return s.channelClient.TransitionUpstreamContract(operatorRPCContext(ctx), p)
}
