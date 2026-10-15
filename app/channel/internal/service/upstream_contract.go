package service

import (
	"context"
	v "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/contractdto"
)

func bindingProto(b biz.ContractBinding) *v.UpstreamContractBinding {
	return &v.UpstreamContractBinding{Scope: contractdto.ScopeProto(b.Scope), CurrentRevision: b.Current, PreviousRevision: b.Previous, Revision: b.Revision, IncidentRevision: b.IncidentRevision, Status: b.Status, GrayPercent: b.GrayPercent, StartedAt: b.StartedAt, CompleteAttempts: b.CompleteAttempts, Failures: b.Failures, CooldownUntil: b.CooldownUntil}
}
func (s *ChannelService) CreateUpstreamContract(ctx context.Context, p *v.CreateUpstreamContractRequest) (*v.CreateUpstreamContractReply, error) {
	ctx = authorization.WithWriteReason(ctx, p.GetReason())
	revision, err := s.uc.CreateUpstreamContract(ctx, contractdto.Document(p.GetDocument()), p.GetReason())
	if err != nil {
		return nil, err
	}
	return &v.CreateUpstreamContractReply{Revision: revision}, nil
}
func (s *ChannelService) ListUpstreamContracts(ctx context.Context, p *v.ListUpstreamContractsRequest) (*v.ListUpstreamContractsReply, error) {
	rows, b, err := s.uc.ListUpstreamContracts(ctx, contractdto.Scope(p.GetScope()), int(p.GetPageSize()))
	if err != nil {
		return nil, err
	}
	reply := &v.ListUpstreamContractsReply{Binding: bindingProto(b)}
	for _, s := range rows {
		reply.Contracts = append(reply.Contracts, contractdto.SnapshotProto(s))
	}
	return reply, nil
}
func (s *ChannelService) TransitionUpstreamContract(ctx context.Context, p *v.TransitionUpstreamContractRequest) (*v.TransitionUpstreamContractReply, error) {
	ctx = authorization.WithWriteReason(ctx, p.GetReason())
	ctx = authorization.WithExpectedResourceRevision(ctx, p.GetExpectedRevision())
	b, err := s.uc.TransitionUpstreamContract(ctx, biz.ContractTransition{AutomaticUntil: p.GetAutomaticUntil(), Scope: contractdto.Scope(p.GetScope()), Revision: p.GetRevision(), Expected: p.GetExpectedRevision(), Action: p.GetAction(), Reason: p.GetReason(), GrayPercent: p.GetGrayPercent()})
	if err != nil {
		return nil, err
	}
	return &v.TransitionUpstreamContractReply{Binding: bindingProto(b)}, nil
}
func (s *ChannelService) AcquireContractProbe(ctx context.Context, p *v.AcquireContractProbeRequest) (*v.AcquireContractProbeReply, error) {
	lease, err := s.uc.AcquireContractProbe(ctx, contractdto.Scope(p.GetScope()), p.GetRevision(), p.GetAttemptId())
	if err != nil {
		return nil, err
	}
	return &v.AcquireContractProbeReply{LeaseId: lease.ID, Snapshot: contractdto.SnapshotProto(lease.Snapshot), SourceRevision: lease.SourceRevision}, nil
}
func (s *ChannelService) RecordContractResult(ctx context.Context, p *v.RecordContractResultRequest) (*v.RecordContractResultReply, error) {
	if len(p.GetRecordJson()) > 256<<10 {
		return nil, biz.ErrContractInvalid
	}
	var record evidence.Record
	if jsonx.Unmarshal(p.GetRecordJson(), &record) != nil {
		return nil, biz.ErrContractInvalid
	}
	ok, err := s.uc.RecordContractResult(ctx, biz.ContractResult{Scope: contractdto.Scope(p.GetScope()), Revision: p.GetRevision(), BindingRevision: p.GetBindingRevision(), IncidentRevision: p.GetIncidentRevision(), AttemptID: p.GetAttemptId(), LeaseID: p.GetLeaseId(), Record: &record, CapabilityValid: p.GetCapabilityValid()})
	if err != nil {
		return nil, err
	}
	return &v.RecordContractResultReply{Accepted: ok}, nil
}
