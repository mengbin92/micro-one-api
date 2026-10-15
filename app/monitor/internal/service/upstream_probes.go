package service

import (
	"context"
	"fmt"
	monitorv1 "micro-one-api/api/monitor/v1"
	relayv1 "micro-one-api/api/relay/v1"
	"micro-one-api/app/monitor/internal/biz"
	"micro-one-api/domain/execution"
	"micro-one-api/pkg/jsonx"
)

func (s *MonitorService) ClaimProbePermit(ctx context.Context, req *monitorv1.ClaimProbePermitRequest) (*monitorv1.ClaimProbePermitResponse, error) {
	allowed, err := s.uc.ClaimProbePermit(ctx, req.GetTargetId(), req.GetAttemptId(), req.GetTargetSha256())
	if err != nil {
		return nil, err
	}
	return &monitorv1.ClaimProbePermitResponse{Allowed: allowed}, nil
}

type upstreamProbeClient struct{ client relayv1.RelayServiceClient }

func NewUpstreamProbeClient(c relayv1.RelayServiceClient) biz.UpstreamProbeClient {
	return &upstreamProbeClient{c}
}
func (c *upstreamProbeClient) Probe(ctx context.Context, target, attempt string) (execution.ProbeResult, error) {
	resp, err := c.client.ProbeUpstream(ctx, &relayv1.ProbeUpstreamRequest{TargetId: target, AttemptId: attempt})
	if err != nil {
		return execution.ProbeResult{}, err
	}
	var result execution.ProbeResult
	if resp == nil || len(resp.ResultJson) > execution.MaxEventBytes || jsonx.Unmarshal(resp.ResultJson, &result) != nil {
		return result, fmt.Errorf("invalid probe reply")
	}
	if result.AttemptID != attempt || result.TargetID != target {
		return result, fmt.Errorf("probe reply identity mismatch")
	}
	return result, nil
}
