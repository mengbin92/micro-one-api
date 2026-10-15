package service

import (
	"context"
	"fmt"
	channelv1 "micro-one-api/api/channel/v1"
	monitorv1 "micro-one-api/api/monitor/v1"
	"micro-one-api/domain/upstream/contract"
	relaybiz "micro-one-api/internal/biz"
	"micro-one-api/platform/contractdto"
)

type probePermitClient struct {
	client monitorv1.MonitorServiceClient
}

func NewProbePermitClient(c monitorv1.MonitorServiceClient) relaybiz.ProbePermitClient {
	return &probePermitClient{c}
}
func (c *probePermitClient) ClaimProbePermit(ctx context.Context, target, attempt, digest string) (bool, error) {
	r, err := c.client.ClaimProbePermit(ctx, &monitorv1.ClaimProbePermitRequest{TargetId: target, AttemptId: attempt, TargetSha256: digest})
	if err != nil {
		return false, err
	}
	return r != nil && r.Allowed, nil
}

type probeSourceClient struct {
	client channelv1.ChannelServiceClient
}

func NewProbeSourceClient(c channelv1.ChannelServiceClient) relaybiz.ProbeSourceClient {
	return &probeSourceClient{c}
}
func (s *probeSourceClient) GetProbeSource(ctx context.Context, kind string, id int64) (relaybiz.ProbeSource, error) {
	if kind == "channel" {
		r, err := s.client.GetChannel(ctx, &channelv1.GetChannelRequest{ChannelId: id})
		if err != nil {
			return relaybiz.ProbeSource{}, err
		}
		c := r.GetChannel()
		if c == nil {
			return relaybiz.ProbeSource{}, fmt.Errorf("probe source unavailable")
		}
		return relaybiz.ProbeSource{Revision: c.GetUpstreamConfigurationRevision(), Channel: &relaybiz.Channel{ID: c.Id, Type: c.Type, BaseURL: c.BaseUrl, Key: c.Key, Config: relaybiz.ChannelConfig{APIVersion: c.GetConfig().GetApiVersion()}}}, nil
	}
	r, err := s.client.GetSubscriptionAccount(ctx, &channelv1.GetSubscriptionAccountRequest{AccountId: id})
	if err != nil {
		return relaybiz.ProbeSource{}, err
	}
	c := r.GetAccount()
	if c == nil {
		return relaybiz.ProbeSource{}, fmt.Errorf("probe source unavailable")
	}
	return relaybiz.ProbeSource{Revision: c.GetUpstreamConfigurationRevision(), Account: &relaybiz.SubscriptionAccount{ID: c.Id, Platform: c.Platform, AccountType: c.AccountType, BaseURL: c.BaseUrl, AccessToken: c.AccessToken, AccountID: c.AccountId, Group: c.Group, Fingerprint: c.Fingerprint}}, nil
}
func (s *probeSourceClient) RecordProbeInventory(ctx context.Context, kind string, id int64, models []string, attempt string, at int64, manual bool) error {
	r, err := s.client.RecordUpstreamInventory(ctx, &channelv1.RecordUpstreamInventoryRequest{SourceKind: kind, SourceId: id, ExactModelIds: models, AttemptId: attempt, ObservedAt: at, Manual: manual})
	if err != nil {
		return err
	}
	if r == nil || !r.Received {
		return fmt.Errorf("inventory receipt unavailable")
	}
	return nil
}

func (s *probeSourceClient) AcquireContractProbe(ctx context.Context, scope contract.Scope, revision, attempt string) (contract.ProbeLease, error) {
	p, err := s.client.AcquireContractProbe(ctx, &channelv1.AcquireContractProbeRequest{Scope: contractdto.ScopeProto(scope), Revision: revision, AttemptId: attempt})
	if err != nil {
		return contract.ProbeLease{}, err
	}
	snap, err := contractdto.Snapshot(p.GetSnapshot())
	return contract.ProbeLease{ID: p.GetLeaseId(), Snapshot: snap, SourceRevision: p.GetSourceRevision()}, err
}
func (s *probeSourceClient) ReportContractProbe(ctx context.Context, scope contract.Scope, lease contract.ProbeLease, attempt string, raw []byte, valid bool) error {
	_, err := s.client.RecordContractResult(ctx, &channelv1.RecordContractResultRequest{Scope: contractdto.ScopeProto(scope), Revision: lease.Snapshot.Revision, BindingRevision: lease.Snapshot.BindingRevision, IncidentRevision: lease.Snapshot.IncidentRevision, AttemptId: attempt, LeaseId: lease.ID, RecordJson: raw, CapabilityValid: valid})
	return err
}
