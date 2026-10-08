package data

import (
	"context"
	"errors"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
	"micro-one-api/platform/database/xdb"
)

// ScanSubscriptionAccounts filters shard ownership in storage, before reading
// rows or loading quota snapshots. ID cursors remain stable when status changes.
func (r *Repository) ScanSubscriptionAccounts(ctx context.Context, scan biz.AccountScan) ([]*biz.SubscriptionAccount, error) {
	if err := scan.Shard.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scan.Limit <= 0 {
		scan.Limit = 200
	}
	if r.db != nil {
		query, err := r.subscriptionAccountScope(ctx, r.db.WithContext(ctx).Model(&subscriptionAccountModel{}), "subscription_accounts")
		if err != nil {
			return nil, err
		}
		query = query.Where("subscription_accounts.id > ?", scan.AfterID)
		if scan.Shard.EffectiveCount() > 1 {
			query = query.Where("subscription_accounts.id % ? = ?", scan.Shard.Count, scan.Shard.Index)
		}
		if scan.Status != 0 {
			query = query.Where("status = ?", scan.Status)
		}
		if scan.FixedOnly {
			query = query.Where("LOWER(TRIM(quota_reset_strategy)) = ?", biz.QuotaResetStrategyFixed)
		}
		var rows []subscriptionAccountModel
		if err := query.Order("subscription_accounts.id ASC").Limit(int(scan.Limit)).Find(&rows).Error; err != nil {
			return nil, err
		}
		accounts := make([]*biz.SubscriptionAccount, len(rows))
		for i := range rows {
			accounts[i] = r.subscriptionAccountModelToBiz(&rows[i])
		}
		if err := r.attachAccountQuotaSnapshots(ctx, accounts); err != nil {
			return nil, err
		}
		return accounts, nil
	}
	if _, iam := authorization.QueryScopeFromContext(ctx, "channel.account.list"); iam {
		return nil, authorization.ErrDenied
	}
	r.lock.RLock()
	defer r.lock.RUnlock()
	var ids []int64
	for id, a := range r.subAccounts {
		if id <= scan.AfterID || id%scan.Shard.EffectiveCount() != scan.Shard.Index ||
			(scan.Status != 0 && a.Status != scan.Status) || (scan.FixedOnly && !a.UsesFixedQuotaReset()) {
			continue
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	ids = ids[:min(len(ids), int(scan.Limit))]
	accounts := make([]*biz.SubscriptionAccount, len(ids))
	for i, id := range ids {
		accounts[i] = cloneSubscriptionAccount(r.subAccounts[id])
	}
	return accounts, nil
}

func cloneSubscriptionAccount(account *biz.SubscriptionAccount) *biz.SubscriptionAccount {
	copy := *account
	copy.Models = slices.Clone(copy.Models)
	copy.PermittedActions = slices.Clone(copy.PermittedActions)
	copy.PrimaryQuotaUsedPercent = cloneAccountValue(copy.PrimaryQuotaUsedPercent)
	copy.PrimaryQuotaResetAfterSeconds = cloneAccountValue(copy.PrimaryQuotaResetAfterSeconds)
	copy.PrimaryQuotaWindowMinutes = cloneAccountValue(copy.PrimaryQuotaWindowMinutes)
	copy.SecondaryQuotaUsedPercent = cloneAccountValue(copy.SecondaryQuotaUsedPercent)
	copy.SecondaryQuotaResetAfterSeconds = cloneAccountValue(copy.SecondaryQuotaResetAfterSeconds)
	copy.SecondaryQuotaWindowMinutes = cloneAccountValue(copy.SecondaryQuotaWindowMinutes)
	copy.PrimaryOverSecondaryPercent = cloneAccountValue(copy.PrimaryOverSecondaryPercent)
	copy.RecoveryBaseline = new(copy.RecoveryState())
	return &copy
}

func cloneAccountValue[T any](value *T) *T {
	if value == nil {
		return nil
	}
	return new(*value)
}

func (r *Repository) ClearRecoveryMarkers(ctx context.Context, expected biz.AccountRecoveryState, at time.Time) (bool, error) {
	if expected.AccountID <= 0 {
		return false, biz.ErrSubscriptionAccountNotFound
	}
	if r.db != nil {
		return r.clearRecoveryMarkersDB(ctx, expected, at)
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	r.lock.Lock()
	defer r.lock.Unlock()
	a, ok := r.subAccounts[expected.AccountID]
	if !ok {
		return false, biz.ErrSubscriptionAccountNotFound
	}
	if a.RecoveryState() != expected || !a.CanAutoRecoverAt(at) {
		return false, nil
	}
	a.RateLimitedUntil = 0
	a.LastError = ""
	a.Metadata = clearSubscriptionAccountRecoveryMetadata(setSubscriptionAccountMetadataValue(a.Metadata, "last_error", ""))
	a.UpdatedAt = now()
	return true, nil
}

func (r *Repository) clearRecoveryMarkersDB(ctx context.Context, expected biz.AccountRecoveryState, at time.Time) (bool, error) {
	cleared := false
	err := authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		cleared = false // A failed attempt may be replayed after SQLite contention.
		if err := r.checkResourceTx(ctx, tx, expected.AccountID, "", true, "channel.account.recovery.clear"); err != nil {
			return err
		}
		var row subscriptionAccountModel
		query := tx.Where("id = ?", expected.AccountID)
		if tx.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&row).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		a := r.subscriptionAccountModelToBiz(&row)
		if a.RecoveryState() != expected {
			return nil
		}
		// A locking read also avoids an older MySQL repeatable-read snapshot.
		// Snapshot writers take the account lock first, in the same order.
		var snapshot accountQuotaSnapshotModel
		snapshotQuery := tx.Where("account_id = ?", expected.AccountID)
		if tx.Dialector.Name() != "sqlite" {
			snapshotQuery = snapshotQuery.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		snapshotResult := snapshotQuery.Limit(1).Find(&snapshot)
		if snapshotResult.Error != nil {
			return snapshotResult.Error
		}
		if snapshotResult.RowsAffected > 0 {
			applyAccountQuotaSnapshot(a, accountQuotaSnapshotModelToBiz(&snapshot))
		}
		if !a.CanAutoRecoverAt(at) {
			return nil
		}
		metadata := clearSubscriptionAccountRecoveryMetadata(setSubscriptionAccountMetadataValue(a.Metadata, "last_error", ""))
		write := tx.Model(&subscriptionAccountModel{}).Where("id = ? AND status = ? AND rate_limited_until = ? AND credential_revision = ?",
			expected.AccountID, expected.Status, expected.RateLimitedUntil, expected.CredentialRevision)
		if row.Metadata == nil {
			write = write.Where("metadata IS NULL")
		} else if tx.Dialector.Name() == "mysql" {
			write = write.Where("CAST(metadata AS BINARY) = ?", *row.Metadata)
		} else {
			write = write.Where("metadata = ?", *row.Metadata)
		}
		res := write.Updates(map[string]any{"rate_limited_until": 0, "metadata": new(metadata), "updated_at": now()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := tx.Model(&subscriptionAccountAbilityModel{}).Where("account_id = ?", expected.AccountID).Update("enabled", xdb.BoolInt(true)).Error; err != nil {
			return err
		}
		cleared = true
		return nil
	})
	return cleared && err == nil, err
}

// Targeted metadata writers must read under the account lock so an alert or
// manual cleanup cannot restore a previously read incident revision.
func (r *Repository) updateSubscriptionAccountMetadataDB(ctx context.Context, accountID int64, update func(string) string) error {
	return authzquery.RunInTx(ctx, r.db, 3, func(ctx context.Context, tx *gorm.DB) error {
		var row subscriptionAccountModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, accountID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return biz.ErrSubscriptionAccountNotFound
			}
			return err
		}
		metadata := update(derefString(row.Metadata))
		return tx.Model(&subscriptionAccountModel{}).Where("id = ?", accountID).Updates(map[string]any{
			"metadata": new(metadata), "updated_at": now(),
		}).Error
	})
}
