package data

import (
	"context"
	"slices"
	"strings"
	"time"

	"micro-one-api/app/channel/internal/biz"
)

func (r *Repository) ListSubscriptionAccountsByRecovery(ctx context.Context, page, pageSize int32, keyword, group string, status int32, platform, policy string) ([]*biz.SubscriptionAccount, int64, error) {
	var candidates []*biz.SubscriptionAccount
	if r.db != nil {
		// Recovery metadata is a legacy text column and may contain invalid JSON.
		// Decode at the storage boundary to keep filtering identical across drivers.
		var rows []subscriptionAccountModel
		query := r.db.WithContext(ctx).Where("metadata LIKE ?", "%"+policy+"%")
		if status != 0 {
			query = query.Where("status = ?", status)
		}
		if platform != "" {
			query = query.Where("platform = ?", platform)
		}
		if group != "" {
			query = query.Where(r.routingGroupSQL("`group` = ?"), group)
		}
		if err := query.Find(&rows).Error; err != nil {
			return nil, 0, err
		}
		for i := range rows {
			candidates = append(candidates, r.subscriptionAccountModelToBiz(&rows[i]))
		}
	} else {
		r.lock.RLock()
		for _, a := range r.subAccounts {
			copy := *a
			candidates = append(candidates, &copy)
		}
		r.lock.RUnlock()
	}
	now := time.Now()
	filtered := make([]*biz.SubscriptionAccount, 0)
	for _, a := range candidates {
		if (status != 0 && a.Status != status) || (platform != "" && a.Platform != platform) || (group != "" && a.Group != group) {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(a.Name+" "+a.AccountID), strings.ToLower(keyword)) {
			continue
		}
		info := a.RecoveryInfo(now)
		if info.Policy == policy && !a.IsSchedulableAt(now) {
			filtered = append(filtered, a)
		}
	}
	slices.SortFunc(filtered, func(a, b *biz.SubscriptionAccount) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 1000 {
		pageSize = 20
	}
	total := int64(len(filtered))
	start := min(int64(page-1)*int64(pageSize), total)
	items := filtered[start:min(start+int64(pageSize), total)]
	if r.db != nil {
		if err := r.attachAccountQuotaSnapshots(ctx, items); err != nil {
			return nil, 0, err
		}
	}
	return items, total, nil
}
