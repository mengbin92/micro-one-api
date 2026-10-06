package data

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/notify/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
	"micro-one-api/platform/database/xdb"
	"time"
)

type notificationRuleModel struct {
	ID        int64    `gorm:"column:id;primaryKey;autoIncrement:false"`
	Name      string   `gorm:"column:name"`
	Event     string   `gorm:"column:event"`
	Type      string   `gorm:"column:type"`
	Recipient string   `gorm:"column:recipient"`
	Enabled   xdb.Flag `gorm:"column:enabled"`
	Revision  uint64   `gorm:"column:revision"`
}

func (notificationRuleModel) TableName() string { return "notification_rules" }
func newRule(r *biz.NotificationRule) notificationRuleModel {
	return notificationRuleModel{ID: r.ID, Name: r.Name, Event: r.Event, Type: r.Type, Recipient: r.Recipient, Enabled: xdb.Flag(xdb.BoolInt(r.Enabled)), Revision: r.Revision}
}
func ruleToBiz(m notificationRuleModel) *biz.NotificationRule {
	return &biz.NotificationRule{ID: m.ID, Name: m.Name, Event: m.Event, Type: m.Type, Recipient: m.Recipient, Enabled: m.Enabled != 0, Revision: m.Revision}
}
func requireNotification(ctx context.Context, op string, id int64) error {
	return authorization.Require(ctx, op, authorization.ObjectFacts{Context: authorization.Platform(), ResourceID: id})
}
func (r *Repository) Acknowledge(ctx context.Context, id int64, revision uint64) (*biz.Notification, error) {
	if r.db == nil {
		if err := authorization.RequireDurableWrite(ctx, "notify.notification.acknowledge"); err != nil {
			return nil, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		n := r.mem[id]
		if n == nil {
			return nil, biz.ErrNotificationNotFound
		}
		if err := requireNotification(ctx, "notify.notification.acknowledge", id); err != nil {
			return nil, err
		}
		if n.Revision != revision {
			return nil, biz.ErrNotificationConflict
		}
		q, _ := authorization.QueryScopeFromContext(ctx, "notify.notification.acknowledge")
		n.AcknowledgedAt = time.Now()
		n.AcknowledgedBy = q.ActorID
		n.Revision++
		copy := *n
		return &copy, nil
	}
	var out *biz.Notification
	err := authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var m notificationModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&m, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrNotificationNotFound
			}
			return err
		}
		if err := requireNotification(ctx, "notify.notification.acknowledge", m.ID); err != nil {
			return err
		}
		if m.Revision != revision {
			return biz.ErrNotificationConflict
		}
		q, _ := authorization.QueryScopeFromContext(ctx, "notify.notification.acknowledge")
		m.AcknowledgedAt = time.Now().Unix()
		m.AcknowledgedBy = q.ActorID
		m.Revision++
		res := tx.Model(&notificationModel{}).Where("id = ? AND revision = ?", id, revision).Updates(map[string]any{"acknowledged_at": m.AcknowledgedAt, "acknowledged_by": m.AcknowledgedBy, "revision": m.Revision})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return biz.ErrNotificationConflict
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "notify.notification.acknowledge", id); err != nil {
			return err
		}
		out = notificationFromModel(m)
		return nil
	})
	return out, authzquery.RecordWriteFailure(ctx, r.db, "notify.notification.acknowledge", id, err)
}
func (r *Repository) ListRules(ctx context.Context) ([]*biz.NotificationRule, error) {
	out := []*biz.NotificationRule{}
	if r.db == nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, v := range r.rules {
			if requireNotification(ctx, "notify.notification.rules.update", v.ID) == nil {
				copy := *v
				out = append(out, &copy)
			}
		}
		return out, nil
	}
	query, err := authzquery.ApplyContext(ctx, r.db.WithContext(ctx).Model(&notificationRuleModel{}), authzquery.Columns{Resource: "id"}, "notify.notification.rules.update")
	if err != nil {
		return nil, err
	}
	var rows []notificationRuleModel
	if err := query.Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, m := range rows {
		out = append(out, ruleToBiz(m))
	}
	return out, nil
}
func (r *Repository) SaveRule(ctx context.Context, rule *biz.NotificationRule) error {
	if r.db == nil {
		if err := authorization.RequireDurableWrite(ctx, "notify.notification.rules.update"); err != nil {
			return err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.rules == nil {
			r.rules = map[int64]*biz.NotificationRule{}
		}
		old := r.rules[rule.ID]
		factsID := rule.ID
		if old == nil {
			factsID = 0
		}
		if err := requireNotification(ctx, "notify.notification.rules.update", factsID); err != nil {
			return err
		}
		if old == nil && rule.Revision != 0 || old != nil && old.Revision != rule.Revision {
			return biz.ErrNotificationConflict
		}
		copy := *rule
		copy.Revision++
		r.rules[rule.ID] = &copy
		rule.Revision = copy.Revision
		return nil
	}
	originalRevision := rule.Revision
	var nextRevision uint64
	err := authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var current notificationRuleModel
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, rule.ID).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if originalRevision != 0 {
				return biz.ErrNotificationConflict
			}
			if err := requireNotification(ctx, "notify.notification.rules.update", 0); err != nil {
				return err
			}
			m := newRule(rule)
			m.Revision = 1
			if err := tx.Create(&m).Error; err != nil {
				return err
			}
			nextRevision = 1
		} else {
			if err != nil {
				return err
			}
			if err := requireNotification(ctx, "notify.notification.rules.update", current.ID); err != nil {
				return err
			}
			if current.Revision != originalRevision {
				return biz.ErrNotificationConflict
			}
			m := newRule(rule)
			m.Revision = current.Revision + 1
			res := tx.Model(&notificationRuleModel{}).Where("id = ? AND revision = ?", rule.ID, current.Revision).Updates(map[string]any{"name": m.Name, "event": m.Event, "type": m.Type, "recipient": m.Recipient, "enabled": m.Enabled, "revision": m.Revision})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected != 1 {
				return biz.ErrNotificationConflict
			}
			nextRevision = m.Revision
		}
		return authzquery.AppendWriteAudit(ctx, tx, "notify.notification.rules.update", rule.ID)
	})
	if err == nil {
		rule.Revision = nextRevision
	}
	return authzquery.RecordWriteFailure(ctx, r.db, "notify.notification.rules.update", rule.ID, err)
}
func (r *Repository) TestRule(ctx context.Context, id int64) (*biz.Notification, error) {
	var out *biz.Notification
	if r.db == nil {
		if err := authorization.RequireDurableWrite(ctx, "notify.notification.test"); err != nil {
			return nil, err
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		rule := r.rules[id]
		if rule == nil {
			return nil, biz.ErrNotificationNotFound
		}
		if err := requireNotification(ctx, "notify.notification.test", id); err != nil {
			return nil, err
		}
		r.seq++
		out = &biz.Notification{ID: r.seq, Revision: 1, Type: rule.Type, Recipient: rule.Recipient, Subject: "Notification delivery test", Content: "This is a notification rule delivery test.", Status: biz.NotifyStatusPending, CreatedAt: time.Now()}
		r.mem[out.ID] = out
		copy := *out
		return &copy, nil
	}
	err := authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var rule notificationRuleModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&rule, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrNotificationNotFound
			}
			return err
		}
		if err := requireNotification(ctx, "notify.notification.test", rule.ID); err != nil {
			return err
		}
		m := notificationModel{Revision: 1, Type: rule.Type, Recipient: rule.Recipient, Subject: "Notification delivery test", Content: "This is a notification rule delivery test.", Status: biz.NotifyStatusPending, CreatedAt: time.Now().Unix()}
		if err := tx.Create(&m).Error; err != nil {
			return err
		}
		if err := authzquery.AppendWriteAudit(ctx, tx, "notify.notification.test", id); err != nil {
			return err
		}
		out = notificationFromModel(m)
		return nil
	})
	return out, authzquery.RecordWriteFailure(ctx, r.db, "notify.notification.test", id, err)
}
func (r *Repository) EventRules(ctx context.Context, event string) ([]*biz.NotificationRule, error) {
	out := []*biz.NotificationRule{}
	if r.db == nil {
		r.mu.RLock()
		defer r.mu.RUnlock()
		for _, v := range r.rules {
			if v.Event == event {
				copy := *v
				out = append(out, &copy)
			}
		}
		return out, nil
	}
	var rows []notificationRuleModel
	if err := r.db.WithContext(ctx).Where("event = ?", event).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, m := range rows {
		out = append(out, ruleToBiz(m))
	}
	return out, nil
}
