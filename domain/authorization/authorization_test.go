package authorization

import (
	"testing"
	"time"
)

func TestScopeDecisionCounterexamples(t *testing.T) {
	now := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	all := Scope{Clauses: []Clause{{All: true}}}
	group := func(ids ...int64) Scope { return Scope{Clauses: []Clause{{RoutingGroupIDs: ids}}} }
	id := func(ids ...int64) Scope { return Scope{Clauses: []Clause{{ResourceIDs: ids}}} }
	source := func(effect Effect, scope, boundary Scope, active bool) GrantSource {
		return GrantSource{Context: Platform(), Operation: "channel.channel.update", Effect: effect, RoleScope: scope, AssignmentBoundary: boundary, Active: active, Validity: Interval{StartsAt: now}}
	}
	base := Input{Actor: Actor{UserID: 7, SessionID: "verified-jti", ExpiresAt: now.Add(time.Hour)}, Context: Platform(), Object: ObjectFacts{Context: Platform(), ResourceID: 42, RoutingGroupIDs: []int64{11, 12}}, Operation: Operation{Code: "channel.channel.update", Binding: "bound", ContextTypes: []string{"platform"}, Scopes: []ScopeKind{All, Self, Users, Resources, Groups}, WholeObject: true}, Now: now, IdentityValid: true, SessionValid: true, ConstraintsPass: true, BusinessRulesPass: true, OperationEnabled: true}
	for _, tt := range []struct {
		name        string
		sources     []GrantSource
		read, allow bool
	}{
		{"shared read", []GrantSource{source(Allow, group(11), all, true)}, true, true},
		{"partial write", []GrantSource{source(Allow, group(11), all, true)}, false, false},
		{"joint whole write", []GrantSource{source(Allow, group(11), all, true), source(Allow, group(12), all, true)}, false, true},
		{"inactive deny", []GrantSource{source(Allow, all, all, true), source(Deny, group(11), group(99), false)}, false, false},
		{"deny read", []GrantSource{source(Allow, id(42), all, true), source(Deny, group(11), all, false)}, true, false},
		{"ID cannot escape boundary", []GrantSource{source(Allow, id(42), group(11), true)}, false, false},
		{"sources cannot splice", []GrantSource{source(Allow, group(11), group(12), true), source(Allow, group(12), group(11), true)}, true, false},
		{"non-group filters preserved", []GrantSource{source(Allow, Scope{Clauses: []Clause{{ResourceIDs: []int64{99}, RoutingGroupIDs: []int64{11}}}}, all, true), source(Allow, group(12), all, true)}, false, false},
		{"inactive allow", []GrantSource{source(Allow, all, all, false)}, true, false},
		{"empty scope", []GrantSource{source(Allow, Scope{}, all, true)}, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.Sources = tt.sources
			if tt.read {
				in.Operation.Code = "channel.channel.read"
				for i := range in.Sources {
					in.Sources[i].Operation = in.Operation.Code
				}
			}
			if got := Decide(in); got.Allowed != tt.allow {
				t.Fatalf("got %+v", got)
			}
		})
	}
	for _, tt := range []struct {
		name   string
		change func(*Input)
		reason string
	}{
		{"unbound", func(i *Input) { i.Operation.Binding = "unbound" }, "OPERATION_UNBOUND"},
		{"unknown operation", func(i *Input) { i.Operation.Code = "identity.unknown.write" }, "OPERATION_UNBOUND"},
		{"disabled", func(i *Input) { i.OperationEnabled = false }, "OPERATION_DISABLED"},
		{"identity", func(i *Input) { i.IdentityValid = false }, "IDENTITY_INVALID"},
		{"missing JTI", func(i *Input) { i.Actor.SessionID = "" }, "IDENTITY_INVALID"},
		{"expired JWT", func(i *Input) { i.Actor.ExpiresAt = now }, "IDENTITY_INVALID"},
		{"session", func(i *Input) { i.SessionValid = false }, "SESSION_INVALID"},
		{"constraints", func(i *Input) { i.ConstraintsPass = false }, "CONSTRAINT_VIOLATION"},
		{"business rule", func(i *Input) { i.BusinessRulesPass = false }, "BUSINESS_RULE_VIOLATION"},
		{"organization", func(i *Input) { i.Context = Context{Type: "organization", OrganizationID: 1, Key: "organization:1"} }, "ORGANIZATION_DISABLED"},
		{"wrong object context", func(i *Input) { i.Object.Context = Context{} }, "CONTEXT_INVALID"},
		{"unsupported context", func(i *Input) { i.Operation.Code = "organization.member.list" }, "CONTEXT_INVALID"},
		{"invalid facts", func(i *Input) { i.Object.RoutingGroupIDs = []int64{-1} }, "SCOPE_INVALID"},
		{"unsupported scope", func(i *Input) { i.Sources[0].RoleScope = Scope{Clauses: []Clause{{UserIDs: []int64{7}}}} }, "SCOPE_INVALID"},
		{"empty clause", func(i *Input) { i.Sources[0].RoleScope = Scope{Clauses: []Clause{{}}} }, "SCOPE_INVALID"},
		{"bad ID", func(i *Input) { i.Sources[0].RoleScope = id(-1) }, "SCOPE_INVALID"},
		{"unknown effect", func(i *Input) { i.Sources[0].Effect = "maybe" }, "SCOPE_INVALID"},
		{"create with ID grant", func(i *Input) { i.Object.ResourceID = 0; i.Sources[0].RoleScope = id(42) }, "ALLOW_MISSING"},
		{"no groups", func(i *Input) { i.Object.RoutingGroupIDs = nil; i.Sources[0].RoleScope = group(11) }, "ALLOW_MISSING"},
		{"other operation", func(i *Input) { i.Sources[0].Operation = "channel.channel.read" }, "ALLOW_MISSING"},
		{"expiry half open", func(i *Input) { i.Sources[0].Validity = Interval{StartsAt: now.Add(-time.Hour), ExpiresAt: &now} }, "ALLOW_MISSING"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := base
			in.Sources = []GrantSource{source(Allow, all, all, true)}
			tt.change(&in)
			d := Decide(in)
			if d.Allowed || d.Reason != tt.reason {
				t.Fatalf("got %+v", d)
			}
		})
	}
	in := base
	future := source(Deny, group(11), all, false)
	future.Validity.StartsAt = now.Add(time.Minute)
	in.Sources = []GrantSource{source(Allow, all, all, true), future}
	if d := Decide(in); !d.Allowed || d.ValidUntil == nil || !d.ValidUntil.Equal(future.Validity.StartsAt) {
		t.Fatalf("future boundary lost: %+v", d)
	}
}

func TestContextCatalogAndCutoverContract(t *testing.T) {
	for _, c := range []Context{{}, {Type: "platform", OrganizationID: 1, Key: "platform"}, {Type: "organization", OrganizationID: 1, Key: "organization:01"}, {Type: "organization", OrganizationID: -1, Key: "organization:-1"}} {
		if c.Validate() == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
	if Platform().RequirePlatform() != nil {
		t.Fatal("platform rejected")
	}
	if (Context{Type: "organization", OrganizationID: 1, Key: "organization:1"}).RequirePlatform() != ErrOrganizationDisabled {
		t.Fatal("organization fallback")
	}
	if _, ok := Lookup("identity.user.*"); ok {
		t.Fatal("wildcard")
	}
	if len(Catalog()) < 200 {
		t.Fatal("incomplete catalog")
	}
	for _, o := range Catalog() {
		if o.Binding != "unbound" || o.Owner == "" || len(o.Scopes) == 0 {
			t.Fatalf("bad catalog %+v", o)
		}
		if o.Protected && o.CheckLifecycle("disable") != ErrProtected {
			t.Fatal(o.Code)
		}
	}
	o, _ := Lookup("iam.permission.disable")
	o.Scopes[0] = Groups
	unchanged, _ := Lookup(o.Code)
	if unchanged.Scopes[0] == Groups {
		t.Fatal("registry mutated through lookup")
	}
	if CheckRoleMutation("root") != ErrProtected {
		t.Fatal("root mutable")
	}
	now := time.Now().UTC()
	states := []PolicyState{{Mode: "legacy", Cutover: "idle"}, {Mode: "legacy", Cutover: "blocked", BatchID: "batch"}, {Mode: "iam", Cutover: "verified", BatchID: "batch", VerifiedAt: &now}, {Mode: "iam", Cutover: "complete", BatchID: "batch", VerifiedAt: &now}}
	writes := []WriteKind{LegacyAccountWrite, IAMManagementWrite, CandidateWrite, MigrationWrite, BootstrapWrite}
	want := [][]bool{{true, false, true, false, true}, {false, false, false, true, false}, {false, false, false, true, false}, {false, true, false, false, true}}
	for i, s := range states {
		for j, k := range writes {
			if got := s.CheckWrite(k, true) == nil; got != want[i][j] {
				t.Fatalf("state %+v kind %s: %v", s, k, got)
			}
		}
	}
	if states[0].CheckWrite(CandidateWrite, false) == nil || states[1].CheckWrite(MigrationWrite, false) == nil {
		t.Fatal("untrusted migration")
	}
	for _, s := range []PolicyState{{Mode: "iam", Cutover: "idle"}, {Mode: "legacy", Cutover: "verified"}, {Mode: "legacy", Cutover: "blocked"}, {Mode: "iam", Cutover: "complete", BatchID: "b"}, {Mode: "legacy", Cutover: "idle", BatchID: "stale"}, {Mode: "legacy", Cutover: "idle", VerifiedAt: &now}} {
		if s.Validate() == nil {
			t.Fatal(s)
		}
	}
	for _, origin := range []string{"default", "bootstrap", "explicit"} {
		if ValidateOrigin(origin, "") != nil || ValidateOrigin(origin, "b") == nil {
			t.Fatal(origin)
		}
	}
	if ValidateOrigin("legacy_candidate", "") == nil || ValidateOrigin("legacy_candidate", "batch") != nil || ValidateOrigin("unknown", "") == nil {
		t.Fatal("candidate ownership")
	}
}

func TestScopeParsingRejectsUnknownConditions(t *testing.T) {
	for _, raw := range []string{`null`, `{}`, `{"clauses":[{"script":"true"}]}`, `{"clauses":[{}]}`, `{"clauses":[{"all":true,"resource_ids":[1]}]}`, `{"clauses":[{"user_ids":[-1]}]}`, `{"clauses":[{"all":true}]} {}`, `{"clauses":[{"organization_ids":[1]}]}`} {
		if _, err := ParseScope([]byte(raw), []ScopeKind{All, Self, Users, Resources, Groups}); err == nil {
			t.Fatal(raw)
		}
	}
	s, err := ParseScope([]byte(`{"clauses":[{"resource_ids":[42],"routing_group_ids":[11]}]}`), []ScopeKind{All, Resources, Groups})
	if err != nil || len(s.Clauses) != 1 {
		t.Fatal(s, err)
	}
}
