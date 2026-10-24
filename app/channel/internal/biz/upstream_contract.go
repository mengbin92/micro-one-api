package biz

import (
	"context"
	"math"
	"strings"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	v "micro-one-api/api/channel/v1"
	"micro-one-api/domain/authorization"
	"micro-one-api/domain/upstream/contract"
	"micro-one-api/domain/upstream/evidence"
	"micro-one-api/platform/security/serviceidentity"
)

var (
	ErrContractInvalid     = errors.BadRequest(v.UpstreamContractErrorReason_UPSTREAM_CONTRACT_INVALID.String(), "invalid upstream contract")
	ErrContractConflict    = errors.Conflict(v.UpstreamContractErrorReason_UPSTREAM_CONTRACT_CONFLICT.String(), "contract binding revision changed")
	ErrContractUnavailable = errors.ServiceUnavailable(v.UpstreamContractErrorReason_UPSTREAM_CONTRACT_UNAVAILABLE.String(), "contract persistence unavailable")
	ErrContractGate        = errors.Conflict(v.UpstreamContractErrorReason_UPSTREAM_CONTRACT_GATE_PENDING.String(), "independent contract validation or promotion gate pending")
)

type UpstreamContract struct {
	Document contract.Document
	Revision string
}
type ContractBinding struct {
	Scope                                                contract.Scope
	Current, Previous                                    string
	Revision, IncidentRevision                           int64
	Status                                               string
	GrayPercent                                          int32
	StartedAt, CompleteAttempts, Failures, CooldownUntil int64
}
type ContractTransition struct {
	AutomaticUntil           int64
	Scope                    contract.Scope
	Revision, Action, Reason string
	Expected                 uint64
	GrayPercent              int32
}
type ContractProbeLease struct {
	ID             string
	Snapshot       contract.Snapshot
	SourceRevision string
}
type ContractResult struct {
	Scope                             contract.Scope
	Revision                          string
	BindingRevision, IncidentRevision int64
	AttemptID, LeaseID                string
	Record                            *evidence.Record
	CapabilityValid                   bool
}
type UpstreamContractRepo interface {
	CreateContract(context.Context, UpstreamContract, time.Time) error
	GetContract(context.Context, contract.Scope, string) (contract.Snapshot, error)
	ListContracts(context.Context, contract.Scope, int) ([]contract.Snapshot, ContractBinding, error)
	TransitionContract(context.Context, ContractTransition, time.Time) (ContractBinding, error)
	ResolveContracts(context.Context, string, int64, string, string) ([]contract.Snapshot, []string, string, error)
	AcquireContractProbe(context.Context, contract.Scope, string, string, string, time.Time) (ContractProbeLease, error)
	RecordContractResult(context.Context, ContractResult, time.Time) (bool, error)
}

func (uc *ChannelUsecase) contracts() (UpstreamContractRepo, error) {
	r, ok := uc.repo.(UpstreamContractRepo)
	if !ok {
		return nil, ErrContractUnavailable
	}
	return r, nil
}
func (uc *ChannelUsecase) contractAuth(ctx context.Context, op, reason string) (context.Context, error) {
	ctx, err := uc.authorize(ctx, "channel.contracts", op)
	if err != nil {
		return ctx, err
	}
	if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
		if err := authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
			return ctx, err
		}
	}
	if reason != "" {
		ctx = authorization.WithWriteReason(ctx, reason)
	}
	return ctx, nil
}
func (uc *ChannelUsecase) CreateUpstreamContract(ctx context.Context, d contract.Document, reason string) (string, error) {
	if strings.TrimSpace(reason) == "" {
		return "", ErrContractInvalid
	}
	if _, err := contract.Compile(d); err != nil {
		return "", errors.BadRequest(v.UpstreamContractErrorReason_UPSTREAM_CONTRACT_INVALID.String(), err.Error())
	}
	ctx, err := uc.contractAuth(ctx, "channel.upstream_contract.create", reason)
	if err != nil {
		return "", err
	}
	r, err := uc.contracts()
	if err != nil {
		return "", err
	}
	rev := d.Revision()
	return rev, r.CreateContract(ctx, UpstreamContract{Document: d, Revision: rev}, uc.now())
}
func (uc *ChannelUsecase) ListUpstreamContracts(ctx context.Context, s contract.Scope, limit int) ([]contract.Snapshot, ContractBinding, error) {
	if s.Validate() != nil {
		return nil, ContractBinding{}, ErrContractInvalid
	}
	ctx, err := uc.contractAuth(ctx, "channel.upstream_contract.read", "")
	if err != nil {
		return nil, ContractBinding{}, err
	}
	r, err := uc.contracts()
	if err != nil {
		return nil, ContractBinding{}, err
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return r.ListContracts(ctx, s, limit)
}
func (uc *ChannelUsecase) TransitionUpstreamContract(ctx context.Context, t ContractTransition) (ContractBinding, error) {
	if t.AutomaticUntil < 0 || t.AutomaticUntil > uc.now().Add(24*time.Hour).Unix() || t.Scope.Validate() != nil || t.Expected > math.MaxInt64 || strings.TrimSpace(t.Reason) == "" || len(t.Reason) > 1024 {
		return ContractBinding{}, ErrContractInvalid
	}
	op := "channel.upstream_contract.publish"
	switch t.Action {
	case "publish", "promote", "rollback", "recover":
	case "quarantine":
		op = "channel.upstream_contract.quarantine"
	default:
		return ContractBinding{}, ErrContractInvalid
	}
	ctx, err := uc.contractAuth(ctx, op, t.Reason)
	if err != nil {
		return ContractBinding{}, err
	}
	r, err := uc.contracts()
	if err != nil {
		return ContractBinding{}, err
	}
	return r.TransitionContract(ctx, t, uc.now())
}
func (uc *ChannelUsecase) AcquireContractProbe(ctx context.Context, s contract.Scope, revision, attempt string) (ContractProbeLease, error) {
	if !serviceidentity.HasSystemCapability(ctx, "/api.channel.v1.ChannelService/AcquireContractProbe") {
		return ContractProbeLease{}, authorization.ErrDenied
	}
	if s.Validate() != nil || len(revision) != 64 || attempt == "" || len(attempt) > 128 {
		return ContractProbeLease{}, ErrContractInvalid
	}
	source, err := uc.contractSourceRevision(ctx, s)
	if err != nil {
		return ContractProbeLease{}, err
	}
	r, err := uc.contracts()
	if err != nil {
		return ContractProbeLease{}, err
	}
	return r.AcquireContractProbe(ctx, s, revision, attempt, source, uc.now())
}
func (uc *ChannelUsecase) contractSourceRevision(ctx context.Context, s contract.Scope) (string, error) {
	if s.Kind == "channel" {
		c, err := uc.repo.FindByID(ctx, s.ID)
		if err != nil {
			return "", err
		}
		if c == nil {
			return "", ErrContractInvalid
		}
		return c.UpstreamRevision(), nil
	}
	c, err := uc.repo.FindSubscriptionAccountByID(ctx, s.ID)
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", ErrContractInvalid
	}
	return c.UpstreamRevision(), nil
}
func (uc *ChannelUsecase) RecordContractResult(ctx context.Context, r ContractResult) (bool, error) {
	if !serviceidentity.HasSystemCapability(ctx, "/api.channel.v1.ChannelService/RecordContractResult") {
		return false, authorization.ErrDenied
	}
	if r.Scope.Validate() != nil || len(r.Revision) != 64 || r.AttemptID == "" || len(r.AttemptID) > 128 || r.Record == nil || r.Record.Observation.Protocol != r.Scope.Protocol {
		return false, ErrContractInvalid
	}
	repo, err := uc.contracts()
	if err != nil {
		return false, err
	}
	snapshot, err := repo.GetContract(ctx, r.Scope, r.Revision)
	if err != nil {
		return false, err
	}
	// Select the proof parser from the stored contract, never a producer-supplied
	// revision. Re-parse raw usage independently before accepting claimed success.
	if proof := r.Record.ContractProof; proof != nil {
		trace := r.Record.ParameterContract
		if trace == nil || trace.BindingRevision != r.BindingRevision || trace.IncidentRevision != r.IncidentRevision {
			return false, ErrContractInvalid
		}
	}
	if evidence.ContractObservation(r.Record, snapshot.Document).Status == "verified" && evidence.ValidateContractObservation(r.Record, snapshot.Document) != nil {
		return false, ErrContractInvalid
	}
	return repo.RecordContractResult(ctx, r, uc.now())
}
