package data

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
)

// Scope predicates are owned by the channel data layer. The verified query
// scope arrives through context from the service boundary; without it the
// repository keeps legacy behavior so in-process and system callers are
// unaffected. Count, pagination and detail reads share the same predicate.

func (r *Repository) channelScope(ctx context.Context, query *gorm.DB, table string) (*gorm.DB, error) {
	return authzquery.ApplyContext(ctx, query, authzquery.Columns{Resource: table + ".id", Groups: r.groupsSubselect("channel_routing_groups", "channel_id", table)}, "channel.channel.list", "channel.channel.export")
}

func (r *Repository) subscriptionAccountScope(ctx context.Context, query *gorm.DB, table string) (*gorm.DB, error) {
	q, ok := authorization.QueryScopeFromContext(ctx, "channel.account.list")
	if !ok {
		return query, nil
	}
	return authzquery.Apply(query, q, authzquery.Columns{Resource: table + ".id", Groups: r.groupsSubselect("account_routing_groups", "subscription_account_id", table)})
}

// groupsSubselect resolves the candidate routing group IDs against the
// relation table, or against the legacy CSV group column through the
// routing_groups key when relations are not enabled. Both shapes keep the
// candidate IDs as the single bound parameter required by authzquery.
func (r *Repository) groupsSubselect(relation, column, table string) string {
	if r.routingGroupRelations {
		return "SELECT 1 FROM " + relation + " rel WHERE rel." + column + " = " + table + ".id AND rel.routing_group_id IN ?"
	}
	return "SELECT 1 FROM routing_groups rg WHERE rg.id IN ? AND " + r.legacyGroupMembershipSQL(table)
}

// ChannelAuthorizationFacts reads the authoritative object facts for one
// channel. Routing group IDs come from the relation table, or from resolving
// the legacy CSV keys, so a decision never trusts caller-supplied groups.
func (r *Repository) ChannelAuthorizationFacts(ctx context.Context, channelID int64) (authorization.ObjectFacts, error) {
	out := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: channelID}
	if r.db == nil {
		return out, biz.ErrChannelNotFound
	}
	var model channelModel
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, channelID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return out, biz.ErrChannelNotFound
		}
		return out, err
	}
	ids, err := r.routingGroupIDs(ctx, "channel_routing_groups", "channel_id", channelID, model.Group)
	if err != nil {
		return out, err
	}
	out.RoutingGroupIDs = ids
	return out, nil
}

// SubscriptionAccountAuthorizationFacts mirrors ChannelAuthorizationFacts for
// subscription accounts.
func (r *Repository) SubscriptionAccountAuthorizationFacts(ctx context.Context, accountID int64) (authorization.ObjectFacts, error) {
	out := authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: accountID}
	if r.db == nil {
		return out, biz.ErrSubscriptionAccountNotFound
	}
	var model subscriptionAccountModel
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).First(&model, accountID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return out, biz.ErrSubscriptionAccountNotFound
		}
		return out, err
	}
	ids, err := r.routingGroupIDs(ctx, "account_routing_groups", "subscription_account_id", accountID, model.Group)
	if err != nil {
		return out, err
	}
	out.RoutingGroupIDs = ids
	return out, nil
}

func (r *Repository) routingGroupIDs(ctx context.Context, relation, column string, id int64, csv string) ([]int64, error) {
	if r.routingGroupRelations {
		var ids []int64
		if err := r.db.WithContext(ctx).Table(relation).Select("routing_group_id").Where(column+" = ?", id).Scan(&ids).Error; err != nil {
			return nil, err
		}
		return ids, nil
	}
	keys := splitGroupCSV(csv)
	if len(keys) == 0 {
		return nil, nil
	}
	return r.RoutingGroupAuthorizationIDs(ctx, csv)
}

func splitGroupCSV(csv string) []string {
	out := []string{}
	for _, part := range strings.Split(csv, ",") {
		if key := strings.TrimSpace(part); key != "" {
			out = append(out, key)
		}
	}
	return out
}

func (r *Repository) RoutingGroupAuthorizationIDs(ctx context.Context, csv string) ([]int64, error) {
	keys := splitGroupCSV(csv)
	if r.db == nil {
		return nil, authorization.ErrDenied
	}
	var groups []routingGroupModel
	if err := r.db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where(map[string]any{"key": keys}).Find(&groups).Error; err != nil {
		return nil, err
	}
	found := map[string]int64{}
	for _, g := range groups {
		found[g.Key] = g.ID
	}
	ids := []int64{}
	for _, key := range keys {
		id, ok := found[key]
		if !ok {
			return nil, authorization.ErrDenied
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// checkResourceTx rereads membership and sensitive columns through the same
// transaction that will write. Changing ownership checks both old and new
// facts; a concurrent membership change cannot reuse a preflight decision.
func (r *Repository) checkResourceTx(ctx context.Context, tx *gorm.DB, id int64, nextGroup string, account bool, operations ...string) error {
	active := false
	for _, op := range operations {
		if _, ok := authorization.QueryScopeFromContext(ctx, op); ok {
			active = true
		}
	}
	if !active {
		return nil
	}
	owner := &Repository{db: tx, routingGroupRelations: r.routingGroupRelations, encKey: r.encKey}
	var facts authorization.ObjectFacts
	var err error
	if id > 0 {
		if account {
			facts, err = owner.SubscriptionAccountAuthorizationFacts(ctx, id)
		} else {
			facts, err = owner.ChannelAuthorizationFacts(ctx, id)
		}
		if err != nil {
			return err
		}
	} else {
		facts.Context = authorization.Platform()
	}
	if id > 0 || nextGroup == "" {
		for _, op := range operations {
			if err := authorization.Require(ctx, op, facts); err != nil {
				return err
			}
		}
	}
	oldGroups := append([]int64{}, facts.RoutingGroupIDs...)
	nextGroups := append([]int64{}, oldGroups...)
	if nextGroup != "" {
		ids, err := owner.RoutingGroupAuthorizationIDs(ctx, nextGroup)
		if err != nil {
			return err
		}
		facts.RoutingGroupIDs = ids
		nextGroups = ids
		for _, op := range operations {
			if err := authorization.Require(ctx, op, facts); err != nil {
				return err
			}
		}
	}
	membershipWrite := false
	for _, op := range operations {
		if op == "channel.channel.delete" || op == "channel.account.delete" {
			nextGroups = nil
			membershipWrite = true
		}
		if slices.Contains([]string{"channel.channel.create", "channel.channel.update", "channel.account.create", "channel.account.update"}, op) {
			membershipWrite = true
		}
	}
	if membershipWrite {
		oldSet, nextSet := append([]int64{}, oldGroups...), append([]int64{}, nextGroups...)
		slices.Sort(oldSet)
		slices.Sort(nextSet)
		if !slices.Equal(oldSet, nextSet) {
			for _, groupID := range append(oldGroups, nextGroups...) {
				if err := authorization.Require(ctx, "channel.routing_group.members.update", authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: groupID}); err != nil {
					return err
				}
			}
		}
	}
	if id > 0 {
		for _, op := range operations {
			if err := authzquery.AppendWriteAudit(ctx, tx, op, id); err != nil {
				return err
			}
		}
	}
	if err := checkSourceWriteRevision(ctx, tx, id, account, operations); err != nil {
		return fmt.Errorf("check source revision for %v id=%d: %w", operations, id, err)
	}
	return nil
}
