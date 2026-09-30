package data

import (
	"context"
	"errors"
	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"slices"
	"strings"
)

type iamRepo struct{ data *Data }

func NewIAMRepo(d *Data) biz.IAMRepo { return &iamRepo{data: d} }

func iamRelationError(tx *iamTx, err error) error {
	if err == nil {
		return nil
	}
	var my *mysql.MySQLError
	var pg *pgconn.PgError
	var sq sqlite3.Error
	if (errors.As(err, &my) && slices.Contains([]uint16{1062, 1451, 1452, 3819}, my.Number)) ||
		(errors.As(err, &pg) && slices.Contains([]string{"23505", "23503", "23514", "23502"}, pg.Code)) ||
		(errors.As(err, &sq) && sq.Code == sqlite3.ErrConstraint) {
		return biz.ErrIAMInvalidRelation
	}
	return iamStorageError(tx, err)
}

func (r *iamRepo) Policy(ctx context.Context, handle biz.IAMTx) (authorization.PolicyState, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return authorization.PolicyState{}, err
	}
	var p iamPolicyModel
	if err := tx.db.First(&p, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return authorization.PolicyState{}, biz.ErrIAMDependencyUnavailable
		}
		return authorization.PolicyState{}, iamStorageError(tx, err)
	}
	s := p.toBiz()
	if s.Validate() != nil {
		return s, biz.ErrIAMCutoverBlocked
	}
	return s, nil
}
func (r *iamRepo) AdvancePolicy(ctx context.Context, handle biz.IAMTx, expected uint64, catalog bool) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	if expected == 0 {
		return biz.ErrIAMRevisionConflict
	}
	updates := map[string]any{"policy_revision": gorm.Expr("policy_revision + 1")}
	if catalog {
		updates["catalog_revision"] = gorm.Expr("catalog_revision + 1")
	}
	result := tx.db.Model(&iamPolicyModel{}).Where("id = 1 AND policy_revision = ?", expected).Updates(updates)
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	if result.RowsAffected != 1 {
		return biz.ErrIAMRevisionConflict
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) UserRevision(ctx context.Context, handle biz.IAMTx, id int64) (uint64, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return 0, err
	}
	var p struct{ AuthorizationRevision uint64 }
	result := tx.db.Table("users").Select("authorization_revision").Where("id = ?", id).Take(&p)
	if result.Error != nil {
		return 0, iamStorageError(tx, result.Error)
	}
	if p.AuthorizationRevision == 0 {
		return 0, biz.ErrIAMInvalidRelation
	}
	return p.AuthorizationRevision, nil
}
func (r *iamRepo) AdvanceUser(ctx context.Context, handle biz.IAMTx, id int64, expected uint64) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	if id <= 0 || expected == 0 {
		return biz.ErrIAMRevisionConflict
	}
	result := tx.db.Table("users").Where("id = ? AND authorization_revision = ?", id, expected).UpdateColumn("authorization_revision", gorm.Expr("authorization_revision + 1"))
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	if result.RowsAffected != 1 {
		return biz.ErrIAMRevisionConflict
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) Roles(ctx context.Context, handle biz.IAMTx, c authorization.Context) ([]biz.IAMRole, error) {
	if c.RequirePlatform() != nil {
		return nil, biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return nil, err
	}
	var rows []iamRoleModel
	if err := tx.db.Where("context_key = ?", c.Key).Order("id").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := make([]biz.IAMRole, 0, len(rows))
	ids := map[int64]int{}
	for _, p := range rows {
		if p.toBiz().Context != c {
			return nil, biz.ErrIAMContextInvalid
		}
		ids[p.ID] = len(out)
		out = append(out, p.toBiz())
	}
	var edges []struct{ SeniorRoleID, JuniorRoleID int64 }
	if err := tx.db.Table("iam_role_inheritance").Where("context_key = ?", c.Key).Order("senior_role_id, junior_role_id").Find(&edges).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	for _, e := range edges {
		i, ok := ids[e.SeniorRoleID]
		if !ok {
			return nil, biz.ErrIAMInvalidRelation
		}
		out[i].Inherits = append(out[i].Inherits, e.JuniorRoleID)
	}
	var grants []struct {
		RoleID                        int64
		Code, Effect, ScopeDescriptor string
	}
	if err := tx.db.Table("iam_role_permissions AS rp").Select("rp.role_id, p.code, rp.effect, rp.scope_descriptor").Joins("JOIN iam_permissions p ON p.id = rp.permission_id").Where("rp.context_key = ?", c.Key).Order("rp.role_id, p.code, rp.effect").Find(&grants).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	for _, g := range grants {
		i, ok := ids[g.RoleID]
		if !ok {
			return nil, biz.ErrIAMInvalidRelation
		}
		op, ok := authorization.Lookup(g.Code)
		if !ok || !slices.Contains(op.ContextTypes, c.Type) {
			return nil, biz.ErrIAMInvalidRelation
		}
		scope, err := authorization.ParseScope([]byte(g.ScopeDescriptor), op.Scopes)
		if err != nil {
			return nil, biz.ErrIAMScopeInvalid
		}
		out[i].Grants = append(out[i].Grants, biz.IAMGrant{Operation: g.Code, Effect: authorization.Effect(g.Effect), Scope: scope})
	}
	return out, nil
}
func (r *iamRepo) Assignments(ctx context.Context, handle biz.IAMTx, c authorization.Context, userID int64) ([]biz.IAMAssignment, error) {
	if c.RequirePlatform() != nil {
		return nil, biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return nil, err
	}
	var rows []iamAssignmentModel
	if err := tx.db.Where("context_key = ? AND user_id = ?", c.Key, userID).Order("id").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := make([]biz.IAMAssignment, 0, len(rows))
	for _, p := range rows {
		a, err := p.toBiz()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}

// SaveRole only stores metadata. Grants/graph changes require the A3 governance
// path; reject those fields instead of silently committing a partial role.
func (r *iamRepo) SaveRole(ctx context.Context, handle biz.IAMTx, role biz.IAMRole, expected uint64) (biz.IAMRole, error) {
	if role.Context.RequirePlatform() != nil {
		return biz.IAMRole{}, biz.ErrIAMContextInvalid
	}
	if len(role.Grants) > 0 || len(role.Inherits) > 0 || role.CreationDelegationID != 0 {
		return biz.IAMRole{}, biz.ErrIAMInvalidRelation
	}
	if role.Builtin || role.Code == "root" {
		return biz.IAMRole{}, biz.ErrIAMProtected
	}
	if strings.TrimSpace(role.Code) == "" || !slices.Contains([]string{"draft", "enabled", "disabled", "archived"}, role.Status) {
		return biz.IAMRole{}, biz.ErrIAMInvalidRelation
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return biz.IAMRole{}, err
	}
	p := newIAMRole(role)
	if role.ID == 0 {
		if expected != 0 {
			return biz.IAMRole{}, biz.ErrIAMRevisionConflict
		}
		p.Revision = 1
		if err := tx.db.Create(&p).Error; err != nil {
			return biz.IAMRole{}, iamRelationError(tx, err)
		}
	} else {
		var current iamRoleModel
		if err := tx.db.First(&current, role.ID).Error; err != nil {
			return biz.IAMRole{}, iamStorageError(tx, err)
		}
		if current.Builtin != 0 || current.CreationDelegationID != nil {
			return biz.IAMRole{}, biz.ErrIAMProtected
		}
		if current.ContextKey != role.Context.Key || current.Code != role.Code {
			return biz.IAMRole{}, biz.ErrIAMInvalidRelation
		}
		if expected == 0 || expected != current.Revision {
			return biz.IAMRole{}, biz.ErrIAMRevisionConflict
		}
		p.Revision = expected + 1
		result := tx.db.Model(&iamRoleModel{}).Where("id = ? AND revision = ?", role.ID, expected).Updates(map[string]any{"name": p.Name, "description": p.Description, "status": p.Status, "max_members": p.MaxMembers, "revision": p.Revision})
		if result.Error != nil {
			return biz.IAMRole{}, iamRelationError(tx, result.Error)
		}
		if result.RowsAffected != 1 {
			return biz.IAMRole{}, biz.ErrIAMRevisionConflict
		}
	}
	policy, err := r.Policy(ctx, handle)
	if err != nil {
		return biz.IAMRole{}, err
	}
	if err := r.AdvancePolicy(ctx, handle, policy.PolicyRevision, false); err != nil {
		return biz.IAMRole{}, err
	}
	return p.toBiz(), nil
}
func (r *iamRepo) SaveAssignment(ctx context.Context, handle biz.IAMTx, a biz.IAMAssignment, expected uint64) (biz.IAMAssignment, error) {
	if a.Context.RequirePlatform() != nil {
		return biz.IAMAssignment{}, biz.ErrIAMContextInvalid
	}
	if a.UserID <= 0 || a.RoleID <= 0 || a.Validity.Validate() != nil || a.Validity.StartsAt.UnixMilli() <= 0 || authorization.ValidateOrigin(a.Origin, a.MigrationBatchID) != nil {
		return biz.IAMAssignment{}, biz.ErrIAMInvalidRelation
	}
	if a.Boundary.Clauses == nil || a.Boundary.Validate(iamScopeKinds) != nil {
		return biz.IAMAssignment{}, biz.ErrIAMScopeInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return biz.IAMAssignment{}, err
	}
	p, err := newIAMAssignment(a)
	if err != nil {
		return biz.IAMAssignment{}, err
	}
	if a.ID == 0 {
		if expected != 0 {
			return biz.IAMAssignment{}, biz.ErrIAMRevisionConflict
		}
		p.Revision = 1
		if err := tx.db.Create(&p).Error; err != nil {
			return biz.IAMAssignment{}, iamRelationError(tx, err)
		}
	} else {
		var current iamAssignmentModel
		if err := tx.db.First(&current, a.ID).Error; err != nil {
			return biz.IAMAssignment{}, iamStorageError(tx, err)
		}
		if current.ContextKey != a.Context.Key || current.UserID != a.UserID || current.RoleID != a.RoleID || current.Origin != a.Origin || current.MigrationBatchID != a.MigrationBatchID {
			return biz.IAMAssignment{}, biz.ErrIAMInvalidRelation
		}
		if expected == 0 || expected != current.Revision {
			return biz.IAMAssignment{}, biz.ErrIAMRevisionConflict
		}
		p.Revision = expected + 1
		result := tx.db.Model(&iamAssignmentModel{}).Where("id = ? AND revision = ?", a.ID, expected).Updates(map[string]any{"allow_boundary": p.AllowBoundary, "starts_at": p.StartsAt, "expires_at": p.ExpiresAt, "status": p.Status, "revision": p.Revision, "assigned_by": p.AssignedBy})
		if result.Error != nil {
			return biz.IAMAssignment{}, iamRelationError(tx, result.Error)
		}
		if result.RowsAffected != 1 {
			return biz.IAMAssignment{}, biz.ErrIAMRevisionConflict
		}
	}
	revision, err := r.UserRevision(ctx, handle, a.UserID)
	if err != nil {
		return biz.IAMAssignment{}, err
	}
	if err := r.AdvanceUser(ctx, handle, a.UserID, revision); err != nil {
		return biz.IAMAssignment{}, err
	}
	return p.toBiz()
}
func validateIAMAudit(e biz.IAMAuditEvent) error {
	if e.Context.RequirePlatform() != nil || e.TargetContext.RequirePlatform() != nil {
		return biz.ErrIAMContextInvalid
	}
	if strings.TrimSpace(e.EventID) == "" || strings.TrimSpace(e.Action) == "" || strings.TrimSpace(e.Reason) == "" || e.OccurredAt.IsZero() || e.OccurredAt.UnixMilli() <= 0 || !slices.Contains([]string{"success", "failure"}, e.Result) {
		return biz.ErrIAMInvalidRelation
	}
	return nil
}
func (r *iamRepo) AppendAudit(ctx context.Context, handle biz.IAMTx, e biz.IAMAuditEvent) error {
	if err := validateIAMAudit(e); err != nil {
		return err
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	p, err := newIAMAudit(e)
	if err != nil {
		return err
	}
	if err := tx.db.Create(&p).Error; err != nil {
		return iamRelationError(tx, err)
	}
	if e.Result == "success" {
		tx.audited = true
	}
	return nil
}
func (r *iamRepo) AppendFailureAudit(ctx context.Context, e biz.IAMAuditEvent) error {
	if e.Result != "failure" {
		return biz.ErrIAMInvalidRelation
	}
	// Deliberately independent: called after the failed business transaction.
	return NewIAMTxRunner(r.data).RunIAMWrite(ctx, func(ctx context.Context, tx biz.IAMTx) error { return r.AppendAudit(ctx, tx, e) })
}
func (r *iamRepo) AuditEvents(ctx context.Context, handle biz.IAMTx, c authorization.Context, limit int) ([]biz.IAMAuditEvent, error) {
	if c.RequirePlatform() != nil {
		return nil, biz.ErrIAMContextInvalid
	}
	if limit < 1 || limit > 1000 {
		return nil, biz.ErrIAMInvalidRelation
	}
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return nil, err
	}
	var rows []iamAuditModel
	if err := tx.db.Where("target_context_key = ?", c.Key).Order("occurred_at DESC, event_id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := make([]biz.IAMAuditEvent, 0, len(rows))
	for _, p := range rows {
		e, err := p.toBiz()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

// Account adapters join the existing account/routing helpers to the caller's
// transaction. GORM's nested routing transaction is a savepoint on this handle,
// so user, grants, OAuth binding, assignment, versions and audit commit together.
// Returning values never mutates caller-owned inputs during a replay.
func (r *iamRepo) CountUsers(ctx context.Context, handle biz.IAMTx) (int64, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return 0, err
	}
	var count int64
	err = tx.db.Table("users").Count(&count).Error
	return count, iamStorageError(tx, err)
}
func (r *iamRepo) CreateUser(ctx context.Context, handle biz.IAMTx, user biz.User) (biz.User, error) {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return biz.User{}, err
	}
	if user.ID != 0 {
		return biz.User{}, biz.ErrIAMInvalidRelation
	}
	bound := NewRepository(&Data{db: tx.db, redis: r.data.redis})
	if err := bound.createUserDB(ctx, &user); err != nil {
		return biz.User{}, iamRelationError(tx, err)
	}
	tx.mutated = true
	return user, nil
}
func (r *iamRepo) CreateOAuthIdentity(ctx context.Context, handle biz.IAMTx, identity biz.OAuthIdentity) (biz.OAuthIdentity, error) {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return biz.OAuthIdentity{}, err
	}
	if identity.ID != 0 || identity.UserID <= 0 {
		return biz.OAuthIdentity{}, biz.ErrIAMInvalidRelation
	}
	bound := NewRepository(&Data{db: tx.db, redis: r.data.redis})
	if err := bound.createOAuthIdentityDB(ctx, &identity); err != nil {
		return biz.OAuthIdentity{}, iamRelationError(tx, err)
	}
	tx.mutated = true
	return identity, nil
}

// User returns authoritative identity facts in the same view as IAM relations
// and versions; callers must not mix a standalone account read into this view.
func (r *iamRepo) User(ctx context.Context, handle biz.IAMTx, id int64) (biz.User, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return biz.User{}, err
	}
	var user userModel
	if err := tx.db.First(&user, id).Error; err != nil {
		return biz.User{}, iamStorageError(tx, err)
	}
	return *userModelToBiz(user), nil
}
