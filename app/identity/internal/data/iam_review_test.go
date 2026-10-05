package data

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/platform/iamdto"
)

func TestIAMReviewManagedCreateCredentialsDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			manager := f.register("review-manager")
			roleID := f.role("review-creator")
			f.assign(manager.ID, roleID, time.Now().Add(-time.Hour), nil)
			repo := NewIAMManagementRepo(f.repo.(*iamRepo).data)
			operations := []string{"identity.user.create"}
			setGrants := func() {
				f.setup(func(ctx context.Context, tx biz.IAMTx) error {
					roles, err := repo.Roles(ctx, tx, authorization.Platform())
					if err != nil {
						return err
					}
					for _, role := range roles {
						if role.ID != roleID {
							continue
						}
						role.Grants = nil
						for _, op := range operations {
							role.Grants = append(role.Grants, biz.IAMGrant{Operation: op, Effect: authorization.Allow, Scope: iamTestAll()})
						}
						_, err = repo.SaveManagedRole(ctx, tx, role, role.Revision)
						return err
					}
					return biz.ErrIAMNotFound
				})
			}
			setGrants()
			require.NoError(t, f.db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
			f.mode("iam", "complete")
			raw := f.token(manager.ID, "review-create", 0, time.Now().Add(time.Hour))
			ctx := authorization.WithCredential(f.ctx, raw)
			_, err = f.uc.GetSessionAuthorization(ctx, raw, authorization.Platform())
			require.NoError(t, err)
			attempt := func(name string) (*biz.User, error) {
				_, policy := f.versions(manager.ID)
				return f.uc.CreateManagedUser(ctx, biz.ManagedUserCreate{Username: name, Password: "review-password", Email: name + "@example.com", ExpectedPolicyRevision: policy, Reason: "review credentials creation"})
			}
			assertDenied := func(name string) {
				t.Helper()
				beforeUsers, beforeAssignments := iamCount(t, f.db, "users"), iamCount(t, f.db, "iam_user_roles")
				_, err := attempt(name)
				require.ErrorIs(t, err, biz.ErrIAMProtected)
				require.Equal(t, beforeUsers, iamCount(t, f.db, "users"))
				require.Equal(t, beforeAssignments, iamCount(t, f.db, "iam_user_roles"))
			}
			assertDenied("missing-credential-permissions")
			operations = append(operations, "identity.user.credential.update", "identity.user.email_binding.update")
			setGrants()
			assertDenied("missing-credential-delegation")
			var delegation m.Delegation
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				var err error
				delegation, err = repo.SaveDelegation(ctx, tx, m.Delegation{Context: authorization.Platform(), ManagerRoleID: roleID, TargetKind: "user_credentials", Actions: []string{"identity.user.credential.update", "identity.user.email_binding.update"}, TargetUserScope: authorization.Scope{Clauses: []authorization.Clause{{UserIDs: []int64{manager.ID}}}}, Validity: authorization.Interval{StartsAt: time.Now().Add(-time.Hour)}}, 0)
				return err
			})
			assertDenied("outside-credential-target")
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				delegation.TargetUserScope = iamTestAll()
				_, err := repo.SaveDelegation(ctx, tx, delegation, delegation.Revision)
				return err
			})
			created, err := attempt(fmt.Sprint("authorized-", driver))
			require.NoError(t, err)
			require.EqualValues(t, biz.RoleCommonUser, created.Role)
			require.Equal(t, created.Email, fmt.Sprint("authorized-", driver, "@example.com"))
			var count int64
			require.NoError(t, f.db.Table("iam_user_roles").Where("user_id = ? AND origin = ?", created.ID, "default").Count(&count).Error)
			require.EqualValues(t, 1, count)
		})
	}
}

func TestIAMReviewPermissionMetadataDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			require.NoError(t, f.db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
			f.mode("iam", "complete")
			raw := f.token(1, "metadata-root", 0, time.Now().Add(time.Hour))
			repo := NewIAMManagementRepo(f.repo.(*iamRepo).data)
			uc := biz.NewIAMGovernanceUsecase(repo, f.runner, f.uc)
			_, err = f.uc.GetSessionAuthorization(f.ctx, raw, authorization.Platform())
			require.NoError(t, err)
			var permissions []m.Permission
			require.NoError(t, f.runner.ReadIAMSnapshot(f.ctx, func(ctx context.Context, tx biz.IAMTx) error {
				permissions, err = repo.Permissions(ctx, tx)
				return err
			}))
			var target m.Permission
			for _, p := range permissions {
				if p.Code == "channel.channel.read" {
					target = p
				}
			}
			require.Positive(t, target.ID)
			_, policy := f.versions(1)
			req := m.Request{Context: authorization.Platform(), ID: target.ID, ExpectedRevision: target.Revision, ExpectedPolicyRevision: policy, Reason: "review display metadata", RequestID: "review-metadata", UpdateMask: []string{"description", "sort", "category"}, Permission: &m.Permission{Description: "Only reads owner-scoped channels", Sort: -10, Category: "review-category"}}
			// Exercise DTO conversion as well as the actual locked write and read.
			req = iamdto.IAMRequestFrom(iamdto.IAMRequestTo(req))
			result, err := uc.Execute(f.ctx, raw, "UpdatePermission", req)
			require.NoError(t, err)
			updated := result.Permissions[0]
			require.Equal(t, req.Permission.Description, updated.Description)
			require.EqualValues(t, -10, updated.Sort)
			require.Equal(t, target.Name, updated.Name, "omitted metadata stays unchanged")
			require.Equal(t, target.Code, updated.Code)
			require.Equal(t, target.SupportedScopes, updated.SupportedScopes)
			listed, err := uc.Execute(f.ctx, raw, "ListPermissions", m.Request{Context: authorization.Platform(), Filter: `category = "review-category"`, OrderBy: "sort", PageSize: 1})
			require.NoError(t, err)
			require.EqualValues(t, 1, listed.Total)
			require.Equal(t, updated, listed.Permissions[0])
		})
	}
}
