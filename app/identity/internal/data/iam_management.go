package data

import (
	"context"
	"fmt"
	"strings"
	"time"

	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	m "micro-one-api/domain/authorization/management"
	"micro-one-api/pkg/jsonx"
)

func NewIAMManagementRepo(d *Data) biz.IAMManagementRepo { return &iamRepo{data: d} }

type iamDelegationModel struct {
	ID, ManagerRoleID                      int64
	ContextKey, TargetKind                 string
	TargetRoleID                           *int64
	Actions, TargetUserScope, GrantCeiling string
	CanRedelegate                          int32
	StartsAt                               int64
	ExpiresAt                              *int64
	Revision                               uint64
}

func (iamDelegationModel) TableName() string { return "iam_delegations" }
func newIAMDelegation(d m.Delegation) iamDelegationModel {
	actions, _ := jsonx.Marshal(d.Actions)
	scope, _ := jsonx.Marshal(d.TargetUserScope)
	ceiling, _ := jsonx.Marshal(d.GrantCeiling)
	p := iamDelegationModel{ID: d.ID, ManagerRoleID: d.ManagerRoleID, ContextKey: d.Context.Key, TargetKind: d.TargetKind, Actions: string(actions), TargetUserScope: string(scope), GrantCeiling: string(ceiling), CanRedelegate: boolInt(d.CanRedelegate), StartsAt: d.Validity.StartsAt.UnixMilli(), Revision: d.Revision}
	if d.TargetRoleID > 0 {
		p.TargetRoleID = &d.TargetRoleID
	}
	if d.Validity.ExpiresAt != nil {
		end := d.Validity.ExpiresAt.UnixMilli()
		p.ExpiresAt = &end
	}
	return p
}
func (p iamDelegationModel) toBiz() (m.Delegation, error) {
	d := m.Delegation{ID: p.ID, Context: authorization.Platform(), ManagerRoleID: p.ManagerRoleID, TargetKind: p.TargetKind, CanRedelegate: p.CanRedelegate != 0, Revision: p.Revision, Validity: authorization.Interval{StartsAt: time.UnixMilli(p.StartsAt).UTC()}}
	if p.ContextKey != "platform" {
		return d, biz.ErrIAMContextInvalid
	}
	if p.TargetRoleID != nil {
		d.TargetRoleID = *p.TargetRoleID
	}
	if p.ExpiresAt != nil {
		end := time.UnixMilli(*p.ExpiresAt).UTC()
		d.Validity.ExpiresAt = &end
	}
	if jsonx.Unmarshal([]byte(p.Actions), &d.Actions) != nil || jsonx.Unmarshal([]byte(p.TargetUserScope), &d.TargetUserScope) != nil || jsonx.Unmarshal([]byte(p.GrantCeiling), &d.GrantCeiling) != nil || d.Validity.Validate() != nil {
		return d, biz.ErrIAMInvalidRelation
	}
	return d, nil
}
func (r *iamRepo) Delegations(ctx context.Context, h biz.IAMTx, c authorization.Context) ([]m.Delegation, error) {
	if c.RequirePlatform() != nil {
		return nil, biz.ErrIAMContextInvalid
	}
	tx, err := iamDB(ctx, r.data, h, false)
	if err != nil {
		return nil, err
	}
	var rows []iamDelegationModel
	if err = tx.db.Where("context_key = ?", c.Key).Order("id").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := []m.Delegation{}
	for _, p := range rows {
		d, e := p.toBiz()
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (r *iamRepo) SaveDelegation(ctx context.Context, h biz.IAMTx, d m.Delegation, expected uint64) (m.Delegation, error) {
	tx, err := iamDB(ctx, r.data, h, true)
	if err != nil {
		return d, err
	}
	p := newIAMDelegation(d)
	if d.ID == 0 {
		if expected != 0 {
			return d, biz.ErrIAMRevisionConflict
		}
		p.Revision = 1
		err = tx.db.Create(&p).Error
	} else {
		var old iamDelegationModel
		if err = tx.db.First(&old, d.ID).Error; err != nil {
			return d, iamStorageError(tx, err)
		}
		if old.ContextKey != p.ContextKey || old.ManagerRoleID != p.ManagerRoleID || old.TargetKind != p.TargetKind || !equalOptionalID(old.TargetRoleID, p.TargetRoleID) {
			return d, biz.ErrIAMInvalidRelation
		}
		if expected == 0 || old.Revision != expected {
			return d, biz.ErrIAMRevisionConflict
		}
		p.Revision = expected + 1
		result := tx.db.Model(&iamDelegationModel{}).Where("id = ? AND revision = ?", d.ID, expected).Updates(map[string]any{"actions": p.Actions, "target_user_scope": p.TargetUserScope, "grant_ceiling": p.GrantCeiling, "can_redelegate": p.CanRedelegate, "starts_at": p.StartsAt, "expires_at": p.ExpiresAt, "revision": p.Revision})
		err = result.Error
		if err == nil && result.RowsAffected != 1 {
			return d, biz.ErrIAMRevisionConflict
		}
	}
	if err != nil {
		return d, iamRelationError(tx, err)
	}
	tx.mutated = true
	return p.toBiz()
}
func equalOptionalID(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

type iamPermissionModel struct {
	ID, ResourceID                                                                                        int64
	Code, Action, Name, Category, Status, RiskLevel, SupportedScopes, SupportedContextTypes, BindingState string
	Protected                                                                                             int32
	Revision                                                                                              uint64
	Description                                                                                           string
	Sort                                                                                                  int32
}

func (iamPermissionModel) TableName() string { return "iam_permissions" }
func (p iamPermissionModel) toBiz() m.Permission {
	d := m.Permission{ID: p.ID, ResourceID: p.ResourceID, Code: p.Code, Name: p.Name, Description: p.Description, Sort: p.Sort, Category: p.Category, RiskLevel: p.RiskLevel, Status: p.Status, Binding: "unbound", Revision: p.Revision}
	if op, ok := authorization.Lookup(p.Code); ok {
		d.SupportedScopes = op.Scopes
		d.ContextTypes = op.ContextTypes
		d.Protected = op.Protected
		if biz.IAMExecutionBound(p.Code) {
			d.Binding = "bound"
		}
	}
	return d
}

func (r *iamRepo) Permissions(ctx context.Context, h biz.IAMTx) ([]m.Permission, error) {
	tx, err := iamDB(ctx, r.data, h, false)
	if err != nil {
		return nil, err
	}
	var rows []iamPermissionModel
	if err = tx.db.Order("id").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := []m.Permission{}
	for _, p := range rows {
		out = append(out, p.toBiz())
	}
	return out, nil
}
func (r *iamRepo) SavePermission(ctx context.Context, h biz.IAMTx, d m.Permission, expected uint64) (m.Permission, error) {
	tx, err := iamDB(ctx, r.data, h, true)
	if err != nil {
		return d, err
	}
	op, ok := authorization.Lookup(d.Code)
	if !ok {
		if d.Status == "enabled" {
			return d, biz.ErrIAMInvalidRelation
		}
		var resource struct{ Code string }
		if err = tx.db.Table("iam_resources").Select("code").Where("id = ?", d.ResourceID).Take(&resource).Error; err != nil {
			return d, iamStorageError(tx, err)
		}
		if !strings.HasPrefix(d.Code, resource.Code+".") || len(d.Code) > 160 {
			return d, biz.ErrIAMInvalidRelation
		}
		op = authorization.Operation{Code: d.Code, Resource: resource.Code, Action: strings.TrimPrefix(d.Code, resource.Code+"."), Scopes: []authorization.ScopeKind{}, ContextTypes: []string{"platform"}}
	}
	var p iamPermissionModel
	if d.ID == 0 {
		if expected != 0 || d.Status != "draft" {
			return d, biz.ErrIAMInvalidRelation
		}
		scopes, _ := jsonx.Marshal(op.Scopes)
		contexts, _ := jsonx.Marshal(op.ContextTypes)
		p = iamPermissionModel{ResourceID: d.ResourceID, Code: d.Code, Action: op.Action, Name: d.Name, Description: d.Description, Sort: d.Sort, Category: d.Category, RiskLevel: d.RiskLevel, Status: "draft", BindingState: "unbound", Protected: boolInt(op.Protected), SupportedScopes: string(scopes), SupportedContextTypes: string(contexts), Revision: 1}
		err = tx.db.Create(&p).Error
	} else {
		if err = tx.db.First(&p, d.ID).Error; err != nil {
			return d, iamStorageError(tx, err)
		}
		if p.Code != d.Code || p.ResourceID != d.ResourceID {
			return d, biz.ErrIAMInvalidRelation
		}
		if expected == 0 || p.Revision != expected {
			return d, biz.ErrIAMRevisionConflict
		}
		result := tx.db.Model(&iamPermissionModel{}).Where("id = ? AND revision = ?", d.ID, expected).Updates(map[string]any{"name": d.Name, "description": d.Description, "sort": d.Sort, "category": d.Category, "risk_level": d.RiskLevel, "status": d.Status, "revision": expected + 1})
		err = result.Error
		if err == nil && result.RowsAffected != 1 {
			return d, biz.ErrIAMRevisionConflict
		}
		p.Name, p.Category, p.RiskLevel, p.Status, p.Revision = d.Name, d.Category, d.RiskLevel, d.Status, expected+1
		p.Description, p.Sort = d.Description, d.Sort
	}
	if err != nil {
		return d, iamRelationError(tx, err)
	}
	tx.mutated = true
	return p.toBiz(), nil
}
func (r *iamRepo) Resources(ctx context.Context, h biz.IAMTx) ([]m.Resource, error) {
	tx, err := iamDB(ctx, r.data, h, false)
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID                       int64
		Code, Name, OwnerService string
		Enabled                  int32
	}
	if err = tx.db.Table("iam_resources").Order("id").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := []m.Resource{}
	for _, p := range rows {
		out = append(out, m.Resource{ID: p.ID, Code: p.Code, Name: p.Name, Owner: p.OwnerService, Enabled: p.Enabled != 0})
	}
	return out, nil
}

type iamRolePermissionModel struct {
	ID                      int64
	ContextKey              string
	RoleID, PermissionID    int64
	Effect, ScopeDescriptor string
	Revision                uint64
}

func (iamRolePermissionModel) TableName() string { return "iam_role_permissions" }
func (r *iamRepo) SaveManagedRole(ctx context.Context, h biz.IAMTx, d biz.IAMRole, expected uint64) (biz.IAMRole, error) {
	tx, err := iamDB(ctx, r.data, h, true)
	if err != nil {
		return d, err
	}
	if d.Context.RequirePlatform() != nil || d.Builtin || d.Code == "root" {
		return d, biz.ErrIAMProtected
	}
	p := newIAMRole(d)
	if d.ID == 0 {
		if expected != 0 {
			return d, biz.ErrIAMRevisionConflict
		}
		p.Revision = 1
		err = tx.db.Create(&p).Error
	} else {
		var old iamRoleModel
		if err = tx.db.First(&old, d.ID).Error; err != nil {
			return d, iamStorageError(tx, err)
		}
		if old.Builtin != 0 || old.Code == "root" {
			return d, biz.ErrIAMProtected
		}
		if old.ContextKey != p.ContextKey || old.Code != p.Code || !equalOptionalID(old.CreationDelegationID, p.CreationDelegationID) {
			return d, biz.ErrIAMInvalidRelation
		}
		if expected == 0 || old.Revision != expected {
			return d, biz.ErrIAMRevisionConflict
		}
		p.Revision = expected + 1
		result := tx.db.Model(&iamRoleModel{}).Where("id = ? AND revision = ?", p.ID, expected).Updates(map[string]any{"name": p.Name, "description": p.Description, "status": p.Status, "max_members": p.MaxMembers, "revision": p.Revision})
		err = result.Error
		if err == nil && result.RowsAffected != 1 {
			return d, biz.ErrIAMRevisionConflict
		}
	}
	if err != nil {
		return d, iamRelationError(tx, err)
	}
	if err = tx.db.Where("context_key = ? AND role_id = ?", d.Context.Key, p.ID).Delete(&iamRolePermissionModel{}).Error; err != nil {
		return d, iamStorageError(tx, err)
	}
	for _, g := range d.Grants {
		var permission iamPermissionModel
		if err = tx.db.Where("code = ?", g.Operation).Take(&permission).Error; err != nil {
			return d, iamStorageError(tx, err)
		}
		b, e := jsonx.Marshal(g.Scope)
		if e != nil {
			return d, biz.ErrIAMScopeInvalid
		}
		grant := iamRolePermissionModel{ContextKey: d.Context.Key, RoleID: p.ID, PermissionID: permission.ID, Effect: string(g.Effect), ScopeDescriptor: string(b), Revision: p.Revision}
		if err = tx.db.Create(&grant).Error; err != nil {
			return d, iamRelationError(tx, err)
		}
	}
	if err = tx.db.Where("context_key = ? AND senior_role_id = ?", d.Context.Key, p.ID).Delete(&iamInheritanceModel{}).Error; err != nil {
		return d, iamStorageError(tx, err)
	}
	for _, id := range d.Inherits {
		edge := iamInheritanceModel{ContextKey: d.Context.Key, SeniorRoleID: p.ID, JuniorRoleID: id}
		if err = tx.db.Create(&edge).Error; err != nil {
			return d, iamRelationError(tx, err)
		}
	}
	tx.mutated = true
	d.ID, d.Revision = p.ID, p.Revision
	return d, nil
}

type iamMenuModel struct {
	ID                       int64
	ParentID                 *int64
	RouteKey, Name, IconKey  string
	Sort                     int32
	Enabled                  int32
	RequiredAll, RequiredAny string
	Revision                 uint64
}

func (iamMenuModel) TableName() string { return "iam_menu_items" }
func (p iamMenuModel) toBiz() (m.Menu, error) {
	d := m.Menu{ID: p.ID, RouteKey: p.RouteKey, Name: p.Name, IconKey: p.IconKey, Sort: p.Sort, Enabled: p.Enabled != 0, Revision: p.Revision}
	if p.ParentID != nil {
		d.ParentID = *p.ParentID
	}
	if jsonx.Unmarshal([]byte(p.RequiredAll), &d.RequiredAll) != nil || jsonx.Unmarshal([]byte(p.RequiredAny), &d.RequiredAny) != nil {
		return d, biz.ErrIAMInvalidRelation
	}
	return d, nil
}
func (r *iamRepo) Menus(ctx context.Context, h biz.IAMTx) ([]m.Menu, error) {
	tx, err := iamDB(ctx, r.data, h, false)
	if err != nil {
		return nil, err
	}
	var rows []iamMenuModel
	if err = tx.db.Order("sort, id").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := []m.Menu{}
	for _, p := range rows {
		d, e := p.toBiz()
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func (r *iamRepo) SaveMenu(ctx context.Context, h biz.IAMTx, d m.Menu, expected uint64) (m.Menu, error) {
	tx, err := iamDB(ctx, r.data, h, true)
	if err != nil {
		return d, err
	}
	a, _ := jsonx.Marshal(d.RequiredAll)
	b, _ := jsonx.Marshal(d.RequiredAny)
	p := iamMenuModel{ID: d.ID, RouteKey: d.RouteKey, Name: d.Name, IconKey: d.IconKey, Sort: d.Sort, Enabled: boolInt(d.Enabled), RequiredAll: string(a), RequiredAny: string(b)}
	if d.ParentID > 0 {
		p.ParentID = &d.ParentID
	}
	if d.ID == 0 {
		if expected != 0 {
			return d, biz.ErrIAMRevisionConflict
		}
		p.Revision = 1
		err = tx.db.Create(&p).Error
	} else {
		p.Revision = expected + 1
		result := tx.db.Model(&iamMenuModel{}).Where("id = ? AND revision = ?", d.ID, expected).Updates(map[string]any{"parent_id": p.ParentID, "route_key": p.RouteKey, "name": p.Name, "icon_key": p.IconKey, "sort": p.Sort, "enabled": p.Enabled, "required_all": p.RequiredAll, "required_any": p.RequiredAny, "revision": p.Revision})
		err = result.Error
		if err == nil && (expected == 0 || result.RowsAffected != 1) {
			return d, biz.ErrIAMRevisionConflict
		}
	}
	if err != nil {
		return d, iamRelationError(tx, err)
	}
	tx.mutated = true
	return p.toBiz()
}
func (r *iamRepo) ManagedAuditEvents(ctx context.Context, h biz.IAMTx, roles []int64, all bool) ([]biz.IAMAuditEvent, error) {
	tx, err := iamDB(ctx, r.data, h, false)
	if err != nil {
		return nil, err
	}
	q := tx.db.Where("target_context_key = ?", "platform")
	// Scope at the owner query, including totals/exports. Role governance events
	// use a stable role:<id> target; generic batch/account audits are root-only.
	if !all {
		targets := []string{}
		for _, id := range roles {
			targets = append(targets, "role:"+fmtIAMID(id))
		}
		q = q.Where("target IN ?", targets)
	}
	var rows []iamAuditModel
	if err = q.Order("occurred_at DESC, event_id DESC").Find(&rows).Error; err != nil {
		return nil, iamStorageError(tx, err)
	}
	out := []biz.IAMAuditEvent{}
	for _, p := range rows {
		d, e := p.toBiz()
		if e != nil {
			return nil, e
		}
		out = append(out, d)
	}
	return out, nil
}
func fmtIAMID(id int64) string { return fmt.Sprint(id) }
