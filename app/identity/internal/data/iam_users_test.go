package data

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
)

func TestIAMB1ManagedUsersDialects(t *testing.T) {
	for _, driver := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			f := newIAMA4Fixture(t, driver)
			_, err := f.uc.EnsureRootAdmin(f.ctx)
			require.NoError(t, err)
			manager, target, hidden := f.register("scoped-manager"), f.register("scoped-target"), f.register("hidden-target")
			roleID := f.role("scoped-users")
			f.assign(manager.ID, roleID, time.Now().Add(-time.Hour), nil)
			repo := NewIAMManagementRepo(f.repo.(*iamRepo).data)
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				roles, err := repo.Roles(ctx, tx, authorization.Platform())
				if err != nil {
					return err
				}
				for _, role := range roles {
					if role.ID != roleID {
						continue
					}
					for _, op := range []string{"identity.user.list", "identity.user.read", "identity.user.update", "identity.user.email_binding.update"} {
						role.Grants = append(role.Grants, biz.IAMGrant{Operation: op, Effect: authorization.Allow, Scope: authorization.Scope{Clauses: []authorization.Clause{{UserIDs: []int64{target.ID}}}}})
					}
					for _, op := range []string{"identity.routing_access.read", "identity.routing_access.grant", "identity.routing_access.default.update", "identity.routing_access.revoke"} {
						scope := authorization.Scope{Clauses: []authorization.Clause{{UserIDs: []int64{target.ID}, RoutingGroupIDs: []int64{11}}}}
						role.Grants = append(role.Grants, biz.IAMGrant{Operation: op, Effect: authorization.Allow, Scope: scope})
					}
					_, err = repo.SaveManagedRole(ctx, tx, role, role.Revision)
					return err
				}
				return biz.ErrIAMNotFound
			})
			require.NoError(t, f.db.Table("iam_permissions").Where("status = ?", "draft").Update("status", "enabled").Error)
			f.mode("iam", "complete")
			raw := f.token(manager.ID, "b1-scoped", 0, time.Now().Add(time.Hour))
			ctx := authorization.WithCredential(f.ctx, raw)
			users, total, err := f.uc.ListManagedUsers(ctx, 1, 1, "", "", 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, users, 1)
			require.Equal(t, target.ID, users[0].ID)
			require.Empty(t, users[0].Email)
			users, total, err = f.uc.ListManagedUsers(ctx, 2, 1, "", "", 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Empty(t, users)
			_, err = f.uc.GetManagedUser(ctx, hidden.ID)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			visible, err := f.uc.GetManagedUser(ctx, target.ID)
			require.NoError(t, err)
			require.Empty(t, visible.Email)
			_, err = f.uc.GetResourceAuthorization(ctx, raw, authorization.ResourceRequest{ExecutionPoint: "identity.users.read", Operation: "identity.user.delete"})
			require.Error(t, err)
			u, p := f.versions(target.ID)
			patch := biz.ManagedUserPatch{DisplayName: "scoped edit", Fields: []string{"display_name"}, ExpectedRevision: u, ExpectedPolicyRevision: p, Reason: "acceptance"}
			require.NoError(t, f.uc.UpdateManagedUser(ctx, target.ID, patch))
			stored, err := f.uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			require.Equal(t, "scoped edit", stored.DisplayName)
			require.Equal(t, target.Email, stored.Email)
			require.Equal(t, target.Status, stored.Status)
			require.ErrorIs(t, f.uc.UpdateManagedUser(ctx, target.ID, patch), biz.ErrIAMRevisionConflict)
			u, p = f.versions(target.ID)
			patch = biz.ManagedUserPatch{Email: "", Fields: []string{"email"}, ExpectedRevision: u, ExpectedPolicyRevision: p, Reason: "cannot take over without delegation"}
			require.ErrorIs(t, f.uc.UpdateManagedUser(ctx, target.ID, patch), biz.ErrIAMProtected)
			stored, err = f.uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			require.Equal(t, target.Email, stored.Email)
			rootRaw := f.token(1, "b1-root", 0, time.Now().Add(time.Hour))
			rootCtx := authorization.WithCredential(f.ctx, rootRaw)
			targetRaw := f.token(target.ID, "b1-target", 0, time.Now().Add(time.Hour))
			_, err = f.uc.GetSessionAuthorization(f.ctx, targetRaw, authorization.Platform())
			require.NoError(t, err)
			u, p = f.versions(target.ID)
			patch.ExpectedRevision, patch.ExpectedPolicyRevision = u, p
			patch.Reason = "explicitly clear email"
			require.NoError(t, f.uc.UpdateManagedUser(rootCtx, target.ID, patch))
			stored, err = f.uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			require.Empty(t, stored.Email)
			require.Positive(t, stored.PasswordChangedAt)
			_, err = f.uc.GetSessionAuthorization(f.ctx, targetRaw, authorization.Platform())
			require.Error(t, err)
			// Exercise a real, independently scoped credential delegation.
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				ceilings := []m.Ceiling{}
				for _, operation := range authorization.Catalog() {
					ceilings = append(ceilings, m.Ceiling{Context: authorization.Platform(), Operation: operation.Code, Scope: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}})
				}
				_, err := repo.SaveDelegation(ctx, tx, m.Delegation{Context: authorization.Platform(), ManagerRoleID: roleID, TargetKind: "user_credentials", Actions: []string{"identity.user.email_binding.update"}, TargetUserScope: authorization.Scope{Clauses: []authorization.Clause{{UserIDs: []int64{target.ID}}}}, GrantCeiling: ceilings, Validity: authorization.Interval{StartsAt: time.Now().Add(-time.Minute)}}, 0)
				return err
			})
			u, p = f.versions(target.ID)
			delegatedPatch := biz.ManagedUserPatch{Email: "delegated-contact@example.com", Fields: []string{"email"}, ExpectedRevision: u, ExpectedPolicyRevision: p, Reason: "authorized delegated credential change"}
			require.NoError(t, f.uc.UpdateManagedUser(ctx, target.ID, delegatedPatch))
			stored, err = f.uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			require.Equal(t, delegatedPatch.Email, stored.Email)
			// A future authority attached to any target role blocks takeover,
			// even when that authority is not active at the request time.
			f.setup(func(ctx context.Context, tx biz.IAMTx) error {
				roles, err := repo.Roles(ctx, tx, authorization.Platform())
				if err != nil {
					return err
				}
				for _, role := range roles {
					if role.Builtin && role.Code == "member" {
						_, err = repo.SaveDelegation(ctx, tx, m.Delegation{Context: authorization.Platform(), ManagerRoleID: role.ID, TargetKind: "role_creation", Actions: []string{"iam.role.create"}, TargetUserScope: authorization.Scope{Clauses: []authorization.Clause{{All: true}}}, Validity: authorization.Interval{StartsAt: time.Now().Add(time.Hour)}}, 0)
						return err
					}
				}
				return biz.ErrIAMNotFound
			})
			u, p = f.versions(target.ID)
			delegatedPatch.ExpectedRevision, delegatedPatch.ExpectedPolicyRevision = u, p
			delegatedPatch.Email = "must-not-replace@example.com"
			require.ErrorIs(t, f.uc.UpdateManagedUser(ctx, target.ID, delegatedPatch), biz.ErrIAMProtected)
			u, p = f.versions(1)
			patch.ExpectedRevision, patch.ExpectedPolicyRevision = u, p
			require.ErrorIs(t, f.uc.UpdateManagedUser(rootCtx, 1, patch), biz.ErrIAMProtected)
			// Any storage failure rolls back the account, authorization revision and
			// audit together; it cannot leave a successful partial profile update.
			u, p = f.versions(hidden.ID)
			patch = biz.ManagedUserPatch{DisplayName: "must roll back", Fields: []string{"display_name"}, ExpectedRevision: u, ExpectedPolicyRevision: p, Reason: "failure injection"}
			remove := f.failCreate("iam_audit_events")
			require.Error(t, f.uc.UpdateManagedUser(rootCtx, hidden.ID, patch))
			remove()
			stored, err = f.uc.GetUser(f.ctx, hidden.ID)
			require.NoError(t, err)
			require.NotEqual(t, "must roll back", stored.DisplayName)
			u2, p2 := f.versions(hidden.ID)
			require.Equal(t, u, u2)
			require.Equal(t, p, p2)
			u, p = f.versions(hidden.ID)
			credentialPatch := biz.ManagedUserPatch{Password: "rotated-test-password", Fields: []string{"password"}, ExpectedRevision: u, ExpectedPolicyRevision: p, Reason: "credential acceptance"}
			require.NoError(t, f.uc.UpdateManagedUser(rootCtx, hidden.ID, credentialPatch))
			stored, err = f.uc.GetUser(f.ctx, hidden.ID)
			require.NoError(t, err)
			require.NoError(t, bcrypt.CompareHashAndPassword([]byte(stored.PasswordHash), []byte("rotated-test-password")))
			var payloads []iamAuditModel
			require.NoError(t, f.db.Table("iam_audit_events").Where("action = ?", "identity.user.update").Scan(&payloads).Error)
			for _, event := range payloads {
				payload := event.BeforeData + event.AfterData + event.Diff
				require.NotContains(t, payload, "rotated-test-password")
				require.NotContains(t, payload, stored.PasswordHash)
			}
			// Self profile writes preserve owner/current-password checks even
			// when no management role has been granted.
			require.ErrorIs(t, f.uc.UpdateSelf(ctx, target.ID, "", "forged self edit", "", "", true), biz.ErrIAMProtected)
			targetUser, err := f.uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			selfRaw := f.token(target.ID, "b1-current-self", targetUser.PasswordChangedAt, time.Now().Add(time.Hour))
			selfCtx := authorization.WithCredential(f.ctx, selfRaw)
			require.NoError(t, f.uc.UpdateSelf(selfCtx, target.ID, "", "verified self edit", "", "", true))
			targetUser, err = f.uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			require.Equal(t, "verified self edit", targetUser.DisplayName)
			// Exercise the owner's actual default/grant relation SQL. A matching
			// allow group never cancels a deny on another shared group; expired
			// grants do not make users visible, and total precedes pagination.
			require.NoError(t, f.db.Table("users").Where("id = ?", target.ID).Update("default_routing_group_id", 11).Error)
			for _, grant := range []routingGrantModel{{UserID: hidden.ID, RoutingGroupID: 11, SourceType: "admin", SourceRef: "b1", Status: "active"}, {UserID: hidden.ID, RoutingGroupID: 12, SourceType: "admin", SourceRef: "b1", Status: "active"}, {UserID: manager.ID, RoutingGroupID: 11, SourceType: "admin", SourceRef: "expired", Status: "active", ExpiresAt: time.Now().Add(-time.Minute).Unix()}} {
				require.NoError(t, f.db.Create(&grant).Error)
			}
			queryScope := authorization.QueryScope{ActorID: manager.ID, Allow: []authorization.Scope{{Clauses: []authorization.Clause{{RoutingGroupIDs: []int64{11}}}}}, Deny: []authorization.Scope{{Clauses: []authorization.Clause{{RoutingGroupIDs: []int64{12}}}}}}
			scopedCtx := authorization.WithQueryScope(f.ctx, "identity.user.list", queryScope)
			ownerRepo := NewRepository(f.repo.(*iamRepo).data)
			rows, total, err := ownerRepo.ListUsers(scopedCtx, 1, 1, "", "", 0)
			require.NoError(t, err)
			require.EqualValues(t, 1, total)
			require.Len(t, rows, 1)
			require.Equal(t, target.ID, rows[0].ID)
			// Recovery is tied to the binding captured when its one-use mail
			// challenge was issued. Raw email strings never prove identity.
			require.ErrorIs(t, f.uc.ResetPasswordByEmail(f.ctx, hidden.Email, "recovered-password"), biz.ErrInvalidToken)
			hiddenUser, err := f.uc.GetUser(f.ctx, hidden.ID)
			require.NoError(t, err)
			proof := biz.WithVerifiedEmailRecovery(f.ctx, hidden.Email, time.Now(), hidden.ID, hiddenUser.PasswordChangedAt)
			require.NoError(t, f.uc.ResetPasswordByEmail(proof, hidden.Email, "recovered-password"))
			require.ErrorIs(t, f.uc.ResetPasswordByEmail(proof, hidden.Email, "reuse-recovery-password"), biz.ErrIAMRevisionConflict)
			hiddenUser, err = f.uc.GetUser(f.ctx, hidden.ID)
			require.NoError(t, err)
			oauthRaw := f.token(hidden.ID, "oauth-binding-session", hiddenUser.PasswordChangedAt, time.Now().Add(time.Hour))
			oauthCtx := authorization.WithCredential(f.ctx, oauthRaw)
			_, err = f.uc.BindOAuthIdentity(oauthCtx, target.ID, "github", "forged-owner")
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			_, err = f.uc.BindOAuthIdentity(oauthCtx, hidden.ID, "github", "verified-provider-subject")
			require.NoError(t, err)
			_, err = f.uc.GetSessionAuthorization(f.ctx, oauthRaw, authorization.Platform())
			require.Error(t, err)
			// Creation never grants numerical roles or accepts a balance write.
			_, p = f.versions(1)
			create := biz.ManagedUserCreate{Username: "managed-created", Password: "managed-password", ExpectedPolicyRevision: p, Reason: "create member"}
			_, err = f.uc.CreateManagedUser(ctx, create)
			require.ErrorIs(t, err, biz.ErrIAMProtected)
			remove = f.failCreate("iam_audit_events")
			_, err = f.uc.CreateManagedUser(rootCtx, create)
			require.Error(t, err)
			remove()
			var count int64
			require.NoError(t, f.db.Table("users").Where("username = ?", create.Username).Count(&count).Error)
			require.Zero(t, count)
			created, err := f.uc.CreateManagedUser(rootCtx, create)
			require.NoError(t, err)
			require.EqualValues(t, biz.RoleCommonUser, created.Role)
			require.NoError(t, f.db.Table("iam_user_roles").Where("user_id = ? AND origin = ?", created.ID, "default").Count(&count).Error)
			require.EqualValues(t, 1, count)
			_, err = f.uc.CreateManagedUser(rootCtx, biz.ManagedUserCreate{Username: "stale-create", Password: "managed-password", ExpectedPolicyRevision: p, Reason: "stale"})
			require.ErrorIs(t, err, biz.ErrIAMRevisionConflict)
			createdRaw := f.token(created.ID, "created-session", 0, time.Now().Add(time.Hour))
			_, err = f.uc.GetSessionAuthorization(f.ctx, createdRaw, authorization.Platform())
			require.NoError(t, err)
			u, p = f.versions(created.ID)
			require.ErrorIs(t, f.uc.DeleteManagedUser(ctx, created.ID, u, p, "out of scope"), biz.ErrIAMProtected)
			uRoot, pRoot := f.versions(1)
			require.ErrorIs(t, f.uc.DeleteManagedUser(rootCtx, 1, uRoot, pRoot, "protect root"), biz.ErrIAMProtected)
			remove = f.failCreate("iam_audit_events")
			require.Error(t, f.uc.DeleteManagedUser(rootCtx, created.ID, u, p, "rollback delete"))
			remove()
			_, err = f.uc.GetUser(f.ctx, created.ID)
			require.NoError(t, err)
			require.NoError(t, f.uc.DeleteManagedUser(rootCtx, created.ID, u, p, "delete acceptance"))
			_, err = f.uc.GetSessionAuthorization(f.ctx, createdRaw, authorization.Platform())
			require.Error(t, err)
			// Intrinsic self operations do not consume admin grants.
			require.ErrorIs(t, f.uc.DeleteSelf(selfCtx, hidden.ID), biz.ErrIAMProtected)
			require.NoError(t, f.uc.InvalidateAllSessions(selfCtx, target.ID))
			_, err = f.uc.GetSessionAuthorization(f.ctx, selfRaw, authorization.Platform())
			require.Error(t, err)
			targetUser, err = f.uc.GetUser(f.ctx, target.ID)
			require.NoError(t, err)
			freshSelf := authorization.WithCredential(f.ctx, f.token(target.ID, "self-delete-session", targetUser.PasswordChangedAt, time.Now().Add(time.Hour)))
			require.NoError(t, f.uc.DeleteSelf(freshSelf, target.ID))
			_, err = f.uc.GetUser(f.ctx, target.ID)
			require.Error(t, err)

		})
	}
}
