// Package authorization owns the fixed, storage-free authorization contract.
// These reference semantics are not installed in production entry points yet.
package authorization

import (
	"errors"
	"strconv"
	"time"
)

var (
	ErrContext              = errors.New("invalid authorization context")
	ErrOrganizationDisabled = errors.New("organization authorization is not enabled")
	ErrScope                = errors.New("invalid or unsupported authorization scope")
	ErrGraph                = errors.New("invalid role inheritance graph")
	ErrInterval             = errors.New("invalid authorization interval")
)

type Context struct {
	Type           string
	OrganizationID int64
	Key            string
}

func Platform() Context { return Context{Type: "platform", Key: "platform"} }

// Validate checks the canonical shape separately from feature availability.
func (c Context) Validate() error {
	if c == Platform() {
		return nil
	}
	if c.Type == "organization" && c.OrganizationID > 0 && c.Key == "organization:"+strconv.FormatInt(c.OrganizationID, 10) {
		return nil
	}
	return ErrContext
}

func (c Context) RequirePlatform() error {
	if err := c.Validate(); err != nil {
		return err
	}
	if c.Type != "platform" {
		return ErrOrganizationDisabled
	}
	return nil
}

type ScopeKind string

const (
	All       ScopeKind = "all"
	Self      ScopeKind = "self"
	Users     ScopeKind = "user_ids"
	Resources ScopeKind = "resource_ids"
	Groups    ScopeKind = "routing_group_ids"
)

// Scope is a finite union; conditions inside each clause are conjunctive.
// Empty Scope matches nothing. An empty clause is invalid, never implicit all.
type Scope struct {
	Clauses []Clause `json:"clauses"`
}
type Clause struct {
	All             bool    `json:"all,omitempty"`
	Self            bool    `json:"self,omitempty"`
	UserIDs         []int64 `json:"user_ids,omitempty"`
	ResourceIDs     []int64 `json:"resource_ids,omitempty"`
	RoutingGroupIDs []int64 `json:"routing_group_ids,omitempty"`
}

type Effect string

const (
	Allow Effect = "allow"
	Deny  Effect = "deny"
)

type Interval struct {
	StartsAt  time.Time
	ExpiresAt *time.Time // nil is infinite; [start, end) in UTC.
}

func (i Interval) Validate() error {
	if i.StartsAt.IsZero() || (i.ExpiresAt != nil && !i.StartsAt.Before(*i.ExpiresAt)) {
		return ErrInterval
	}
	return nil
}
func (i Interval) Contains(now time.Time) bool {
	return i.Validate() == nil && !now.Before(i.StartsAt) && (i.ExpiresAt == nil || now.Before(*i.ExpiresAt))
}

type Actor struct {
	UserID        int64
	ServiceID     string
	SessionID     string // verified, nonempty JWT JTI; never user ID.
	PasswordEpoch int64
	ExpiresAt     time.Time
}
type Versions struct {
	User           uint64
	Policy         uint64
	Catalog        uint64
	Session        uint64
	SessionContext uint64
	Organization   uint64
	Membership     uint64
	Unit           uint64
}
type ObjectFacts struct {
	Context         Context
	ResourceID      int64 // zero for creates; resource-ID grants cannot match.
	OwnerUserID     int64
	RoutingGroupIDs []int64 // owner-verified original + target groups on moves.
}
type GrantSource struct {
	Context            Context
	Operation          string
	Effect             Effect
	AssignmentID       int64
	RoleID             int64
	InheritancePath    []int64
	RoleScope          Scope
	AssignmentBoundary Scope
	Validity           Interval
	Active             bool // allow only; mandatory deny ignores activation and boundary.
}
type Decision struct {
	Allowed    bool
	Reason     string
	Context    Context
	Operation  string
	Versions   Versions
	ValidUntil *time.Time
	Sources    []GrantSource
}
