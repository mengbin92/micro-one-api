package service

import (
	"context"
	"fmt"
	relayv1 "micro-one-api/api/relay/v1"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func (s *RelayGrpcService) SetUpstreamProbe(u *relaybiz.UpstreamProbeUsecase) { s.upstreamProbe = u }
func (s *RelayGrpcService) ProbeUpstream(ctx context.Context, req *relayv1.ProbeUpstreamRequest) (*relayv1.ProbeUpstreamResponse, error) {
	if s.upstreamProbe == nil || req == nil {
		return nil, fmt.Errorf("upstream probe unavailable")
	}
	r, err := s.upstreamProbe.Probe(ctx, req.TargetId, req.AttemptId)
	if err != nil {
		return nil, err
	}
	b, err := jsonx.Marshal(r)
	if err != nil {
		return nil, err
	}
	return &relayv1.ProbeUpstreamResponse{ResultJson: b}, nil
}
