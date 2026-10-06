package biz

import (
	"testing"
	"time"

	"github.com/go-kratos/kratos/v3/errors"
	"micro-one-api/domain/authorization"
)

func TestIAMInheritanceAndFutureCounterexamples(t *testing.T) {
	ctx := authorization.Platform()
	now := time.Now().UTC()
	all := authorization.Scope{Clauses: []authorization.Clause{{All: true}}}
	roles := map[int64]IAMRole{
		1: {ID: 1, Context: ctx, Status: "enabled", Inherits: []int64{2, 3}},
		2: {ID: 2, Context: ctx, Status: "enabled", Inherits: []int64{3}},
		3: {ID: 3, Context: ctx, Status: "enabled", Grants: []IAMGrant{{Operation: "channel.channel.read", Effect: authorization.Allow, Scope: all}, {Operation: "channel.channel.delete", Effect: authorization.Deny, Scope: all}}},
	}
	a := IAMAssignment{ID: 1, UserID: 7, RoleID: 1, Context: ctx, Boundary: all, Validity: authorization.Interval{StartsAt: now}}
	sources, err := IAMSources(ctx, roles, []IAMAssignment{a}, []int64{2})
	if err != nil || len(sources) != 4 {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}
	active, inactive := 0, 0
	for _, s := range sources {
		if s.Active {
			active++
		} else {
			inactive++
		}
		if s.AssignmentID != 1 || s.RoleID != 3 {
			t.Fatal(s)
		}
	}
	if active != 2 || inactive != 2 {
		t.Fatal("activation erased path boundaries")
	}
	r := roles[2]
	r.Status = "disabled"
	roles[2] = r
	sources, err = IAMSources(ctx, roles, []IAMAssignment{a}, []int64{1})
	if err != nil || len(sources) != 2 {
		t.Fatal("disabled node must cut only its path", err)
	}
	r = roles[3]
	r.Inherits = []int64{1}
	roles[3] = r
	if _, err = IAMSources(ctx, roles, []IAMAssignment{a}, nil); err != authorization.ErrGraph {
		t.Fatal("cycle accepted", err)
	}
	r.Inherits = []int64{99}
	roles[3] = r
	if _, err = IAMSources(ctx, roles, []IAMAssignment{a}, nil); err != authorization.ErrGraph {
		t.Fatal("missing role accepted", err)
	}
	r.Inherits = nil
	r.Context = authorization.Context{Type: "organization", OrganizationID: 1, Key: "organization:1"}
	roles[3] = r
	if _, err = IAMSources(ctx, roles, []IAMAssignment{a}, nil); err != authorization.ErrGraph {
		t.Fatal("cross-domain inheritance", err)
	}
	start, end := now.Add(24*time.Hour), now.Add(25*time.Hour)
	a.Validity = authorization.Interval{StartsAt: start, ExpiresAt: &end}
	b := a
	b.ID = 2
	b.UserID = 8
	if IAMMemberLimit(now, []IAMAssignment{a, b}, 1) == nil {
		t.Fatal("future overlap accepted")
	}
	b.UserID = a.UserID
	if err := IAMMemberLimit(now, []IAMAssignment{a, b}, 1); err != nil {
		t.Fatal("duplicate path counted", err)
	}
	b.UserID = 8
	b.Validity = authorization.Interval{StartsAt: end}
	if err := IAMMemberLimit(now, []IAMAssignment{a, b}, 1); err != nil {
		t.Fatal("adjacent/infinite interval rejected", err)
	}
	b.Validity = authorization.Interval{StartsAt: start, ExpiresAt: &start}
	if IAMMemberLimit(now, []IAMAssignment{b}, 1) == nil {
		t.Fatal("empty interval accepted")
	}
}

func TestIAMTypedErrorContract(t *testing.T) {
	for _, tt := range []struct {
		reason string
		code   int
	}{{"IDENTITY_INVALID", 401}, {"SESSION_INVALID", 401}, {"REVISION_CONFLICT", 409}, {"DENY_MATCHED", 403}, {"UNKNOWN", 403}} {
		err := errors.FromError(IAMDecisionError(authorization.Decision{Reason: tt.reason}))
		if int(err.Code) != tt.code {
			t.Fatal(err)
		}
	}
	if IAMDecisionError(authorization.Decision{Allowed: true}) != nil {
		t.Fatal("allowed decision errored")
	}
}
