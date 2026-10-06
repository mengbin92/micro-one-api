package data

import (
	"context"
	"github.com/stretchr/testify/require"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"testing"
	"time"
)

func TestIAMB1ExportUsersOwnerDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			actor, target, hidden := f.register("export-only"), f.register("export-target"), f.register("export-hidden")
			role := f.role("export-role")
			f.assign(actor.ID, role, time.Now().Add(-time.Hour), nil)
			management := NewIAMManagementRepo(f.repo.(*iamRepo).data)
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				roles, err := management.Roles(ctx, tx, authorization.Platform())
				if err != nil {
					return err
				}
				for _, r := range roles {
					if r.ID == role {
						r.Grants = append(r.Grants, biz.IAMGrant{Operation: "identity.user.export", Effect: authorization.Allow, Scope: authorization.Scope{Clauses: []authorization.Clause{{UserIDs: []int64{target.ID}}}}})
						_, err = management.SaveManagedRole(ctx, tx, r, r.Revision)
						return err
					}
				}
				return biz.ErrIAMNotFound
			})
			require.NoError(t, f.db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
			f.mode("iam", "complete")
			ctx := authorization.WithCredential(authorization.WithExternal(f.ctx), f.token(actor.ID, "export-only", 0, time.Now().Add(time.Hour)))
			_, _, err = f.uc.ListManagedUsers(ctx, 1, 20, "", "", 0)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			rows, total, err := f.uc.ExportManagedUsers(ctx, 1, 1, "", "", 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			require.Equal(t, target.ID, rows[0].ID)
			require.Empty(t, rows[0].Email)
			require.Empty(t, rows[0].PasswordHash)
			require.NotEqual(t, hidden.ID, rows[0].ID)
			rows, total, err = f.uc.ExportManagedUsers(ctx, 2, 1, "", "", 0)
			require.NoError(t, err)
			require.Empty(t, rows)
			require.EqualValues(t, 1, total)
			_, _, err = f.uc.ExportManagedUsers(ctx, 1, 201, "", "", 0)
			require.ErrorIs(t, err, biz.ErrIAMInvalidRelation)
			noProof := authorization.WithExternal(f.ctx)
			_, _, err = f.uc.ExportManagedUsers(noProof, 1, 20, "", "", 0)
			require.Error(t, err)
		})
	}
}
