package data

import (
	"context"
	"errors"
	"slices"

	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/pkg/jsonx"
	"micro-one-api/platform/database/xdb"
)

func NewIAMMigrationRepo(d *Data) biz.IAMMigrationRepo { return &iamRepo{data: d} }

func (r *iamRepo) MigrationReceipt(ctx context.Context, h biz.IAMTx, id, digest string) (*biz.IAMMigrationReport, error) {
	tx, err := iamDB(ctx, r.data, h, false)
	if err != nil {
		return nil, err
	}
	var row iamAuditModel
	err = tx.db.Where("event_id = ? AND actor_service_id = ? AND result = ?", id, "iam-migrate", "success").Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, iamStorageError(tx, err)
	}
	var meta struct{ RequestDigest string }
	var report biz.IAMMigrationReport
	if jsonx.Unmarshal([]byte(row.BeforeData), &meta) != nil || meta.RequestDigest != digest {
		return nil, biz.ErrIAMRevisionConflict
	}
	if jsonx.Unmarshal([]byte(row.AfterData), &report) != nil {
		return nil, biz.ErrIAMDependencyUnavailable
	}
	return &report, nil
}

// OpenIAMMigrationStorage opens only the explicitly selected dedicated channel.
// It never runs bootstrap, token backfill, Redis, or automatic schema changes.
func OpenIAMMigrationStorage(config xdb.DatabaseConfig) (biz.IAMMigrationRepo, biz.IAMTxRunner, func(), error) {
	db, err := xdb.Open(config)
	if err != nil {
		return nil, nil, nil, err
	}
	d := &Data{db: db}
	close := func() {
		if sqlDB, e := db.DB(); e == nil {
			_ = sqlDB.Close()
		}
	}
	return NewIAMMigrationRepo(d), NewIAMTxRunner(d), close, nil
}

func (r *iamRepo) MigrationUsers(ctx context.Context, h biz.IAMTx) ([]biz.IAMMigrationUser, error) {
	tx, err := iamDB(ctx, r.data, h, false)
	if err != nil {
		return nil, err
	}
	var users []struct {
		ID                    int64
		Role, Status          int32
		AuthorizationRevision uint64
	}
	if err = tx.db.Table("users").Select("id, role, status, authorization_revision").Order("id").Find(&users).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := make([]biz.IAMMigrationUser, 0, len(users))
	for _, u := range users {
		out = append(out, biz.IAMMigrationUser{ID: u.ID, Role: u.Role, Status: u.Status, Revision: u.AuthorizationRevision})
	}
	return out, nil
}

func (r *iamRepo) ReplaceCandidates(ctx context.Context, h biz.IAMTx, candidates []biz.IAMAssignment) error {
	tx, err := iamDB(ctx, r.data, h, true)
	if err != nil {
		return err
	}
	var old []iamAssignmentModel
	if err = tx.db.Where("origin = ?", "legacy_candidate").Order("id").Find(&old).Error; err != nil {
		return iamStorageError(tx, err)
	}
	var nonCandidates []iamAssignmentModel
	if err = tx.db.Where("origin <> ?", "legacy_candidate").Find(&nonCandidates).Error; err != nil {
		return iamStorageError(tx, err)
	}
	keep := map[int64]bool{}
	removed := map[int64]bool{}
	changed := map[int64]bool{}
	for _, a := range candidates {
		p, err := newIAMAssignment(a)
		if err != nil {
			return err
		}
		if a.Origin != "legacy_candidate" || a.Context.RequirePlatform() != nil || a.MigrationBatchID == "" {
			return biz.ErrIAMProtected
		}
		// Existing atomic default/bootstrap rows own the same unique relation.
		// Preserve their origin/ID; biz verifies their scope and current mapping.
		if slices.ContainsFunc(nonCandidates, func(o iamAssignmentModel) bool {
			return o.UserID == p.UserID && o.RoleID == p.RoleID && o.ContextKey == p.ContextKey
		}) {
			continue
		}
		match := slices.IndexFunc(old, func(o iamAssignmentModel) bool {
			return !keep[o.ID] && o.UserID == p.UserID && o.RoleID == p.RoleID && o.ContextKey == p.ContextKey && o.AllowBoundary == p.AllowBoundary && o.Status == "active" && o.StartsAt <= p.StartsAt && o.ExpiresAt == nil && o.MigrationBatchID == p.MigrationBatchID
		})
		if match >= 0 {
			keep[old[match].ID] = true
			continue
		}
		// A changed batch/boundary can still own this unique user/role key.
		// Remove that candidate in the same transaction before its replacement;
		// non-candidate origins were excluded above and can never be rewritten.
		for _, o := range old {
			if o.UserID == p.UserID && o.RoleID == p.RoleID && o.ContextKey == p.ContextKey {
				if err = tx.db.Where("id = ? AND origin = ?", o.ID, "legacy_candidate").Delete(&iamAssignmentModel{}).Error; err != nil {
					return iamStorageError(tx, err)
				}
				removed[o.ID] = true
				tx.mutated = true
			}
		}
		p.Revision = 1
		if err = tx.db.Create(&p).Error; err != nil {
			return iamRelationError(tx, err)
		}
		keep[p.ID] = true
		changed[p.UserID] = true
		tx.mutated = true
	}
	for _, p := range old {
		if !keep[p.ID] && !removed[p.ID] {
			if err = tx.db.Where("id = ? AND origin = ?", p.ID, "legacy_candidate").Delete(&iamAssignmentModel{}).Error; err != nil {
				return iamStorageError(tx, err)
			}
			changed[p.UserID] = true
			tx.mutated = true
		}
	}
	for _, a := range candidates {
		if changed[a.UserID] {
			rev, e := r.UserRevision(ctx, h, a.UserID)
			if e != nil {
				return e
			}
			if e = r.AdvanceUser(ctx, h, a.UserID, rev); e != nil {
				return e
			}
		}
	}
	// Deleted identities cannot retain a session that a future reused ID could
	// activate. Audit events intentionally have no user FK and are never deleted.
	for _, table := range []string{"iam_session_roles", "iam_session_contexts"} {
		result := tx.db.Exec("DELETE FROM " + table + " WHERE session_id IN (SELECT session_id FROM iam_sessions WHERE user_id NOT IN (SELECT id FROM users))")
		if result.Error != nil {
			return iamStorageError(tx, result.Error)
		}
		tx.mutated = tx.mutated || result.RowsAffected > 0
	}
	result := tx.db.Where("user_id NOT IN (SELECT id FROM users)").Delete(&iamSessionModel{})
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	tx.mutated = tx.mutated || result.RowsAffected > 0
	return nil
}

func (r *iamRepo) PublishMigrationCatalog(ctx context.Context, h biz.IAMTx, grants map[string][]biz.IAMGrant) error {
	tx, err := iamDB(ctx, r.data, h, true)
	if err != nil {
		return err
	}
	var permissions []iamPermissionModel
	if err = tx.db.Order("id").Find(&permissions).Error; err != nil {
		return iamStorageError(tx, err)
	}
	ids := map[string]int64{}
	allowed := map[string]bool{}
	for _, g := range grants["root"] {
		allowed[g.Operation] = true
	}
	changed := false
	for _, p := range permissions {
		ids[p.Code] = p.ID
		bound := biz.IAMExecutionBound(p.Code)
		status, binding := "draft", "unbound"
		if bound {
			binding = "bound"
			if allowed[p.Code] {
				status = "enabled"
			}
		}
		if p.Status == status && p.BindingState == binding {
			continue
		}
		if err = tx.db.Model(&iamPermissionModel{}).Where("id = ?", p.ID).Updates(map[string]any{"status": status, "binding_state": binding, "revision": gorm.Expr("revision + 1")}).Error; err != nil {
			return iamStorageError(tx, err)
		}
		tx.mutated = true
		changed = true
	}
	for _, code := range []string{"guest", "member", "platform_admin", "root"} {
		var role iamRoleModel
		if err = tx.db.Where("context_key = ? AND code = ? AND builtin = 1", "platform", code).Take(&role).Error; err != nil {
			return iamStorageError(tx, err)
		}
		if role.Status != "enabled" {
			return biz.ErrIAMProtected
		}
		var current []iamRolePermissionModel
		if err = tx.db.Where("role_id = ?", role.ID).Order("permission_id,effect").Find(&current).Error; err != nil {
			return iamStorageError(tx, err)
		}
		desired := []iamRolePermissionModel{}
		for _, g := range grants[code] {
			if ids[g.Operation] == 0 {
				return biz.ErrIAMInvalidRelation
			}
			b, e := jsonx.Marshal(g.Scope)
			if e != nil {
				return biz.ErrIAMScopeInvalid
			}
			desired = append(desired, iamRolePermissionModel{ContextKey: "platform", RoleID: role.ID, PermissionID: ids[g.Operation], Effect: string(g.Effect), ScopeDescriptor: string(b), Revision: role.Revision + 1})
		}
		slices.SortFunc(desired, func(a, b iamRolePermissionModel) int {
			if a.PermissionID < b.PermissionID {
				return -1
			}
			if a.PermissionID > b.PermissionID {
				return 1
			}
			return 0
		})
		if slices.EqualFunc(current, desired, func(a, b iamRolePermissionModel) bool {
			return a.PermissionID == b.PermissionID && a.Effect == b.Effect && a.ScopeDescriptor == b.ScopeDescriptor
		}) {
			continue
		}
		if err = tx.db.Where("role_id = ?", role.ID).Delete(&iamRolePermissionModel{}).Error; err != nil {
			return iamStorageError(tx, err)
		}
		if len(desired) > 0 {
			if err = tx.db.Create(&desired).Error; err != nil {
				return iamRelationError(tx, err)
			}
		}
		if err = tx.db.Model(&iamRoleModel{}).Where("id = ?", role.ID).UpdateColumn("revision", gorm.Expr("revision + 1")).Error; err != nil {
			return iamStorageError(tx, err)
		}
		tx.mutated = true
		changed = true
	}
	if changed {
		p, e := r.Policy(ctx, h)
		if e != nil {
			return e
		}
		return r.AdvancePolicy(ctx, h, p.PolicyRevision, true)
	}
	return nil
}

func (r *iamRepo) SetCutover(ctx context.Context, h biz.IAMTx, p authorization.PolicyState, expected uint64) error {
	tx, err := iamDB(ctx, r.data, h, true)
	if err != nil {
		return err
	}
	if p.Validate() != nil || expected == 0 {
		return biz.ErrIAMCutoverBlocked
	}
	var verified any
	if p.VerifiedAt != nil {
		verified = p.VerifiedAt.UnixMilli()
	}
	result := tx.db.Model(&iamPolicyModel{}).Where("id = 1 AND policy_revision = ?", expected).Updates(map[string]any{"authorization_mode": p.Mode, "cutover_state": p.Cutover, "cutover_batch_id": p.BatchID, "cutover_verified_at": verified, "policy_revision": expected + 1})
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	if result.RowsAffected != 1 {
		return biz.ErrIAMRevisionConflict
	}
	tx.mutated = true
	return nil
}
