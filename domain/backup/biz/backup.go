// Package biz owns the managed-backup control plane. Backup artifacts and
// orchestration clients belong to data; no business rows are changed here.
package biz

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-kratos/kratos/v3/errors"
	v "micro-one-api/api/admin/v1"
	"micro-one-api/domain/authorization"
)

var (
	ErrInvalid     = errors.BadRequest(v.AuthorizationErrorReason_AUTHORIZATION_BUSINESS_RULE_VIOLATION.String(), "invalid backup policy or request")
	ErrConflict    = errors.Conflict(v.AuthorizationErrorReason_AUTHORIZATION_REVISION_CONFLICT.String(), "backup revision or request conflict")
	ErrUnavailable = errors.ServiceUnavailable(v.AuthorizationErrorReason_AUTHORIZATION_DEPENDENCY_UNAVAILABLE.String(), "managed backup unavailable")
)

type Policy struct {
	Revision      int64
	Enabled       bool
	IntervalHours int32
	Retain        int32
	RPOHours      int32
	RTOMinutes    int32
}

func DefaultPolicy() Policy {
	return Policy{Revision: 1, IntervalHours: 24, Retain: 7, RPOHours: 24, RTOMinutes: 60}
}
func (p Policy) Validate() error {
	if p.Revision < 1 || p.IntervalHours < 1 || p.IntervalHours > 168 || p.Retain < 2 || p.Retain > 90 || p.RPOHours < p.IntervalHours || p.RPOHours > 168 || p.RTOMinutes < 1 || p.RTOMinutes > 1440 {
		return ErrInvalid
	}
	return nil
}

type Job struct {
	ID         string
	Status     string
	Trigger    string
	Actor      string
	Reason     string
	CreatedAt  int64
	StartedAt  int64
	FinishedAt int64
	StorageID  string
	ObjectKey  string
	SHA256     string
	Bytes      int64
	ErrorCode  string
	Policy     Policy
}
type Event struct {
	ID     string
	JobID  string
	Action string
	Actor  string
	Reason string
	At     int64
}
type WorkerState struct {
	LastSeen         int64
	Phase, ErrorCode string
	TimeoutMinutes   int32
}

type Repo interface {
	WorkerState(context.Context) (WorkerState, error)
	Policy(context.Context) (Policy, error)
	UpdatePolicy(context.Context, Policy, string, string) (Policy, error)
	Enqueue(context.Context, string, string, string, time.Time) (Job, error)
	List(context.Context, int) ([]Job, error)
	Events(context.Context, int) ([]Event, error)
}

// WorkerRepo is used only by the host worker. The named MySQL connection lock
// spans scheduling, snapshot, upload and retention. A lost connection cancels work.
type WorkerRepo interface {
	Repo
	SetWorkerState(context.Context, WorkerState) error
	Lead(context.Context, func(context.Context) error) (bool, error)
	Claim(context.Context, time.Time) (*Job, error)
	Finish(context.Context, Job) error
	ExpireRunning(context.Context, time.Time) error
	Prune(context.Context, int, string, func(context.Context, Job) error) error
}

type Usecase struct {
	repo Repo
	auth authorization.Resolver
}

func NewUsecase(r Repo, a authorization.Resolver) *Usecase { return &Usecase{repo: r, auth: a} }
func (u *Usecase) prepare(ctx context.Context, write bool) (context.Context, string, error) {
	if u == nil || u.repo == nil {
		return ctx, "", ErrUnavailable
	}
	op := "system.option.read"
	if write {
		op = "system.option.security.update"
	}
	ctx, err := authorization.Prepare(ctx, u.auth, "admin.backups", op)
	if err != nil {
		return ctx, "", err
	}
	if err = authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform()}); err != nil {
		return ctx, "", err
	}
	reason := strings.TrimSpace(authorization.WriteReason(ctx))
	if write && (utf8.RuneCountInString(reason) < 3 || utf8.RuneCountInString(reason) > 240) {
		return ctx, "", ErrInvalid
	}
	return ctx, reason, nil
}
func (u *Usecase) Policy(ctx context.Context) (Policy, error) {
	ctx, _, err := u.prepare(ctx, false)
	if err != nil {
		return Policy{}, err
	}
	return u.repo.Policy(ctx)
}
func (u *Usecase) UpdatePolicy(ctx context.Context, p Policy, actor string) (Policy, error) {
	if err := p.Validate(); err != nil {
		return Policy{}, err
	}
	ctx, reason, err := u.prepare(ctx, true)
	if err != nil {
		return Policy{}, err
	}
	return u.repo.UpdatePolicy(ctx, p, actor, reason)
}

var requestID = regexp.MustCompile(`^[a-zA-Z0-9_-]{16,80}$`)

func (u *Usecase) Create(ctx context.Context, id, actor string) (Job, error) {
	if !requestID.MatchString(id) {
		return Job{}, ErrInvalid
	}
	ctx, reason, err := u.prepare(ctx, true)
	if err != nil {
		return Job{}, err
	}
	return u.repo.Enqueue(ctx, id, actor, reason, time.Now().UTC())
}
func (u *Usecase) List(ctx context.Context) ([]Job, error) {
	ctx, _, err := u.prepare(ctx, false)
	if err != nil {
		return nil, err
	}
	return u.repo.List(ctx, 50)
}
func (u *Usecase) Events(ctx context.Context) ([]Event, error) {
	ctx, _, err := u.prepare(ctx, false)
	if err != nil {
		return nil, err
	}
	return u.repo.Events(ctx, 100)
}

func (u *Usecase) WorkerState(ctx context.Context) (WorkerState, error) {
	ctx, _, err := u.prepare(ctx, false)
	if err != nil {
		return WorkerState{}, err
	}
	return u.repo.WorkerState(ctx)
}
