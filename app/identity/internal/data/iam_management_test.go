package data

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

func TestIAMA5A6GovernanceDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			manager := f.register("manager")
			member := f.register("managed-member")
			outsider := f.register("outside-member")
			managerRole := f.role("iam-manager")
			f.assign(manager.ID, managerRole, time.Now().Add(-time.Hour), nil)
			repo := NewIAMManagementRepo(f.repo.(*iamRepo).data)
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				roles, err := repo.Roles(ctx, tx, authorization.Platform())
				if err != nil {
					return err
				}
				for _, r := range roles {
					if r.ID == managerRole {
						for _, op := range authorization.Catalog() {
							if strings.HasPrefix(op.Code, "iam.") || strings.HasPrefix(op.Code, "identity.user_role.") {
								r.Grants = append(r.Grants, biz.IAMGrant{Operation: op.Code, Effect: authorization.Allow, Scope: iamTestAll()})
							}
						}
						_, err = repo.SaveManagedRole(ctx, tx, r, r.Revision)
						return err
					}
				}
				return nil
			})
			require.NoError(t, f.db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
			f.mode("iam", "complete")
			rootToken := f.token(1, "a5-root", 0, time.Now().Add(3*time.Hour))
			managerToken := f.token(manager.ID, "a5-manager", 0, time.Now().Add(3*time.Hour))
			uc := biz.NewIAMGovernanceUsecase(repo, f.runner, f.uc)
			seq := 0
			request := func() m.Request {
				seq++
				_, policy := f.versions(1)
				return m.Request{Context: authorization.Platform(), ExpectedPolicyRevision: policy, RequestID: fmt.Sprintf("a5-request-%d", seq), Reason: "isolated acceptance"}
			}
			run := func(raw, method string, req m.Request) m.Response {
				t.Helper()
				out, err := uc.Execute(f.ctx, raw, method, req)
				require.NoError(t, err, "%s", method)
				return out
			}
			ceiling := []m.Ceiling{{Context: authorization.Platform(), Operation: "channel.channel.read", Scope: iamTestAll()}}
			req := request()
			req.Delegation = &m.Delegation{ManagerRoleID: managerRole, TargetKind: "role_creation", Actions: []string{"iam.role.create", "iam.role.copy", "iam.role.read", "iam.role.update", "iam.role.enable", "iam.role.disable", "iam.role.permissions.read", "iam.role.permissions.update", "iam.role.hierarchy.update", "iam.role.members.read", "identity.user_role.read", "identity.user_role.assign", "identity.user_role.revoke", "identity.user_role.batch_assign"}, TargetUserScope: authorization.Scope{Clauses: []authorization.Clause{{UserIDs: []int64{member.ID}}}}, GrantCeiling: ceiling, Validity: authorization.Interval{StartsAt: time.Now().Add(-time.Minute)}}
			delegation := run(rootToken, "CreateDelegation", req).Delegations[0]
			req = request()
			req.Role = &m.Role{Code: "managed", Name: "Managed", Grants: []m.Grant{{Operation: "channel.channel.read", Effect: authorization.Allow, Scope: iamTestAll()}}}
			created := run(managerToken, "CreateRole", req).Roles[0]
			require.Equal(t, "draft", created.Status)
			require.Equal(t, delegation.ID, created.CreationDelegationID)
			req = request()
			req.ID = created.ID
			req.ExpectedRevision = created.Revision
			req.Role = &m.Role{Status: "enabled"}
			req.Operation = "SetRoleStatus"
			preview := run(managerToken, "PreviewRoleChange", req)
			require.Equal(t, req.ExpectedPolicyRevision, preview.BasePolicyRevision)
			require.NotEmpty(t, preview.ContentDigest)
			req.BasePolicyRevision, req.ContentDigest = preview.BasePolicyRevision, preview.ContentDigest
			published := run(managerToken, "SetRoleStatus", req).Roles[0]
			stale := req
			_, err = uc.Execute(f.ctx, managerToken, "SetRoleStatus", stale)
			require.ErrorIs(t, err, biz.ErrIAMRevisionConflict)
			req = request()
			req.UserID = member.ID
			req.Assignment = &m.Assignment{UserID: member.ID, RoleID: created.ID, Boundary: iamTestAll()}
			assigned := run(managerToken, "AssignUserRole", req).Assignments[0]
			require.Equal(t, "explicit", assigned.Origin)
			// A valid managed change must not disclose unrelated grants on its member.
			hiddenRole := f.role("hidden-finance")
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				roles, err := repo.Roles(ctx, tx, authorization.Platform())
				if err != nil {
					return err
				}
				for _, r := range roles {
					if r.ID == hiddenRole {
						r.Grants = []biz.IAMGrant{{Operation: "billing.payment.read", Effect: authorization.Allow, Scope: iamTestAll()}}
						_, err = repo.SaveManagedRole(ctx, tx, r, r.Revision)
						return err
					}
				}
				return biz.ErrIAMNotFound
			})
			f.assign(member.ID, hiddenRole, time.Now().Add(-time.Hour), nil)
			req = request()
			req.ID, req.ExpectedRevision = created.ID, published.Revision
			req.Operation = "UpdateRolePermissions"
			req.Grants = created.Grants
			diff := run(managerToken, "PreviewRoleChange", req)
			require.Len(t, diff.Impacts, 1)
			require.Len(t, diff.Impacts[0].Before, 1)
			require.Len(t, diff.Impacts[0].After, 1)
			require.Equal(t, "channel.channel.read", diff.Impacts[0].After[0].Operation)
			req = request()
			req.UserID = outsider.ID
			req.Assignment = &m.Assignment{UserID: outsider.ID, RoleID: created.ID, Boundary: iamTestAll()}
			_, err = uc.Execute(f.ctx, managerToken, "AssignUserRole", req)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			req = request()
			req.UserID = manager.ID
			req.Assignment = &m.Assignment{UserID: manager.ID, RoleID: created.ID, Boundary: iamTestAll()}
			_, err = uc.Execute(f.ctx, managerToken, "AssignUserRole", req)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			req = request()
			req.ID = created.ID
			req.ExpectedRevision = published.Revision
			req.Grants = []m.Grant{{Operation: "billing.payment.read", Effect: authorization.Allow, Scope: iamTestAll()}}
			_, err = uc.Execute(f.ctx, managerToken, "UpdateRolePermissions", req)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			read := run(managerToken, "ListRoles", m.Request{Context: authorization.Platform()})
			require.Len(t, read.Roles, 1)
			require.Equal(t, created.ID, read.Roles[0].ID)
			req = request()
			req.ID = created.ID
			req.ExpectedRevision = published.Revision
			_, err = uc.Execute(f.ctx, rootToken, "ArchiveRole", req)
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			// Concurrent target and policy CAS: exactly one transaction/audit commits.
			req = request()
			req.ID = created.ID
			req.ExpectedRevision = published.Revision
			req.Role = &m.Role{Name: "Concurrent"}
			req.UpdateMask = []string{"name"}
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			for i := range 2 {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					r := req
					r.RequestID = fmt.Sprint("concurrent-", i)
					_, err := uc.Execute(f.ctx, rootToken, "UpdateRole", r)
					errs <- err
				}(i)
			}
			wg.Wait()
			close(errs)
			success := 0
			for e := range errs {
				if e == nil {
					success++
				} else {
					require.ErrorIs(t, e, biz.ErrIAMRevisionConflict)
				}
			}
			require.Equal(t, 1, success)
			// Snapshot validity includes management authority expiry without a sweep.
			end := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
			req = request()
			req.ID, req.ExpectedRevision = delegation.ID, delegation.Revision
			req.UpdateMask = []string{"validity.expires_at"}
			req.Delegation = &m.Delegation{Validity: authorization.Interval{ExpiresAt: &end}}
			delegation = run(rootToken, "UpdateDelegation", req).Delegations[0]
			managerSnapshot, err := f.uc.GetSessionAuthorization(f.ctx, managerToken, authorization.Platform())
			require.NoError(t, err)
			require.True(t, managerSnapshot.ValidUntil.Equal(end))
			// Revoked creation delegation loses management but retains business sources.
			req = request()
			req.ID = delegation.ID
			req.ExpectedRevision = delegation.Revision
			run(rootToken, "RevokeDelegation", req)
			req = request()
			req.Role = &m.Role{Code: "after-revoke", Name: "Denied"}
			_, err = uc.Execute(f.ctx, managerToken, "CreateRole", req)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			snap, err := f.uc.GetSessionAuthorization(f.ctx, f.token(member.ID, "member-business", 0, time.Now().Add(time.Hour)), authorization.Platform())
			require.NoError(t, err)
			require.True(t, strings.Contains(fmt.Sprint(snap.Sources), "channel.channel.read"))
			f.mode("legacy", "idle")
			req = request()
			req.Role = &m.Role{Code: "legacy-denied", Name: "Denied"}
			_, err = uc.Execute(f.ctx, rootToken, "CreateRole", req)
			require.ErrorIs(t, err, biz.ErrIAMCutoverBlocked)
		})
	}
}
func iamTestAll() authorization.Scope {
	return authorization.Scope{Clauses: []authorization.Clause{{All: true}}}
}

func TestIAMA5LifecycleAtomicBatchAndCatalogDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			user := f.register("batch-user")
			f.mode("iam", "complete")
			require.NoError(t, f.db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
			repo := NewIAMManagementRepo(f.repo.(*iamRepo).data)
			uc := biz.NewIAMGovernanceUsecase(repo, f.runner, f.uc)
			raw := f.token(1, "lifecycle-root", 0, time.Now().Add(time.Hour))
			seq := 0
			req := func() m.Request {
				seq++
				_, p := f.versions(1)
				return m.Request{Context: authorization.Platform(), ExpectedPolicyRevision: p, RequestID: fmt.Sprint("life-", seq), Reason: "isolated lifecycle"}
			}
			run := func(method string, r m.Request) m.Response {
				t.Helper()
				out, err := uc.Execute(f.ctx, raw, method, r)
				require.NoError(t, err, method)
				return out
			}
			create := func(code string) m.Role {
				r := req()
				r.Role = &m.Role{Code: code, Name: code, Grants: []m.Grant{{Operation: "channel.channel.read", Effect: authorization.Allow, Scope: iamTestAll()}}}
				role := run("CreateRole", r).Roles[0]
				r = req()
				r.ID = role.ID
				r.ExpectedRevision = role.Revision
				r.Role = &m.Role{Status: "enabled"}
				return run("SetRoleStatus", r).Roles[0]
			}
			first, second := create("first"), create("second")
			r := req()
			r.SourceID = first.ID
			r.Role = &m.Role{Code: "copy", Name: "Copy"}
			copy := run("CopyRole", r).Roles[0]
			require.Equal(t, "draft", copy.Status)
			require.Zero(t, copy.CreationDelegationID)
			r = req()
			r.Constraint = &m.Constraint{Name: "exclusive", Kind: "SSD", RoleIDs: []int64{first.ID, second.ID}, MaxCount: 1, Enabled: true}
			constraints := run("CreateRoleConstraint", r).Constraints
			require.Len(t, constraints, 1)
			r = req()
			r.Assignments = []m.Assignment{{UserID: user.ID, RoleID: first.ID, Boundary: iamTestAll()}, {UserID: user.ID, RoleID: second.ID, Boundary: iamTestAll()}}
			r.Operation = "BatchAssignUserRoles"
			preview := run("PreviewUserRoleChange", r)
			require.NotEmpty(t, preview.Conflicts)
			require.Len(t, preview.Impacts, 1)
			before := iamCount(t, f.db, "iam_user_roles")
			_, err = uc.Execute(f.ctx, raw, "BatchAssignUserRoles", r)
			require.ErrorIs(t, err, biz.ErrIAMConstraintsViolated)
			require.Equal(t, before, iamCount(t, f.db, "iam_user_roles"))
			r = req()
			r.ID = constraints[0].ID
			r.ExpectedRevision = constraints[0].Revision
			run("DeleteRoleConstraint", r)
			r = req()
			r.Assignments = []m.Assignment{{UserID: user.ID, RoleID: first.ID, Boundary: iamTestAll()}, {UserID: user.ID, RoleID: second.ID, Boundary: iamTestAll()}}
			assigned := run("BatchAssignUserRoles", r).Assignments
			require.Len(t, assigned, 2)
			r = req()
			r.ID = first.ID
			r.ExpectedRevision = first.Revision
			r.RoleIDs = []int64{second.ID}
			first = run("UpdateRoleInheritance", r).Roles[0]
			r = req()
			r.ID = second.ID
			r.ExpectedRevision = second.Revision
			r.RoleIDs = []int64{first.ID}
			_, err = uc.Execute(f.ctx, raw, "UpdateRoleInheritance", r)
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			// Forced deny participates in permission edits and their preview impact.
			r = req()
			r.ID = first.ID
			r.ExpectedRevision = first.Revision
			r.Grants = []m.Grant{{Operation: "channel.channel.read", Effect: authorization.Allow, Scope: iamTestAll()}, {Operation: "channel.channel.read", Effect: authorization.Deny, Scope: iamTestAll()}}
			first = run("UpdateRolePermissions", r).Roles[0]
			r = req()
			r.ID = first.ID
			r.ExpectedRevision = first.Revision
			r.Grants = []m.Grant{{Operation: "channel.channel.read", Effect: authorization.Allow, Scope: iamTestAll()}}
			r.Operation = "UpdateRolePermissions"
			preview = run("PreviewRoleChange", r)
			require.NotEmpty(t, preview.Impacts[0].Before)
			require.NotEmpty(t, preview.Impacts[0].After)
			// Catalog drafts cannot create executable capabilities or remove core recovery.
			permissions := run("ListPermissions", m.Request{Context: authorization.Platform(), PageSize: 200}).Permissions
			var resourceID, coreID int64
			var coreRevision uint64
			for _, p := range permissions {
				if p.Code == "channel.channel.read" {
					resourceID = p.ResourceID
				}
				if p.Code == "iam.permission.read" {
					coreID = p.ID
					coreRevision = p.Revision
					require.Equal(t, "bound", p.Binding)
					require.True(t, p.Protected)
				}
			}
			r = req()
			r.Permission = &m.Permission{Code: "channel.channel.new_draft", ResourceID: resourceID, Name: "Draft only"}
			draft := run("CreatePermission", r).Permissions[0]
			require.Equal(t, "unbound", draft.Binding)
			r = req()
			r.ID = draft.ID
			r.ExpectedRevision = draft.Revision
			r.Permission = &m.Permission{Status: "enabled"}
			_, err = uc.Execute(f.ctx, raw, "SetPermissionStatus", r)
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			r = req()
			r.ID = coreID
			r.ExpectedRevision = coreRevision
			r.Permission = &m.Permission{Status: "disabled"}
			_, err = uc.Execute(f.ctx, raw, "SetPermissionStatus", r)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			var unbound struct {
				ID       int64
				Revision uint64
			}
			require.NoError(t, f.db.Table("iam_permissions").Select("id, revision").Where("code = ?", "channel.channel.read").Scan(&unbound).Error)
			r = req()
			r.ID, r.ExpectedRevision = unbound.ID, unbound.Revision
			r.Permission = &m.Permission{Status: "enabled"}
			_, err = uc.Execute(f.ctx, raw, "SetPermissionStatus", r)
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			// PATCH masks preserve omitted constraint facts.
			third := create("patch-role")
			r = req()
			r.Constraint = &m.Constraint{Kind: "SSD", Name: "patch", RoleIDs: []int64{first.ID, second.ID, third.ID}, MaxCount: 2, Enabled: true}
			patchConstraints := run("CreateRoleConstraint", r).Constraints
			var patch m.Constraint
			for _, c := range patchConstraints {
				if c.Name == "patch" {
					patch = c
				}
			}
			require.Positive(t, patch.ID)
			r = req()
			r.ID, r.ExpectedRevision = patch.ID, patch.Revision
			r.UpdateMask = []string{"name"}
			r.Constraint = &m.Constraint{Name: "renamed"}
			updated := run("UpdateRoleConstraint", r).Constraints
			for _, c := range updated {
				if c.ID == patch.ID {
					require.Equal(t, "renamed", c.Name)
					require.Equal(t, patch.Kind, c.Kind)
					require.Equal(t, patch.RoleIDs, c.RoleIDs)
					require.Equal(t, patch.MaxCount, c.MaxCount)
					require.True(t, c.Enabled)
				}
			}
			// Registered route/icon whitelist and parent cycle validation.
			r = req()
			r.Menu = &m.Menu{RouteKey: "users", IconKey: "users", Name: "Users", Enabled: true}
			menu := run("CreateMenuItem", r).Menus[0]
			r = req()
			r.ID = menu.ID
			r.ExpectedRevision = menu.Revision
			r.UpdateMask = []string{"parent_id"}
			r.Menu = &m.Menu{ParentID: menu.ID}
			_, err = uc.Execute(f.ctx, raw, "UpdateMenuItem", r)
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			r = req()
			r.Menu = &m.Menu{RouteKey: "arbitrary_external_url", IconKey: "users", Name: "Denied"}
			_, err = uc.Execute(f.ctx, raw, "CreateMenuItem", r)
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			page := run("ListRoles", m.Request{Context: authorization.Platform(), PageSize: 1, Filter: `status = "enabled"`, OrderBy: "id desc"})
			require.NotEmpty(t, page.NextPageToken)
			require.Greater(t, page.Total, int64(1))
			next := run("ListRoles", m.Request{Context: authorization.Platform(), PageSize: 1, Filter: `status = "enabled"`, OrderBy: "id desc", PageToken: page.NextPageToken})
			require.NotEqual(t, page.Roles[0].ID, next.Roles[0].ID)
			// Failure of the success audit rolls back every resource and version.
			before = iamCount(t, f.db, "iam_roles")
			r = req()
			r.Role = &m.Role{Code: "audit-rollback", Name: "Rollback"}
			cleanup := f.failCreate("iam_audit_events")
			_, err = uc.Execute(f.ctx, raw, "CreateRole", r)
			cleanup()
			require.Error(t, err)
			require.Equal(t, before, iamCount(t, f.db, "iam_roles"))
		})
	}
}
