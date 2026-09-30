package data

import (
	"context"
	"slices"
	"time"

	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
)

func NewIAMConstraintRepo(d *Data) biz.IAMConstraintRepo { return &iamRepo{data: d} }

type iamConstraintModel struct {
	ID                     int64
	ContextKey, Type, Name string
	MaxCount               int64
	Enabled                int32
	Revision               uint64
}

func (iamConstraintModel) TableName() string { return "iam_role_constraints" }

type iamConstraintMemberModel struct {
	ContextKey           string
	ConstraintID, RoleID int64
}

func (iamConstraintMemberModel) TableName() string { return "iam_role_constraint_members" }

type iamInheritanceModel struct {
	ContextKey                 string
	SeniorRoleID, JuniorRoleID int64
}

func (iamInheritanceModel) TableName() string { return "iam_role_inheritance" }

type iamSessionRoleModel struct {
	SessionID, ContextKey string
	RoleID                int64
}

func (iamSessionRoleModel) TableName() string { return "iam_session_roles" }

func (r *iamRepo) ConstraintState(ctx context.Context, handle biz.IAMTx, c authorization.Context) (biz.IAMConstraintState, error) {
	state := biz.IAMConstraintState{Context: c}
	if c.RequirePlatform() != nil {
		return state, biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return state, err
	}
	state.Roles, err = r.Roles(ctx, handle, c)
	if err != nil {
		return state, err
	}
	var policy iamPolicyModel
	if err := tx.db.First(&policy, 1).Error; err != nil {
		return state, iamStorageError(tx, err)
	}
	state.Limits = biz.IAMLimits{MaxRolesPerUser: policy.MaxRolesPerUser, MaxRolesPerSession: policy.MaxRolesPerSession}
	var assignments []iamAssignmentModel
	if err := tx.db.Where("context_key = ?", c.Key).Order("user_id, role_id, id").Find(&assignments).Error; err != nil {
		return state, iamStorageError(tx, err)
	}
	for _, p := range assignments {
		a, err := p.toBiz()
		if err != nil {
			return state, err
		}
		state.Assignments = append(state.Assignments, a)
	}
	var constraints []iamConstraintModel
	if err := tx.db.Where("context_key = ?", c.Key).Order("id").Find(&constraints).Error; err != nil {
		return state, iamStorageError(tx, err)
	}
	ids := map[int64]int{}
	for _, p := range constraints {
		ids[p.ID] = len(state.Constraints)
		state.Constraints = append(state.Constraints, biz.IAMRoleConstraint{ID: p.ID, Context: c, Kind: p.Type, Name: p.Name, MaxCount: p.MaxCount, Enabled: p.Enabled != 0, Revision: p.Revision})
	}
	var members []iamConstraintMemberModel
	if err := tx.db.Where("context_key = ?", c.Key).Order("constraint_id, role_id").Find(&members).Error; err != nil {
		return state, iamStorageError(tx, err)
	}
	for _, p := range members {
		i, ok := ids[p.ConstraintID]
		if !ok {
			return state, biz.ErrIAMInvalidRelation
		}
		state.Constraints[i].RoleIDs = append(state.Constraints[i].RoleIDs, p.RoleID)
	}
	var sessions []struct {
		SessionID                   string
		UserID, ExpiresAt           int64
		RevokedAt, ContextRevokedAt *int64
		ActivationState             string
		SessionRevision, Revision   uint64
	}
	if err := tx.db.Table("iam_sessions AS s").Select("s.session_id, s.user_id, s.expires_at, s.revoked_at, c.revoked_at AS context_revoked_at, c.activation_state, s.revision AS session_revision, c.revision").Joins("JOIN iam_session_contexts c ON c.session_id = s.session_id").Where("c.context_key = ?", c.Key).Order("s.user_id, s.session_id").Find(&sessions).Error; err != nil {
		return state, iamStorageError(tx, err)
	}
	sessionIDs := map[string]int{}
	for _, p := range sessions {
		sessionIDs[p.SessionID] = len(state.Sessions)
		s := biz.IAMSessionContext{SessionID: p.SessionID, UserID: p.UserID, Context: c, ExpiresAt: time.UnixMilli(p.ExpiresAt).UTC(), ActivationState: p.ActivationState, SessionRevision: p.SessionRevision, Revision: p.Revision}
		if p.RevokedAt != nil {
			at := time.UnixMilli(*p.RevokedAt).UTC()
			s.RevokedAt = &at
		}
		if p.ContextRevokedAt != nil {
			at := time.UnixMilli(*p.ContextRevokedAt).UTC()
			s.ContextRevokedAt = &at
		}
		state.Sessions = append(state.Sessions, s)
	}
	var active []iamSessionRoleModel
	if err := tx.db.Where("context_key = ?", c.Key).Order("session_id, role_id").Find(&active).Error; err != nil {
		return state, iamStorageError(tx, err)
	}
	for _, p := range active {
		i, ok := sessionIDs[p.SessionID]
		if !ok {
			return state, biz.ErrIAMInvalidRelation
		}
		state.Sessions[i].ActiveRoleIDs = append(state.Sessions[i].ActiveRoleIDs, p.RoleID)
	}
	return state, nil
}
func (r *iamRepo) SaveRoleTopology(ctx context.Context, handle biz.IAMTx, c authorization.Context, role biz.IAMRoleTopology, expected uint64) error {
	if c.RequirePlatform() != nil {
		return biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	var current iamRoleModel
	if err := tx.db.Where("context_key = ? AND id = ?", c.Key, role.ID).Take(&current).Error; err != nil {
		return iamStorageError(tx, err)
	}
	if current.Code == "root" {
		return biz.ErrIAMProtected
	}
	if expected == 0 || current.Revision != expected {
		return biz.ErrIAMRevisionConflict
	}
	result := tx.db.Model(&iamRoleModel{}).Where("context_key = ? AND id = ? AND revision = ?", c.Key, role.ID, expected).Updates(map[string]any{"status": role.Status, "max_members": role.MaxMembers, "revision": gorm.Expr("revision + 1")})
	if result.Error != nil {
		return iamRelationError(tx, result.Error)
	}
	if result.RowsAffected != 1 {
		return biz.ErrIAMRevisionConflict
	}
	if err := tx.db.Where("context_key = ? AND senior_role_id = ?", c.Key, role.ID).Delete(&iamInheritanceModel{}).Error; err != nil {
		return iamStorageError(tx, err)
	}
	children := slices.Clone(role.Inherits)
	slices.Sort(children)
	for _, id := range children {
		if err := tx.db.Create(&iamInheritanceModel{ContextKey: c.Key, SeniorRoleID: role.ID, JuniorRoleID: id}).Error; err != nil {
			return iamRelationError(tx, err)
		}
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) SaveConstraint(ctx context.Context, handle biz.IAMTx, c biz.IAMRoleConstraint, expected uint64) error {
	if c.Context.RequirePlatform() != nil {
		return biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	p := iamConstraintModel{ID: c.ID, ContextKey: c.Context.Key, Type: c.Kind, Name: c.Name, MaxCount: c.MaxCount, Enabled: boolInt(c.Enabled), Revision: 1}
	if c.ID == 0 {
		if expected != 0 {
			return biz.ErrIAMRevisionConflict
		}
		if err := tx.db.Create(&p).Error; err != nil {
			return iamRelationError(tx, err)
		}
	} else {
		if expected == 0 {
			return biz.ErrIAMRevisionConflict
		}
		result := tx.db.Model(&iamConstraintModel{}).Where("context_key = ? AND id = ? AND revision = ?", c.Context.Key, c.ID, expected).Updates(map[string]any{"type": p.Type, "name": p.Name, "max_count": p.MaxCount, "enabled": p.Enabled, "revision": gorm.Expr("revision + 1")})
		if result.Error != nil {
			return iamRelationError(tx, result.Error)
		}
		if result.RowsAffected != 1 {
			return biz.ErrIAMRevisionConflict
		}
		if err := tx.db.Where("context_key = ? AND constraint_id = ?", c.Context.Key, c.ID).Delete(&iamConstraintMemberModel{}).Error; err != nil {
			return iamStorageError(tx, err)
		}
	}
	ids := slices.Clone(c.RoleIDs)
	slices.Sort(ids)
	for _, id := range ids {
		if err := tx.db.Create(&iamConstraintMemberModel{ContextKey: c.Context.Key, ConstraintID: p.ID, RoleID: id}).Error; err != nil {
			return iamRelationError(tx, err)
		}
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) DeleteConstraint(ctx context.Context, handle biz.IAMTx, c authorization.Context, id int64, expected uint64) error {
	if c.RequirePlatform() != nil {
		return biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	var p iamConstraintModel
	if err := tx.db.Where("context_key = ? AND id = ?", c.Key, id).Take(&p).Error; err != nil {
		return iamStorageError(tx, err)
	}
	if expected == 0 || p.Revision != expected {
		return biz.ErrIAMRevisionConflict
	}
	if err := tx.db.Where("context_key = ? AND constraint_id = ?", c.Key, id).Delete(&iamConstraintMemberModel{}).Error; err != nil {
		return iamStorageError(tx, err)
	}
	result := tx.db.Where("context_key = ? AND id = ? AND revision = ?", c.Key, id, expected).Delete(&iamConstraintModel{})
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	if result.RowsAffected != 1 {
		return biz.ErrIAMRevisionConflict
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) SaveLimits(ctx context.Context, handle biz.IAMTx, limits biz.IAMLimits) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	result := tx.db.Model(&iamPolicyModel{}).Where("id = 1").Updates(map[string]any{"max_roles_per_user": limits.MaxRolesPerUser, "max_roles_per_session": limits.MaxRolesPerSession})
	if result.Error != nil {
		return iamRelationError(tx, result.Error)
	}
	// MySQL reports no changed rows for a no-op; existence was checked under lock.
	tx.mutated = true
	return nil
}
func (r *iamRepo) SaveActivation(ctx context.Context, handle biz.IAMTx, c authorization.Context, a biz.IAMActivationChange, expected uint64) error {
	if c.RequirePlatform() != nil {
		return biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	if expected == 0 {
		return biz.ErrIAMRevisionConflict
	}
	result := tx.db.Table("iam_session_contexts").Where("session_id = ? AND context_key = ? AND revision = ? AND revoked_at IS NULL", a.SessionID, c.Key, expected).Updates(map[string]any{"activation_state": "active", "revision": gorm.Expr("revision + 1")})
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	if result.RowsAffected != 1 {
		return biz.ErrIAMRevisionConflict
	}
	if err := tx.db.Where("session_id = ? AND context_key = ?", a.SessionID, c.Key).Delete(&iamSessionRoleModel{}).Error; err != nil {
		return iamStorageError(tx, err)
	}
	ids := slices.Clone(a.RoleIDs)
	slices.Sort(ids)
	for _, id := range ids {
		if err := tx.db.Create(&iamSessionRoleModel{SessionID: a.SessionID, ContextKey: c.Key, RoleID: id}).Error; err != nil {
			return iamRelationError(tx, err)
		}
	}
	tx.mutated = true
	return nil
}
