package biz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"time"

	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/pkg/jsonx"
)

// IAMMigrationRepo is available only to the offline owner CLI, never an RPC.
// It shares the policy lock and audit transaction with normal identity writes.
type IAMMigrationRepo interface {
	IAMManagementRepo
	MigrationUsers(context.Context, IAMTx) ([]IAMMigrationUser, error)
	ReplaceCandidates(context.Context, IAMTx, []IAMAssignment) error
	PublishMigrationCatalog(context.Context, IAMTx, map[string][]IAMGrant) error
	SetCutover(context.Context, IAMTx, authorization.PolicyState, uint64) error
	MigrationReceipt(context.Context, IAMTx, string, string) (*IAMMigrationReport, error)
}

type IAMMigrationUser struct {
	ID           int64
	Role, Status int32
	Revision     uint64
}

// Evidence is a root-approved, signed operational attestation. References name
// collected evidence, not secrets. The CLI verifies its signature before biz.
type IAMCutoverEvidence struct {
	BatchID, SourceDigest, ManifestDigest, DatabaseIdentity         string
	RootUserID                                                      int64
	CapturedAt, ExpiresAt                                           time.Time
	Barrier, Drained, OldWritersExited, OldDBChannelsRevoked        string
	FinanceIsolation, FinanceReplay, Rollback, Frontend, Regression string
	Instances                                                       map[string]string
}

type IAMMigrationManifest struct {
	Roles       []IAMRole
	Assignments []IAMAssignment
	Delegations []m.Delegation
}

func IAMMigrationDigest(value any) string {
	b, err := jsonx.Marshal(value)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

type IAMMigrationRequest struct {
	Command, BatchID, RequestID, Reason string
	ExpectedPolicyRevision              uint64
	Evidence                            *IAMCutoverEvidence
	Manifest                            IAMMigrationManifest
}

type IAMMigrationDifference struct {
	UserID                    int64
	Operation, Kind           string
	LegacyAllowed, IAMAllowed bool
}

type IAMMigrationReport struct {
	Command, BatchID, Digest           string
	DatabaseIdentity, SourceDigest     string
	Policy                             authorization.PolicyState
	Users                              []IAMMigrationUser
	UnknownUsers                       []int64
	MismatchedUsers, OrphanAssignments []int64
	Differences                        []IAMMigrationDifference
	Conflicts                          []IAMConstraintConflict
	CatalogMismatches                  []string
	Manifest                           IAMMigrationManifest
	Verified                           bool
}

type IAMMigrationUsecase struct {
	repo   IAMMigrationRepo
	runner IAMTxRunner
	now    func() time.Time
}

func NewIAMMigrationUsecase(repo IAMMigrationRepo, runner IAMTxRunner) *IAMMigrationUsecase {
	return &IAMMigrationUsecase{repo: repo, runner: runner, now: time.Now}
}

func legacyRoleCode(role int32) string {
	switch role {
	case 0:
		return "guest"
	case 1:
		return "member"
	case 10:
		return "platform_admin"
	case 100:
		return "root"
	default:
		return ""
	}
}

func (uc *IAMMigrationUsecase) evidence(ctx context.Context, tx IAMTx, req IAMMigrationRequest, complete bool) error {
	e := req.Evidence
	now := uc.now().UTC()
	if e == nil || e.BatchID != req.BatchID || len(e.SourceDigest) != 64 || e.DatabaseIdentity == "" || e.ManifestDigest != IAMMigrationDigest(req.Manifest) || e.CapturedAt.After(now) || !e.ExpiresAt.After(now) || e.ExpiresAt.Sub(e.CapturedAt) > 24*time.Hour {
		return ErrIAMProtected
	}
	for _, ref := range []string{e.Barrier, e.Drained, e.OldWritersExited, e.OldDBChannelsRevoked, e.FinanceIsolation, e.FinanceReplay, e.Rollback, e.Regression} {
		if strings.TrimSpace(ref) == "" {
			return ErrIAMProtected
		}
	}
	if complete && e.Frontend == "" {
		return ErrIAMProtected
	}
	for _, service := range []string{"admin", "identity", "channel", "billing", "config", "log", "monitor", "notify", "relay"} {
		if strings.TrimSpace(e.Instances[service]) == "" {
			return ErrIAMProtected
		}
	}
	u, err := uc.repo.User(ctx, tx, e.RootUserID)
	if err != nil {
		return err
	}
	if u.Status != UserStatusEnabled {
		return ErrIAMProtected
	}
	p, err := uc.repo.Policy(ctx, tx)
	if err != nil {
		return err
	}
	if p.Mode == "legacy" {
		if u.Role != RoleRootUser {
			return ErrIAMProtected
		}
	} else {
		assignments, err := uc.repo.Assignments(ctx, tx, authorization.Platform(), u.ID)
		if err != nil {
			return err
		}
		roles, err := uc.repo.Roles(ctx, tx, authorization.Platform())
		if err != nil {
			return err
		}
		root := false
		for _, a := range assignments {
			for _, r := range roles {
				if a.RoleID == r.ID && r.Builtin && r.Code == "root" && r.Status == "enabled" && !a.Revoked && a.Validity.Contains(now) {
					root = true
				}
			}
		}
		if !root {
			return ErrIAMProtected
		}
	}
	return nil
}

func migrationCandidates(users []IAMMigrationUser, roles []IAMRole, batch string, now time.Time) ([]IAMAssignment, []int64, error) {
	ids := map[string]int64{}
	for _, r := range roles {
		if r.Builtin && r.Status == "enabled" {
			ids[r.Code] = r.ID
		}
	}
	for _, code := range []string{"guest", "member", "platform_admin", "root"} {
		if ids[code] == 0 {
			return nil, nil, ErrIAMProtected
		}
	}
	out := []IAMAssignment{}
	unknown := []int64{}
	for _, u := range users {
		code := legacyRoleCode(u.Role)
		if code == "" {
			unknown = append(unknown, u.ID)
			continue
		}
		out = append(out, IAMAssignment{UserID: u.ID, RoleID: ids[code], Context: authorization.Platform(), Boundary: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, Validity: authorization.Interval{StartsAt: now}, Origin: "legacy_candidate", MigrationBatchID: batch})
	}
	return out, unknown, nil
}

// Execute keeps rebuild, full verification, mode CAS and the report's success
// audit atomic. A retry from verified never reads users.role as authority.
func (uc *IAMMigrationUsecase) Execute(ctx context.Context, req IAMMigrationRequest) (IAMMigrationReport, error) {
	out := IAMMigrationReport{Command: req.Command, BatchID: req.BatchID}
	read := slices.Contains([]string{"status", "inventory", "shadow", "verify"}, req.Command)
	if !read && (!slices.Contains([]string{"apply", "block", "rebuild", "import", "activate", "complete", "resume"}, req.Command) || strings.TrimSpace(req.BatchID) == "" || req.ExpectedPolicyRevision == 0 || strings.TrimSpace(req.Reason) == "" || strings.TrimSpace(req.RequestID) == "") {
		return out, ErrIAMInvalidRelation
	}
	if uc == nil || uc.repo == nil || uc.runner == nil {
		return out, ErrIAMDependencyUnavailable
	}
	run := uc.runner.RunIAMWrite
	if read {
		run = uc.runner.ReadIAMSnapshot
	}
	originalReq := req
	err := run(ctx, func(ctx context.Context, tx IAMTx) error {
		req = originalReq
		out = IAMMigrationReport{Command: req.Command, BatchID: req.BatchID}
		p, err := uc.repo.Policy(ctx, tx)
		if err != nil {
			return err
		}
		out.Policy = p
		if req.Command == "status" {
			out.Verified = p.Cutover == "verified" || p.Cutover == "complete"
			return nil
		}
		if !read {
			receipt, e := uc.repo.MigrationReceipt(ctx, tx, req.RequestID, IAMMigrationDigest(req))
			if e != nil {
				return e
			}
			if receipt != nil {
				out = *receipt
				return nil
			}
		}
		if !read && p.PolicyRevision != req.ExpectedPolicyRevision {
			return ErrIAMRevisionConflict
		}
		if !read && p.Cutover != "idle" && p.BatchID != req.BatchID {
			return ErrIAMCutoverBlocked
		}
		if !read && req.Command != "apply" {
			if err = uc.evidence(ctx, tx, req, req.Command == "complete"); err != nil {
				return err
			}
		}
		now := uc.now().UTC().Truncate(time.Millisecond)
		if req.Command == "block" {
			if p.Mode != "legacy" || (p.Cutover != "idle" && p.Cutover != "blocked") {
				return ErrIAMCutoverBlocked
			}
			if p.Cutover == "blocked" {
				out.Verified = false
				return nil
			}
			p.Cutover, p.BatchID = "blocked", req.BatchID
			if err = uc.repo.SetCutover(ctx, tx, p, p.PolicyRevision); err != nil {
				return err
			}
			return uc.finish(ctx, tx, originalReq, &out)
		}
		if req.Command == "resume" {
			switch p.Cutover {
			case "blocked":
				req.Command = "activate"
			case "verified":
				req.Command = "complete"
			case "complete":
				out.Verified = true
				return nil
			default:
				return ErrIAMCutoverBlocked
			}
		}
		if req.Command == "complete" {
			if p.Mode != "iam" || (p.Cutover != "verified" && p.Cutover != "complete") {
				return ErrIAMCutoverBlocked
			}
			if err = uc.evidence(ctx, tx, req, true); err != nil {
				return err
			}
			if p.Cutover == "complete" {
				out.Verified = true
				return nil
			}
		}
		if slices.Contains([]string{"apply", "rebuild", "activate", "import"}, req.Command) {
			kind := authorization.MigrationWrite
			if req.Command == "apply" {
				kind = authorization.CandidateWrite
			}
			if p.CheckWrite(kind, true) != nil || p.Mode != "legacy" {
				return ErrIAMCutoverBlocked
			}
		}
		users, err := uc.repo.MigrationUsers(ctx, tx)
		if err != nil {
			return err
		}
		state, err := uc.repo.ConstraintState(ctx, tx, authorization.Platform())
		if err != nil {
			return err
		}
		out.Users = users
		if p.Mode == "legacy" {
			batch := req.BatchID
			if batch == "" {
				batch = "inventory"
			}
			candidates, unknown, err := migrationCandidates(users, state.Roles, batch, now)
			if err != nil {
				return err
			}
			out.UnknownUsers = unknown
			if len(unknown) > 0 {
				if read {
					return nil
				}
				return ErrIAMInvalidRelation
			}
			if slices.Contains([]string{"apply", "rebuild", "activate"}, req.Command) {
				if err = uc.repo.ReplaceCandidates(ctx, tx, candidates); err != nil {
					return err
				}
				if err = uc.repo.PublishMigrationCatalog(ctx, tx, IAMMigrationBuiltinGrants()); err != nil {
					return err
				}
				state, err = uc.repo.ConstraintState(ctx, tx, authorization.Platform())
				if err != nil {
					return err
				}
			}
			if req.Command == "import" {
				if err = uc.importManifest(ctx, tx, req.Manifest, now); err != nil {
					return err
				}
				state, err = uc.repo.ConstraintState(ctx, tx, authorization.Platform())
				if err != nil {
					return err
				}
			}
			if !read {
				users, err = uc.repo.MigrationUsers(ctx, tx)
				if err != nil {
					return err
				}
				out.Users = users
			}
			uc.reconcile(users, state, &out, now)
			if req.Command == "shadow" || req.Command == "activate" {
				out.Differences = iamMigrationShadow(users, state, now)
				if len(req.Manifest.Roles) > 0 {
					if err = uc.approvedRelations(ctx, tx, state, req.Manifest); err != nil {
						return err
					}
					for i := range out.Differences {
						if out.Differences[i].Kind == "unexpected" {
							out.Differences[i].Kind = "root_approved_change"
						}
					}
				}
			}
		} else {
			// Never compare against legacy numeric roles after the handoff.
			uc.validateIAMUsers(users, state, &out, now)
		}
		out.Conflicts, err = IAMCheckConstraints(now, state)
		if err != nil {
			return err
		}
		if req.Command != "inventory" && req.Command != "shadow" {
			if err = uc.validateRelations(ctx, tx, users, state, now); err != nil {
				return err
			}
		}
		out.CatalogMismatches, err = uc.catalogMismatches(ctx, tx, state)
		if err != nil {
			return err
		}
		out.Digest = IAMMigrationDigest(struct {
			Users []IAMMigrationUser
			State IAMConstraintState
		}{users, state})
		for _, r := range state.Roles {
			if !r.Builtin {
				out.Manifest.Roles = append(out.Manifest.Roles, r)
			}
		}
		for _, a := range state.Assignments {
			if a.Origin == "explicit" && !a.Revoked {
				out.Manifest.Assignments = append(out.Manifest.Assignments, a)
			}
		}
		out.Manifest.Delegations, err = uc.repo.Delegations(ctx, tx, authorization.Platform())
		if err != nil {
			return err
		}
		out.Verified = len(out.UnknownUsers) == 0 && len(out.MismatchedUsers) == 0 && len(out.OrphanAssignments) == 0 && len(out.Conflicts) == 0 && len(out.CatalogMismatches) == 0
		for _, d := range out.Differences {
			if d.Kind == "unexpected" || d.Kind == "source_error" {
				out.Verified = false
			}
		}
		if read {
			return nil
		}
		if !out.Verified {
			return ErrIAMInvalidRelation
		}
		if req.Command == "activate" || req.Command == "complete" {
			if err = uc.approvedRelations(ctx, tx, state, req.Manifest); err != nil {
				return err
			}
			if req.Command == "activate" {
				p.Mode, p.Cutover, p.VerifiedAt = "iam", "verified", &now
			} else {
				p.Cutover = "complete"
			}
			// Re-read after assignment/catalog versions advanced.
			current, err := uc.repo.Policy(ctx, tx)
			if err != nil {
				return err
			}
			if err = uc.repo.SetCutover(ctx, tx, p, current.PolicyRevision); err != nil {
				return err
			}
		} else {
			current, e := uc.repo.Policy(ctx, tx)
			if e != nil {
				return e
			}
			if err = uc.repo.AdvancePolicy(ctx, tx, current.PolicyRevision, false); err != nil {
				return err
			}
		}
		return uc.finish(ctx, tx, originalReq, &out)
	})
	if err != nil && !read {
		e := IAMAuditEvent{EventID: req.RequestID + "-failure", Actor: authorization.Actor{ServiceID: "iam-migrate"}, Context: authorization.Platform(), TargetContext: authorization.Platform(), Action: "iam.migration." + req.Command, Target: req.BatchID, Before: "{}", After: "{}", Diff: "{}", Result: "failure", RequestID: req.RequestID, Reason: req.Reason, OccurredAt: uc.now().UTC()}
		if auditErr := uc.repo.AppendFailureAudit(ctx, e); auditErr != nil {
			return out, fmt.Errorf("%w; failure audit: %v", err, auditErr)
		}
	}
	return out, err
}

// Non-candidate origin is not approval of authority: the signed manifest names
// all custom policy and delegation facts preserved at the handoff.
func (uc *IAMMigrationUsecase) approvedRelations(ctx context.Context, tx IAMTx, state IAMConstraintState, manifest IAMMigrationManifest) error {
	roleDigest := func(r IAMRole) string { r.ID = 0; r.Revision = 0; return IAMMigrationDigest(r) }
	assignmentDigest := func(a IAMAssignment) string { a.ID = 0; a.Revision = 0; return IAMMigrationDigest(a) }
	delegationDigest := func(d m.Delegation) string { d.ID = 0; d.Revision = 0; return IAMMigrationDigest(d) }
	custom := map[int64]bool{}
	for _, r := range state.Roles {
		if r.Builtin {
			continue
		}
		custom[r.ID] = true
		if !slices.ContainsFunc(manifest.Roles, func(x IAMRole) bool { return roleDigest(x) == roleDigest(r) }) {
			return ErrIAMProtected
		}
	}
	for _, a := range state.Assignments {
		if !a.Revoked && custom[a.RoleID] {
			if !slices.ContainsFunc(manifest.Assignments, func(x IAMAssignment) bool { return assignmentDigest(x) == assignmentDigest(a) }) {
				return ErrIAMProtected
			}
		}
	}
	delegations, err := uc.repo.Delegations(ctx, tx, authorization.Platform())
	if err != nil {
		return err
	}
	for _, d := range delegations {
		if !slices.ContainsFunc(manifest.Delegations, func(x m.Delegation) bool { return delegationDigest(x) == delegationDigest(d) }) {
			return ErrIAMProtected
		}
	}
	for _, r := range manifest.Roles {
		if !slices.ContainsFunc(state.Roles, func(x IAMRole) bool { return !x.Builtin && roleDigest(x) == roleDigest(r) }) {
			return ErrIAMProtected
		}
	}
	for _, a := range manifest.Assignments {
		if !slices.ContainsFunc(state.Assignments, func(x IAMAssignment) bool { return !x.Revoked && assignmentDigest(x) == assignmentDigest(a) }) {
			return ErrIAMProtected
		}
	}
	for _, d := range manifest.Delegations {
		if !slices.ContainsFunc(delegations, func(x m.Delegation) bool { return delegationDigest(x) == delegationDigest(d) }) {
			return ErrIAMProtected
		}
	}
	return nil
}

func (uc *IAMMigrationUsecase) finish(ctx context.Context, tx IAMTx, req IAMMigrationRequest, out *IAMMigrationReport) error {
	p, err := uc.repo.Policy(ctx, tx)
	if err != nil {
		return err
	}
	out.Policy = p
	b, err := jsonx.Marshal(out)
	if err != nil {
		return ErrIAMInvalidRelation
	}
	evidence, _ := jsonx.Marshal(struct {
		RequestDigest string
		Evidence      *IAMCutoverEvidence
	}{IAMMigrationDigest(req), req.Evidence})
	return uc.repo.AppendAudit(ctx, tx, IAMAuditEvent{EventID: req.RequestID, Actor: authorization.Actor{ServiceID: "iam-migrate"}, Context: authorization.Platform(), TargetContext: authorization.Platform(), Action: "iam.migration." + req.Command, Target: req.BatchID, Before: string(evidence), After: string(b), Diff: out.Digest, Result: "success", RequestID: req.RequestID, Reason: req.Reason, OccurredAt: uc.now().UTC(), Versions: authorization.Versions{Policy: p.PolicyRevision, Catalog: p.CatalogRevision}})
}

func (uc *IAMMigrationUsecase) catalogMismatches(ctx context.Context, tx IAMTx, state IAMConstraintState) ([]string, error) {
	permissions, err := uc.repo.Permissions(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := []string{}
	seen := map[string]bool{}
	for _, p := range permissions {
		seen[p.Code] = true
		bound := IAMExecutionBound(p.Code) && slices.Contains(iamMigrationRootCodes, p.Code)
		if (bound && (p.Status != "enabled" || p.Binding != "bound")) || (!bound && p.Status == "enabled") {
			out = append(out, p.Code)
		}
	}
	for _, code := range iamMigrationRootCodes {
		if !seen[code] {
			out = append(out, code)
		}
	}
	for code, grants := range IAMMigrationBuiltinGrants() {
		found := false
		for _, r := range state.Roles {
			if r.Code != code || !r.Builtin {
				continue
			}
			found = true
			actual := slices.Clone(r.Grants)
			expected := slices.Clone(grants)
			cmp := func(a, b IAMGrant) int { return strings.Compare(IAMMigrationDigest(a), IAMMigrationDigest(b)) }
			slices.SortFunc(actual, cmp)
			slices.SortFunc(expected, cmp)
			if r.Status != "enabled" || len(r.Inherits) > 0 || !slices.EqualFunc(actual, expected, func(a, b IAMGrant) bool { return IAMMigrationDigest(a) == IAMMigrationDigest(b) }) {
				out = append(out, "role:"+code)
			}
		}
		if !found {
			out = append(out, "role:"+code)
		}
	}
	slices.Sort(out)
	return out, nil
}

func (uc *IAMMigrationUsecase) reconcile(users []IAMMigrationUser, state IAMConstraintState, out *IAMMigrationReport, now time.Time) {
	roles := map[int64]IAMRole{}
	for _, r := range state.Roles {
		roles[r.ID] = r
	}
	known := map[int64]bool{}
	for _, u := range users {
		known[u.ID] = true
		count := 0
		mismatch := false
		for _, a := range state.Assignments {
			if a.UserID == u.ID && (a.Origin == "legacy_candidate" || (roles[a.RoleID].Builtin && roles[a.RoleID].Code == legacyRoleCode(u.Role))) {
				count++
				if a.Revoked || !a.Validity.Contains(now) || a.Validity.ExpiresAt != nil || !slices.EqualFunc(a.Boundary.Clauses, []authorization.Clause{{All: true}}, func(x, y authorization.Clause) bool { return IAMMigrationDigest(x) == IAMMigrationDigest(y) }) || roles[a.RoleID].Code != legacyRoleCode(u.Role) || (a.Origin == "legacy_candidate" && out.BatchID != "" && a.MigrationBatchID != out.BatchID) {
					mismatch = true
				}
			}
		}
		if count != 1 || mismatch {
			out.MismatchedUsers = append(out.MismatchedUsers, u.ID)
		}
	}
	for _, a := range state.Assignments {
		if !known[a.UserID] {
			out.OrphanAssignments = append(out.OrphanAssignments, a.ID)
		}
	}
}

func (uc *IAMMigrationUsecase) validateIAMUsers(users []IAMMigrationUser, state IAMConstraintState, out *IAMMigrationReport, now time.Time) {
	known := map[int64]bool{}
	for _, u := range users {
		known[u.ID] = true
		found := false
		for _, a := range state.Assignments {
			if a.UserID == u.ID && !a.Revoked && a.Validity.Contains(now) {
				found = true
			}
		}
		if !found {
			out.MismatchedUsers = append(out.MismatchedUsers, u.ID)
		}
	}
	for _, a := range state.Assignments {
		if !known[a.UserID] {
			out.OrphanAssignments = append(out.OrphanAssignments, a.ID)
		}
	}
}

func (uc *IAMMigrationUsecase) validateRelations(ctx context.Context, tx IAMTx, users []IAMMigrationUser, state IAMConstraintState, now time.Time) error {
	roles := map[int64]IAMRole{}
	for _, r := range state.Roles {
		roles[r.ID] = r
		for _, g := range r.Grants {
			op, ok := authorization.Lookup(g.Operation)
			if !ok || g.Scope.Validate(op.Scopes) != nil || (g.Effect != authorization.Allow && g.Effect != authorization.Deny) {
				return ErrIAMInvalidRelation
			}
		}
	}
	delegations, err := uc.repo.Delegations(ctx, tx, authorization.Platform())
	if err != nil {
		return err
	}
	for _, d := range delegations {
		if err = iamValidateDelegation(d, state); err != nil {
			return err
		}
	}
	root := false
	p, err := uc.repo.Policy(ctx, tx)
	if err != nil {
		return err
	}
	for _, u := range users {
		for _, a := range state.Assignments {
			if a.UserID != u.ID || a.Revoked || !a.Validity.Contains(now) {
				continue
			}
			r := roles[a.RoleID]
			if r.Code == "root" {
				if p.Mode == "legacy" && u.Role != RoleRootUser {
					return ErrIAMProtected
				}
				if u.Status == UserStatusEnabled {
					root = true
				}
			}
			if p.Mode == "legacy" && a.Origin != "legacy_candidate" && r.Builtin && r.Code != "guest" && r.Code != "member" && r.Code != legacyRoleCode(u.Role) {
				return ErrIAMProtected
			}
		}
	}
	if !root {
		return ErrIAMProtected
	}
	return nil
}

func (uc *IAMMigrationUsecase) importManifest(ctx context.Context, tx IAMTx, manifest IAMMigrationManifest, now time.Time) error {
	for _, r := range manifest.Roles {
		if r.Context.RequirePlatform() != nil || r.Builtin || r.Code == "" || r.CreationDelegationID != 0 || (r.ID == 0 && r.Revision != 0) || (r.ID != 0 && r.Revision == 0) || !slices.Contains([]string{"draft", "enabled", "disabled"}, r.Status) {
			return ErrIAMProtected
		}
		for _, g := range r.Grants {
			op, ok := authorization.Lookup(g.Operation)
			if !ok || !IAMExecutionBound(g.Operation) || g.Scope.Validate(op.Scopes) != nil || (g.Effect != authorization.Allow && g.Effect != authorization.Deny) {
				return ErrIAMScopeInvalid
			}
		}
		if _, err := uc.repo.SaveManagedRole(ctx, tx, r, r.Revision); err != nil {
			return err
		}
	}
	state, err := uc.repo.ConstraintState(ctx, tx, authorization.Platform())
	if err != nil {
		return err
	}
	for _, d := range manifest.Delegations {
		if err = iamValidateDelegation(d, state); err != nil {
			return err
		}
		if _, err = uc.repo.SaveDelegation(ctx, tx, d, d.Revision); err != nil {
			return err
		}
	}
	for _, a := range manifest.Assignments {
		if a.Origin != "explicit" || a.MigrationBatchID != "" || a.Revoked || a.Context.RequirePlatform() != nil || a.Validity.Validate() != nil || (a.Validity.ExpiresAt != nil && !a.Validity.ExpiresAt.After(now)) {
			return ErrIAMProtected
		}
		for _, r := range state.Roles {
			if r.ID == a.RoleID && r.Builtin {
				return ErrIAMProtected
			}
		}
		if _, err = uc.repo.SaveAssignment(ctx, tx, a, a.Revision); err != nil {
			return err
		}
	}
	return nil
}
