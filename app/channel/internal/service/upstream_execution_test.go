package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	channelv1 "micro-one-api/api/channel/v1"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/security/serviceidentity"
)

func upstreamSystemContext(caller, method string) context.Context {
	ctx := serviceidentity.WithPrincipal(context.Background(), serviceidentity.Principal{Name: caller, Dedicated: true})
	return serviceidentity.WithRPCMethod(ctx, method)
}

func TestClaimUpstreamExecutionRequiresRelayAndCurrentConfiguration(t *testing.T) {
	channel := &biz.Channel{ID: 7, Status: biz.ChannelStatusEnabled, Key: "secret"}
	s := NewChannelService(biz.NewChannelUsecase(&channelServiceRepo{channel: channel}, nil))
	req := &channelv1.ClaimUpstreamExecutionRequest{SourceKind: "channel", SourceId: 7, ConfigurationRevision: channel.UpstreamRevision(), UpstreamModelId: "model"}
	method := channelv1.ChannelService_ClaimUpstreamExecution_FullMethodName
	for _, caller := range []string{"monitor", "admin", "legacy-shared"} {
		if _, err := s.ClaimUpstreamExecution(upstreamSystemContext(caller, method), req); !errors.Is(err, authorization.ErrDenied) {
			t.Fatalf("%s claim error=%v, want denied", caller, err)
		}
	}
	if _, err := s.ClaimUpstreamExecution(upstreamSystemContext("relay", channelv1.ChannelService_RecordUpstreamObservation_FullMethodName), req); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("different RPC capability admitted claim: %v", err)
	}
	ctx := upstreamSystemContext("relay", method)
	reply, err := s.ClaimUpstreamExecution(ctx, req)
	if err != nil || reply.GetConfigurationRevision() != channel.UpstreamRevision() {
		t.Fatalf("relay claim=%+v err=%v", reply, err)
	}
	req.ConfigurationRevision = ""
	if _, err := s.ClaimUpstreamExecution(ctx, req); err == nil {
		t.Fatal("missing configuration revision accepted")
	}
	req.ConfigurationRevision = strings.Repeat("0", 64)
	if _, err := s.ClaimUpstreamExecution(ctx, req); err == nil {
		t.Fatal("stale configuration revision accepted")
	}
}

func TestMonitorObservationCannotAuthorizeDataPlaneRecovery(t *testing.T) {
	s := NewChannelService(nil)
	ctx := upstreamSystemContext("monitor", channelv1.ChannelService_RecordUpstreamObservation_FullMethodName)
	for _, outcome := range []int32{0, 1, 2} {
		if _, err := s.RecordUpstreamObservation(ctx, &channelv1.RecordUpstreamObservationRequest{SourceKind: "channel", SourceId: 7, Outcome: outcome}); !errors.Is(err, authorization.ErrDenied) {
			t.Fatalf("monitor data-plane outcome %d error=%v", outcome, err)
		}
	}
	reply, err := s.RecordUpstreamObservation(ctx, &channelv1.RecordUpstreamObservationRequest{SourceKind: "channel", SourceId: 7, FailureKind: "probe"})
	if err != nil || reply.GetAccepted() {
		t.Fatalf("diagnostic probe affected health: %+v %v", reply, err)
	}
}
