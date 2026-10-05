// Package management defines transport-independent IAM objects shared by the
// identity owner and admin RPC adapter. It contains no storage or credentials.
package management

import (
	"micro-one-api/domain/authorization"
	"time"
)

type Grant struct {
	Operation string
	Effect    authorization.Effect
	Scope     authorization.Scope
}
type Role struct {
	ID                   int64
	Context              authorization.Context
	Code                 string
	Name, Description    string
	Builtin              bool
	MaxMembers           *int64
	Status               string
	Revision             uint64
	CreationDelegationID int64
	Inherits             []int64 // senior -> junior
	Grants               []Grant
}
type Assignment struct {
	ID, UserID, RoleID int64
	Context            authorization.Context
	Boundary           authorization.Scope
	Validity           authorization.Interval
	Revoked            bool
	Origin             string // legacy_candidate/default/bootstrap/explicit; no username inference.
	MigrationBatchID   string
	Revision           uint64
	AssignedBy         int64
}

type Limits struct {
	MaxRolesPerUser, MaxRolesPerSession *int64
}
type Constraint struct {
	ID         int64
	Context    authorization.Context
	Kind, Name string
	RoleIDs    []int64
	MaxCount   int64
	Enabled    bool
	Revision   uint64
}
type Session struct {
	SessionID                   string
	UserID                      int64
	Context                     authorization.Context
	ExpiresAt                   time.Time
	RevokedAt, ContextRevokedAt *time.Time
	ActivationState             string
	ActiveRoleIDs               []int64
	SessionRevision, Revision   uint64
}
type State struct {
	Context     authorization.Context
	Roles       []Role
	Assignments []Assignment
	Constraints []Constraint
	Sessions    []Session
	Limits      Limits
}
type Conflict struct {
	ConstraintName       string
	ProposedConstraint   bool
	Kind                 string
	ConstraintID, RoleID int64
	UserIDs, RoleIDs     []int64
	SessionIDs           []string
	Actual, Limit        int64
	Validity             authorization.Interval
}

type Ceiling struct {
	Context   authorization.Context
	Operation string
	Scope     authorization.Scope
}
type Delegation struct {
	ID, ManagerRoleID, TargetRoleID int64
	Context                         authorization.Context
	TargetKind                      string
	Actions                         []string
	TargetUserScope                 authorization.Scope
	GrantCeiling                    []Ceiling
	CanRedelegate                   bool
	Validity                        authorization.Interval
	Revision                        uint64
}
type Permission struct {
	SupportedScopes                                  []authorization.ScopeKind
	ContextTypes                                     []string
	Protected                                        bool
	ID, ResourceID                                   int64
	Code, Name, Category, RiskLevel, Status, Binding string
	Description                                      string
	Sort                                             int32
	Revision                                         uint64
}
type Resource struct {
	ID                int64
	Code, Name, Owner string
	Enabled           bool
}
type Menu struct {
	ID, ParentID             int64
	RouteKey, Name, IconKey  string
	Sort                     int32
	Enabled                  bool
	RequiredAll, RequiredAny []string
	Revision                 uint64
}
type Reference struct {
	Kind       string
	ID, UserID int64
}
type Audit struct {
	EventID                                                        string
	Actor                                                          authorization.Actor
	Context, TargetContext                                         authorization.Context
	Action, Target, Before, After, Diff, Result, RequestID, Reason string
	Versions                                                       authorization.Versions
	OccurredAt                                                     time.Time
}
type Request struct {
	Context                                                      authorization.Context
	ID, UserID, SourceID                                         int64
	EventID                                                      string
	Role                                                         *Role
	Permission                                                   *Permission
	Delegation                                                   *Delegation
	Constraint                                                   *Constraint
	Menu                                                         *Menu
	Assignment                                                   *Assignment
	Assignments                                                  []Assignment
	Grants                                                       []Grant
	RoleIDs                                                      []int64
	ExpectedRevision, ExpectedPolicyRevision                     uint64
	BasePolicyRevision                                           uint64
	ContentDigest, Reason, RequestID, Filter, OrderBy, PageToken string
	PageSize                                                     int32
	UpdateMask                                                   []string
	Operation                                                    string
	Object                                                       authorization.ObjectFacts
}
type Response struct {
	TargetRevision                   uint64
	AuthorizationMode                string
	LegacyAdmin                      bool
	PermittedOperations              []string
	Impacts                          []Impact
	Sessions                         []Session
	Roles                            []Role
	Permissions                      []Permission
	Resources                        []Resource
	Delegations                      []Delegation
	Constraints                      []Constraint
	Menus                            []Menu
	Assignments                      []Assignment
	Audits                           []Audit
	References                       []Reference
	Conflicts                        []Conflict
	Sources                          []authorization.GrantSource
	Decision                         *authorization.Decision
	Versions                         authorization.Versions
	Session                          *Session
	ValidUntil                       *time.Time
	AuthorizedRoleIDs, ActiveRoleIDs []int64
	AffectedUserIDs                  []int64
	BasePolicyRevision               uint64
	ContentDigest, NextPageToken     string
	Total                            int64
}

type Impact struct {
	UserID        int64
	Before, After []authorization.GrantSource
}
