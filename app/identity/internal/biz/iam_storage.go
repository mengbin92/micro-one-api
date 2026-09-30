package biz

import (
	"context"
	"time"

	"micro-one-api/domain/authorization"
)

// IAMRepo is the storage seam. All relation writes require the caller's locked
// transaction; governance, mode gates and authorization remain biz responsibilities.
// A2 does not install this seam in any public management entry point.
type IAMRepo interface {
	User(context.Context, IAMTx, int64) (User, error)
	CountUsers(context.Context, IAMTx) (int64, error)
	CreateUser(context.Context, IAMTx, User) (User, error)
	CreateOAuthIdentity(context.Context, IAMTx, OAuthIdentity) (OAuthIdentity, error)
	Policy(context.Context, IAMTx) (authorization.PolicyState, error)
	AdvancePolicy(context.Context, IAMTx, uint64, bool) error
	UserRevision(context.Context, IAMTx, int64) (uint64, error)
	AdvanceUser(context.Context, IAMTx, int64, uint64) error
	Roles(context.Context, IAMTx, authorization.Context) ([]IAMRole, error)
	Assignments(context.Context, IAMTx, authorization.Context, int64) ([]IAMAssignment, error)
	SaveRole(context.Context, IAMTx, IAMRole, uint64) (IAMRole, error)
	SaveAssignment(context.Context, IAMTx, IAMAssignment, uint64) (IAMAssignment, error)
	AppendAudit(context.Context, IAMTx, IAMAuditEvent) error
	AuditEvents(context.Context, IAMTx, authorization.Context, int) ([]IAMAuditEvent, error)
	// Failure audits are appended only after rollback. Append failure never changes
	// the denied request into success; its error must be observable by the caller.
	AppendFailureAudit(context.Context, IAMAuditEvent) error
}

type IAMAuditEvent struct {
	EventID                                                        string
	Actor                                                          authorization.Actor
	Context, TargetContext                                         authorization.Context
	Action, Target, Before, After, Diff, Result, RequestID, Reason string
	Versions                                                       authorization.Versions
	OccurredAt                                                     time.Time
}
