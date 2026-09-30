package data

import (
	"time"

	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
)

type iamPolicyModel struct {
	ID                                              int
	PolicyRevision, CatalogRevision                 uint64
	AuthorizationMode, CutoverState, CutoverBatchID string
	CutoverVerifiedAt                               *int64
}

func (iamPolicyModel) TableName() string { return "iam_policy_state" }
func (p iamPolicyModel) toBiz() authorization.PolicyState {
	s := authorization.PolicyState{Mode: p.AuthorizationMode, Cutover: p.CutoverState, BatchID: p.CutoverBatchID, PolicyRevision: p.PolicyRevision, CatalogRevision: p.CatalogRevision}
	if p.CutoverVerifiedAt != nil {
		t := time.UnixMilli(*p.CutoverVerifiedAt).UTC()
		s.VerifiedAt = &t
	}
	return s
}

type iamRoleModel struct {
	ID                                          int64
	ContextType                                 string
	OrganizationID                              int64
	ContextKey, Code, Name, Description, Status string
	Builtin                                     int32
	MaxMembers                                  *int64
	CreationDelegationID                        *int64
	Revision                                    uint64
}

func (iamRoleModel) TableName() string { return "iam_roles" }
func newIAMRole(r biz.IAMRole) iamRoleModel {
	p := iamRoleModel{ID: r.ID, ContextType: r.Context.Type, OrganizationID: r.Context.OrganizationID, ContextKey: r.Context.Key, Code: r.Code, Name: r.Name, Description: r.Description, Status: r.Status, Builtin: boolInt(r.Builtin), MaxMembers: r.MaxMembers, Revision: r.Revision}
	if r.CreationDelegationID != 0 {
		p.CreationDelegationID = &r.CreationDelegationID
	}
	return p
}
func (p iamRoleModel) toBiz() biz.IAMRole {
	r := biz.IAMRole{ID: p.ID, Context: authorization.Context{Type: p.ContextType, OrganizationID: p.OrganizationID, Key: p.ContextKey}, Code: p.Code, Name: p.Name, Description: p.Description, Status: p.Status, Builtin: p.Builtin != 0, MaxMembers: p.MaxMembers, Revision: p.Revision}
	if p.CreationDelegationID != nil {
		r.CreationDelegationID = *p.CreationDelegationID
	}
	return r
}

type iamAssignmentModel struct {
	ID, UserID, RoleID, MembershipID int64
	ContextKey, AllowBoundary        string
	StartsAt                         int64
	ExpiresAt                        *int64
	Status, Origin, MigrationBatchID string
	Revision                         uint64
	AssignedBy                       int64
}

func (iamAssignmentModel) TableName() string { return "iam_user_roles" }
func newIAMAssignment(a biz.IAMAssignment) (iamAssignmentModel, error) {
	b, err := jsonx.Marshal(a.Boundary)
	if err != nil {
		return iamAssignmentModel{}, biz.ErrIAMScopeInvalid
	}
	p := iamAssignmentModel{ID: a.ID, UserID: a.UserID, RoleID: a.RoleID, ContextKey: a.Context.Key, AllowBoundary: string(b), StartsAt: a.Validity.StartsAt.UnixMilli(), Status: "active", Origin: a.Origin, MigrationBatchID: a.MigrationBatchID, Revision: a.Revision, AssignedBy: a.AssignedBy}
	if a.Validity.ExpiresAt != nil {
		t := a.Validity.ExpiresAt.UnixMilli()
		p.ExpiresAt = &t
	}
	if a.Revoked {
		p.Status = "revoked"
	}
	return p, nil
}

var iamScopeKinds = []authorization.ScopeKind{authorization.All, authorization.Self, authorization.Users, authorization.Resources, authorization.Groups}

func (p iamAssignmentModel) toBiz() (biz.IAMAssignment, error) {
	scope, err := authorization.ParseScope([]byte(p.AllowBoundary), iamScopeKinds)
	if err != nil {
		return biz.IAMAssignment{}, biz.ErrIAMScopeInvalid
	}
	a := biz.IAMAssignment{ID: p.ID, UserID: p.UserID, RoleID: p.RoleID, Context: authorization.Platform(), Boundary: scope, Validity: authorization.Interval{StartsAt: time.UnixMilli(p.StartsAt).UTC()}, Revoked: p.Status == "revoked", Origin: p.Origin, MigrationBatchID: p.MigrationBatchID, Revision: p.Revision, AssignedBy: p.AssignedBy}
	if p.ExpiresAt != nil {
		t := time.UnixMilli(*p.ExpiresAt).UTC()
		a.Validity.ExpiresAt = &t
	}
	if p.ContextKey != "platform" {
		return biz.IAMAssignment{}, biz.ErrIAMContextInvalid
	}
	if a.Validity.Validate() != nil || authorization.ValidateOrigin(a.Origin, a.MigrationBatchID) != nil {
		return biz.IAMAssignment{}, biz.ErrIAMInvalidRelation
	}
	return a, nil
}

type iamAuditModel struct {
	EventID                                                                                                                                                string `gorm:"primaryKey"`
	ActorUserID                                                                                                                                            int64
	ActorServiceID, ActorSessionID, ContextKey, TargetContextKey, Action, Target, BeforeData, AfterData, Diff, Result, DecisionVersions, RequestID, Reason string
	OccurredAt                                                                                                                                             int64
}

func (iamAuditModel) TableName() string { return "iam_audit_events" }
func newIAMAudit(e biz.IAMAuditEvent) (iamAuditModel, error) {
	versions, err := jsonx.Marshal(e.Versions)
	if err != nil {
		return iamAuditModel{}, biz.ErrIAMInvalidRelation
	}
	return iamAuditModel{EventID: e.EventID, ActorUserID: e.Actor.UserID, ActorServiceID: e.Actor.ServiceID, ActorSessionID: e.Actor.SessionID, ContextKey: e.Context.Key, TargetContextKey: e.TargetContext.Key, Action: e.Action, Target: e.Target, BeforeData: e.Before, AfterData: e.After, Diff: e.Diff, Result: e.Result, DecisionVersions: string(versions), RequestID: e.RequestID, Reason: e.Reason, OccurredAt: e.OccurredAt.UnixMilli()}, nil
}
func (p iamAuditModel) toBiz() (biz.IAMAuditEvent, error) {
	e := biz.IAMAuditEvent{EventID: p.EventID, Actor: authorization.Actor{UserID: p.ActorUserID, ServiceID: p.ActorServiceID, SessionID: p.ActorSessionID}, Context: authorization.Platform(), TargetContext: authorization.Platform(), Action: p.Action, Target: p.Target, Before: p.BeforeData, After: p.AfterData, Diff: p.Diff, Result: p.Result, RequestID: p.RequestID, Reason: p.Reason, OccurredAt: time.UnixMilli(p.OccurredAt).UTC()}
	if p.ContextKey != "platform" || p.TargetContextKey != "platform" {
		return e, biz.ErrIAMContextInvalid
	}
	if err := jsonx.Unmarshal([]byte(p.DecisionVersions), &e.Versions); err != nil {
		return e, biz.ErrIAMInvalidRelation
	}
	return e, nil
}
