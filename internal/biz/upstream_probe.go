package biz

import (
	"context"
	"fmt"
	"micro-one-api/domain/execution"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/platform/security/serviceidentity"
	"time"
)

type ProbeSource struct {
	Revision string
	Channel  *Channel
	Account  *SubscriptionAccount
}
type ProbeSourceClient interface {
	GetProbeSource(context.Context, string, int64) (ProbeSource, error)
	RecordProbeInventory(context.Context, string, int64, []string, string, int64, bool) error
}

type ProbePermitClient interface {
	ClaimProbePermit(context.Context, string, string, string) (bool, error)
}

type UpstreamProbeRepo interface {
	Probe(context.Context, execution.ProbeTarget, string) (execution.ProbeResult, error)
}
type UpstreamProbeUsecase struct {
	permit  ProbePermitClient
	repo    UpstreamProbeRepo
	targets map[string]execution.ProbeTarget
}

func NewUpstreamProbeUsecase(repo UpstreamProbeRepo, targets []execution.ProbeTarget) *UpstreamProbeUsecase {
	u := &UpstreamProbeUsecase{repo: repo, targets: map[string]execution.ProbeTarget{}}
	for _, t := range targets {
		u.targets[t.ID] = t
	}
	return u
}
func (u *UpstreamProbeUsecase) Probe(ctx context.Context, targetID, attemptID string) (execution.ProbeResult, error) {
	const method = "/api.relay.v1.RelayService/ProbeUpstream"
	if u == nil || !serviceidentity.HasSystemCapability(ctx, method) {
		return execution.ProbeResult{}, fmt.Errorf("probe system capability required")
	}
	t, ok := u.targets[targetID]
	if !ok || attemptID == "" || len(attemptID) > 128 {
		return execution.ProbeResult{}, fmt.Errorf("probe target or attempt invalid")
	}
	if t.Expired(time.Now()) {
		return execution.ProbeResult{}, fmt.Errorf("probe target expired")
	}
	if u.permit == nil {
		return execution.ProbeResult{}, fmt.Errorf("persistent probe permit unavailable")
	}
	permitCtx, cancelPermit := context.WithTimeout(ctx, 5*time.Second)
	allowed, err := u.permit.ClaimProbePermit(permitCtx, t.ID, attemptID, t.Digest())
	cancelPermit()
	if err != nil {
		return execution.ProbeResult{}, err
	}
	if !allowed {
		return execution.ProbeResult{}, fmt.Errorf("probe budget permit denied")
	}
	call, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return u.repo.Probe(call, t, attemptID)
}

func (u *UpstreamProbeUsecase) SetProbePermitClient(p ProbePermitClient) { u.permit = p }

type ContractProbeClient interface {
	AcquireContractProbe(context.Context, contract.Scope, string, string) (contract.ProbeLease, error)
	ReportContractProbe(context.Context, contract.Scope, contract.ProbeLease, string, []byte, bool) error
}
