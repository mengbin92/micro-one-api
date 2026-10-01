package data

import (
	"context"
	"errors"
	"slices"
	"time"

	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
)

func NewIAMRuntimeRepo(d *Data) biz.IAMRuntimeRepo { return &iamRepo{data: d} }

// HasPersistentStorage distinguishes explicit development memory mode from a
// database dependency failure. Persistent mode never falls back to memory IAM.
func (r *Repository) HasPersistentStorage() bool { return r.db != nil }

type iamSessionModel struct {
	SessionID         string `gorm:"primaryKey"`
	UserID, ExpiresAt int64
	RevokedAt         *int64
	Revision          uint64
}

func (iamSessionModel) TableName() string { return "iam_sessions" }

type iamSessionContextModel struct {
	SessionID       string `gorm:"primaryKey"`
	ContextKey      string `gorm:"primaryKey"`
	ActivationState string
	RevokedAt       *int64
	Revision        uint64
}

func (iamSessionContextModel) TableName() string { return "iam_session_contexts" }

func (r *iamRepo) Session(ctx context.Context, handle biz.IAMTx, id string, c authorization.Context) (biz.IAMSessionContext, error) {
	if c.RequirePlatform() != nil {
		return biz.IAMSessionContext{}, biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return biz.IAMSessionContext{}, err
	}
	var p iamSessionModel
	if err = tx.db.Where("session_id = ?", id).Take(&p).Error; err != nil {
		return biz.IAMSessionContext{}, iamStorageError(tx, err)
	}
	var sc iamSessionContextModel
	if err = tx.db.Where("session_id = ? AND context_key = ?", id, c.Key).Take(&sc).Error; err != nil {
		return biz.IAMSessionContext{}, iamStorageError(tx, err)
	}
	s := biz.IAMSessionContext{SessionID: id, UserID: p.UserID, Context: c, ExpiresAt: time.UnixMilli(p.ExpiresAt).UTC(), ActivationState: sc.ActivationState, SessionRevision: p.Revision, Revision: sc.Revision}
	if p.RevokedAt != nil {
		at := time.UnixMilli(*p.RevokedAt).UTC()
		s.RevokedAt = &at
	}
	if sc.RevokedAt != nil {
		at := time.UnixMilli(*sc.RevokedAt).UTC()
		s.ContextRevokedAt = &at
	}
	var active []iamSessionRoleModel
	if err = tx.db.Where("session_id = ? AND context_key = ?", id, c.Key).Order("role_id").Find(&active).Error; err != nil {
		return s, iamStorageError(tx, err)
	}
	for _, a := range active {
		s.ActiveRoleIDs = append(s.ActiveRoleIDs, a.RoleID)
	}
	return s, nil
}
func (r *iamRepo) CreateSession(ctx context.Context, handle biz.IAMTx, a authorization.Actor, s biz.IAMSessionContext) error {
	if s.Context.RequirePlatform() != nil {
		return biz.ErrIAMContextInvalid
	}
	if a.SessionID == "" || len(a.SessionID) > 128 || a.UserID <= 0 || a.SessionID != s.SessionID || a.UserID != s.UserID || !a.ExpiresAt.Equal(s.ExpiresAt) || !slices.Contains([]string{"active", "selection_required"}, s.ActivationState) || (s.ActivationState == "selection_required" && len(s.ActiveRoleIDs) > 0) {
		return biz.ErrIAMInvalidRelation
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	// Never replace an existing JTI, especially a revoked one. The policy lock
	// serializes lazy initialization and identity verification in biz.
	var existing iamSessionModel
	err = tx.db.Where("session_id = ?", a.SessionID).Take(&existing).Error
	if err == nil {
		return biz.ErrSessionRevoked
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return iamStorageError(tx, err)
	}
	if err = tx.db.Create(&iamSessionModel{SessionID: a.SessionID, UserID: a.UserID, ExpiresAt: a.ExpiresAt.UnixMilli(), Revision: 1}).Error; err != nil {
		return iamRelationError(tx, err)
	}
	if err = tx.db.Create(&iamSessionContextModel{SessionID: a.SessionID, ContextKey: s.Context.Key, ActivationState: s.ActivationState, Revision: 1}).Error; err != nil {
		return iamRelationError(tx, err)
	}
	ids := slices.Clone(s.ActiveRoleIDs)
	slices.Sort(ids)
	for _, id := range ids {
		if err = tx.db.Create(&iamSessionRoleModel{SessionID: a.SessionID, ContextKey: s.Context.Key, RoleID: id}).Error; err != nil {
			return iamRelationError(tx, err)
		}
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) RevokeSession(ctx context.Context, handle biz.IAMTx, id string, c authorization.Context, global bool, at time.Time, expected uint64) error {
	if c.RequirePlatform() != nil {
		return biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	if expected == 0 || at.IsZero() {
		return biz.ErrIAMRevisionConflict
	}
	table, where, args := "iam_session_contexts", "session_id = ? AND context_key = ? AND revision = ? AND revoked_at IS NULL", []any{id, c.Key, expected}
	if global {
		table, where, args = "iam_sessions", "session_id = ? AND revision = ? AND revoked_at IS NULL", []any{id, expected}
	}
	result := tx.db.Table(table).Where(where, args...).Updates(map[string]any{"revoked_at": at.UnixMilli(), "revision": gorm.Expr("revision + 1")})
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	if result.RowsAffected != 1 {
		return biz.ErrIAMRevisionConflict
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) RevokeUserSessions(ctx context.Context, handle biz.IAMTx, id int64, at time.Time) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	result := tx.db.Table("iam_sessions").Where("user_id = ? AND revoked_at IS NULL", id).Updates(map[string]any{"revoked_at": at.UnixMilli(), "revision": gorm.Expr("revision + 1")})
	if result.Error != nil {
		return iamStorageError(tx, result.Error)
	}
	if result.RowsAffected > 0 {
		tx.mutated = true
	}
	return nil
}

func (r *iamRepo) UpdateAccount(ctx context.Context, handle biz.IAMTx, u biz.User, fields []string) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	updates := map[string]any{}
	for _, field := range fields {
		switch field {
		case "username":
			updates["username"] = u.Username
		case "display_name":
			updates["display_name"] = u.DisplayName
		case "email":
			updates["email"] = u.Email
		case "group":
			updates["group"] = u.Group
		case "status":
			updates["status"] = u.Status
		case "role":
			updates["role"] = u.Role
		case "password":
			updates["password_hash"] = u.PasswordHash
			updates["password_changed_at"] = u.PasswordChangedAt
		case "password_epoch":
			updates["password_changed_at"] = u.PasswordChangedAt
		case "aff_code":
			updates["aff_code"] = u.AffCode
		default:
			return biz.ErrIAMInvalidRelation
		}
	}
	if u.ID <= 0 || len(updates) == 0 {
		return biz.ErrIAMInvalidRelation
	}
	if _, ok := updates["group"]; ok && biz.RoutingV2Enabled() {
		bound := NewRepository(&Data{db: tx.db, redis: r.data.redis})
		if err = bound.updateRoutingUserDB(ctx, &u, updates); err != nil {
			return iamRelationError(tx, err)
		}
	} else if err = tx.db.Table("users").Where("id = ?", u.ID).Updates(updates).Error; err != nil {
		return iamRelationError(tx, err)
	}
	tx.mutated = true
	return nil
}
func (r *iamRepo) DeleteAccount(ctx context.Context, handle biz.IAMTx, id int64) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	// Delete dependents in FK order. Audits deliberately have no user FK and
	// remain immutable. Governance references fail closed rather than cascading.
	for _, table := range []string{"iam_session_roles", "iam_session_contexts"} {
		if err = tx.db.Table(table).Where("session_id IN (?)", tx.db.Table("iam_sessions").Select("session_id").Where("user_id = ?", id)).Delete(map[string]any{}).Error; err != nil {
			return iamRelationError(tx, err)
		}
	}
	for _, table := range []string{"iam_sessions", "iam_user_roles", "user_oauth_identities", "user_routing_group_grants", "tokens"} {
		if err = tx.db.Table(table).Where("user_id = ?", id).Delete(map[string]any{}).Error; err != nil {
			return iamRelationError(tx, err)
		}
	}
	if err = tx.db.Table("users").Where("id = ?", id).Delete(map[string]any{}).Error; err != nil {
		return iamRelationError(tx, err)
	}
	tx.mutated = true
	return nil
}

func (r *iamRepo) FindOAuthIdentityTx(ctx context.Context, handle biz.IAMTx, provider, id string) (*biz.OAuthIdentity, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return nil, err
	}
	var p oauthIdentityModel
	err = tx.db.Where("provider = ? AND provider_id = ?", provider, id).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrOAuthUserNotFound
	}
	if err != nil {
		return nil, iamStorageError(tx, err)
	}
	return oauthIdentityModelToBiz(p), nil
}
func (r *iamRepo) FindOAuthIdentityByUserTx(ctx context.Context, handle biz.IAMTx, user int64, provider string) (*biz.OAuthIdentity, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return nil, err
	}
	var p oauthIdentityModel
	err = tx.db.Where("user_id = ? AND provider = ?", user, provider).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, biz.ErrOAuthUserNotFound
	}
	if err != nil {
		return nil, iamStorageError(tx, err)
	}
	return oauthIdentityModelToBiz(p), nil
}

func (r *iamRepo) UpdateRoutingAccessTx(ctx context.Context, handle biz.IAMTx, c biz.RoutingAccessChange) error {
	tx, err := iamDB(ctx, r.data, handle, true)
	if err != nil {
		return err
	}
	if err = applyRoutingAccess(tx.db, c); err != nil {
		return iamRelationError(tx, err)
	}
	tx.mutated = true
	return nil
}
