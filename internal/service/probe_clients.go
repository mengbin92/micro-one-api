package service

import (
	"context"
	"fmt"
	channelv1 "micro-one-api/api/channel/v1"
	monitorv1 "micro-one-api/api/monitor/v1"
	relaybiz "micro-one-api/internal/biz"
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
		return relaybiz.ProbeSource{Channel: &relaybiz.Channel{ID: c.Id, Type: c.Type, BaseURL: c.BaseUrl, Key: c.Key, Config: relaybiz.ChannelConfig{APIVersion: c.GetConfig().GetApiVersion()}}}, nil
	}
	r, err := s.client.GetSubscriptionAccount(ctx, &channelv1.GetSubscriptionAccountRequest{AccountId: id})
	if err != nil {
		return relaybiz.ProbeSource{}, err
	}
	c := r.GetAccount()
	if c == nil {
		return relaybiz.ProbeSource{}, fmt.Errorf("probe source unavailable")
	}
	return relaybiz.ProbeSource{Account: &relaybiz.SubscriptionAccount{ID: c.Id, Platform: c.Platform, AccountType: c.AccountType, BaseURL: c.BaseUrl, AccessToken: c.AccessToken, AccountID: c.AccountId, Group: c.Group, Fingerprint: c.Fingerprint}}, nil
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
