package data

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"gorm.io/gorm"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
)

func userScopeQuery(ctx context.Context, query *gorm.DB) (*gorm.DB, error) {
	q, ok := authorization.QueryScopeFromContext(ctx, "identity.user.export")
	if !ok {
		q, ok = authorization.QueryScopeFromContext(ctx, "identity.user.list")
	}
	if !ok {
		return query, nil
	}
	now := time.Now().Unix()
	// One data-owned relation covers default groups and currently effective
	// grants. The same predicate is used by total and page queries.

	// The candidate IDs are parameterized once via a derived relation.
	groups := "SELECT 1 FROM (SELECT id AS user_id, default_routing_group_id AS routing_group_id FROM users UNION SELECT user_id, routing_group_id FROM user_routing_group_grants WHERE status = 'active' AND starts_at <= " + strconv.FormatInt(now, 10) + " AND (expires_at = 0 OR expires_at > " + strconv.FormatInt(now, 10) + ")) ag WHERE ag.user_id = users.id AND ag.routing_group_id IN ?"
	return authzquery.Apply(query, q, authzquery.Columns{Resource: "users.id", User: "users.id", Groups: groups})
}

func (r *Repository) UserAuthorizationFacts(ctx context.Context, id int64, at time.Time) (authorization.ObjectFacts, error) {
	out := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id, OwnerUserID: id}
	if r.db == nil {
		return out, biz.ErrIAMDependencyUnavailable
	}
	return userAuthorizationFacts(r.db.WithContext(ctx), id, at)
}

func userAuthorizationFacts(db *gorm.DB, id int64, at time.Time) (authorization.ObjectFacts, error) {
	out := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id, OwnerUserID: id}
	var u userModel
	if err := db.First(&u, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return out, biz.ErrUserNotFound
		}
		return out, err
	}
	if u.DefaultRoutingGroupID > 0 {
		out.RoutingGroupIDs = append(out.RoutingGroupIDs, u.DefaultRoutingGroupID)
	}
	var grants []routingGrantModel
	if err := db.Where("user_id = ? AND status = 'active' AND starts_at <= ? AND (expires_at = 0 OR expires_at > ?)", id, at.Unix(), at.Unix()).Find(&grants).Error; err != nil {
		return out, err
	}
	for _, g := range grants {
		if !slices.Contains(out.RoutingGroupIDs, g.RoutingGroupID) {
			out.RoutingGroupIDs = append(out.RoutingGroupIDs, g.RoutingGroupID)
		}
	}
	return out, nil
}

func (r *iamRepo) UserAuthorizationFactsTx(ctx context.Context, handle biz.IAMTx, id int64, at time.Time) (authorization.ObjectFacts, error) {
	tx, err := iamDB(ctx, r.data, handle, false)
	if err != nil {
		return authorization.ObjectFacts{}, err
	}
	return userAuthorizationFacts(tx.db, id, at)
}

func (r *Repository) ExportUsers(ctx context.Context, page, size int32, keyword, group string, status int32) ([]*biz.User, int64, error) {
	if r.db == nil {
		return nil, 0, biz.ErrIAMDependencyUnavailable
	}
	if _, ok := authorization.QueryScopeFromContext(ctx, "identity.user.export"); !ok && authorization.External(ctx) {
		return nil, 0, authorization.ErrDenied
	}
	return r.listUsersDB(ctx, page, size, keyword, group, status)
}
