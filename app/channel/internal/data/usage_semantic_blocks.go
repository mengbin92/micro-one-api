package data

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"micro-one-api/app/channel/internal/biz"
)

// usageSemanticSourceBlockModel maps migration 087
// (usage_semantic_source_blocks): the usage-semantics quarantine keyed by
// execution source + upstream model + adapter protocol.
type usageSemanticSourceBlockModel struct {
	ID                   int64      `gorm:"column:id;primaryKey;autoIncrement"`
	SourceKind           string     `gorm:"column:source_kind;uniqueIndex:uk_usage_semantic_block_key,priority:1"`
	SourceID             int64      `gorm:"column:source_id;uniqueIndex:uk_usage_semantic_block_key,priority:2"`
	UpstreamModelID      string     `gorm:"column:upstream_model_id;uniqueIndex:uk_usage_semantic_block_key,priority:3"`
	AdapterProtocol      string     `gorm:"column:adapter_protocol;uniqueIndex:uk_usage_semantic_block_key,priority:4"`
	Status               string     `gorm:"column:status"`
	Reason               string     `gorm:"column:reason"`
	WindowStartedAt      *time.Time `gorm:"column:window_started_at"`
	ConsecutiveAmbiguous int32      `gorm:"column:consecutive_ambiguous"`
	BlockedUntil         *time.Time `gorm:"column:blocked_until"`
	LastVerifiedAt       *time.Time `gorm:"column:last_verified_at"`
	CreatedAt            time.Time  `gorm:"column:created_at"`
	UpdatedAt            time.Time  `gorm:"column:updated_at"`
}

func (usageSemanticSourceBlockModel) TableName() string { return "usage_semantic_source_blocks" }

func usageSemanticBlockToBiz(m *usageSemanticSourceBlockModel) biz.UsageSemanticBlock {
	out := biz.UsageSemanticBlock{
		ID:                   m.ID,
		SourceKind:           m.SourceKind,
		SourceID:             m.SourceID,
		UpstreamModelID:      m.UpstreamModelID,
		AdapterProtocol:      m.AdapterProtocol,
		Status:               m.Status,
		Reason:               m.Reason,
		ConsecutiveAmbiguous: m.ConsecutiveAmbiguous,
		UpdatedAt:            m.UpdatedAt,
	}
	if m.WindowStartedAt != nil {
		out.WindowStartedAt = *m.WindowStartedAt
	}
	if m.BlockedUntil != nil {
		out.BlockedUntil = *m.BlockedUntil
	}
	if m.LastVerifiedAt != nil {
		out.LastVerifiedAt = *m.LastVerifiedAt
	}
	return out
}

// UpsertUsageSemanticVerdict applies one final-attempt verdict to the
// quarantine row keyed by (source_kind, source_id, upstream_model_id,
// adapter_protocol):
//
//   - verified: resets the consecutive counter, reactivates the row and
//     stamps last_verified_at. An ACTIVE block whose blocked_until is still
//     in the future is NOT auto-cleared — recovery requires the quarantine
//     window to expire or manual resolve (§5.2 point 6); verified verdicts
//     only stop the counter from advancing.
//   - ambiguous: starts (or continues) the sliding window, increments the
//     consecutive counter, and trips status=blocked with
//     blocked_until=now+blockDuration once the threshold is reached.
//
// The upsert is keyed on the unique index uk_usage_semantic_block_key so
// concurrent relays converge on one row per key.
func (r *Repository) UpsertUsageSemanticVerdict(ctx context.Context, verdict biz.UsageSemanticVerdict, window, blockDuration time.Duration, threshold int32, now time.Time) (*biz.UsageSemanticBlock, error) {
	if r.db == nil {
		return nil, nil
	}
	if threshold <= 0 {
		threshold = 3
	}
	key := usageSemanticSourceBlockModel{
		SourceKind:      verdict.SourceKind,
		SourceID:        verdict.SourceID,
		UpstreamModelID: verdict.UpstreamModelID,
		AdapterProtocol: verdict.AdapterProtocol,
	}
	if verdict.ParseStatus != "verified" && verdict.ParseStatus != "ambiguous" {
		return nil, nil
	}

	var out *biz.UsageSemanticBlock
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A verified request without prior ambiguous state has nothing to
		// reset. Avoid creating a quarantine row for every healthy request.
		if verdict.ParseStatus == "ambiguous" {
			seed := key
			seed.Status = biz.UsageSemanticBlockStatusActive
			seed.CreatedAt = now
			seed.UpdatedAt = now
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seed).Error; err != nil {
				return err
			}
		}

		var row usageSemanticSourceBlockModel
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(key).First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) && verdict.ParseStatus == "verified" {
			return nil
		}
		if err != nil {
			return err
		}

		updates := map[string]any{"updated_at": now}
		switch verdict.ParseStatus {
		case "verified":
			updates["consecutive_ambiguous"] = 0
			updates["window_started_at"] = nil
			updates["last_verified_at"] = now
			if row.Status == "" {
				updates["status"] = biz.UsageSemanticBlockStatusActive
			} else if row.Status == biz.UsageSemanticBlockStatusBlocked && row.BlockedUntil != nil && !row.BlockedUntil.After(now) {
				// Quarantine expired and a verified verdict arrived: auto-reactivate.
				updates["status"] = biz.UsageSemanticBlockStatusActive
				updates["blocked_until"] = nil
			}
		case "ambiguous":
			windowStart := now
			consecutive := int32(1)
			if row.WindowStartedAt != nil && now.Sub(*row.WindowStartedAt) <= window {
				windowStart = *row.WindowStartedAt
				consecutive = row.ConsecutiveAmbiguous + 1
			}
			updates["window_started_at"] = windowStart
			updates["consecutive_ambiguous"] = consecutive
			updates["reason"] = verdict.Reason
			updates["status"] = biz.UsageSemanticBlockStatusActive
			if consecutive >= threshold {
				blockedUntil := now.Add(blockDuration)
				updates["status"] = biz.UsageSemanticBlockStatusBlocked
				updates["blocked_until"] = blockedUntil
			}
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Where(key).First(&row).Error; err != nil {
			return err
		}
		converted := usageSemanticBlockToBiz(&row)
		out = &converted
		return nil
	})
	return out, err
}

// ListBlockedUsageSemanticBlocks returns every row whose block is currently
// in effect — the selector's filter set (§6.1 index on status+blocked_until).
func (r *Repository) ListBlockedUsageSemanticBlocks(ctx context.Context, now time.Time) ([]biz.UsageSemanticBlock, error) {
	if r.db == nil {
		return nil, nil
	}
	var rows []usageSemanticSourceBlockModel
	err := r.db.WithContext(ctx).
		Where("status = ? AND blocked_until > ?", biz.UsageSemanticBlockStatusBlocked, now).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]biz.UsageSemanticBlock, len(rows))
	for i := range rows {
		out[i] = usageSemanticBlockToBiz(&rows[i])
	}
	return out, nil
}

// ResolveUsageSemanticBlock clears a block after an operator confirms the
// adapter fix (§5.2 point 6). Returns false when no matching row exists.
func (r *Repository) ResolveUsageSemanticBlock(ctx context.Context, sourceKind string, sourceID int64, upstreamModelID, adapterProtocol string, now time.Time) (bool, error) {
	if r.db == nil {
		return false, nil
	}
	res := r.db.WithContext(ctx).Model(&usageSemanticSourceBlockModel{}).
		Where("source_kind = ? AND source_id = ? AND upstream_model_id = ? AND adapter_protocol = ? AND status = ?",
			sourceKind, sourceID, upstreamModelID, adapterProtocol, biz.UsageSemanticBlockStatusBlocked).
		Updates(map[string]any{
			"status":                biz.UsageSemanticBlockStatusResolved,
			"consecutive_ambiguous": 0,
			"window_started_at":     nil,
			"blocked_until":         nil,
			"updated_at":            now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListUsageSemanticBlocks pages quarantine rows for the admin surface.
func (r *Repository) ListUsageSemanticBlocks(ctx context.Context, onlyBlocked bool, page, pageSize int32) ([]biz.UsageSemanticBlock, int64, error) {
	if r.db == nil {
		return nil, 0, nil
	}
	query := r.db.WithContext(ctx).Model(&usageSemanticSourceBlockModel{})
	if onlyBlocked {
		query = query.Where("status = ?", biz.UsageSemanticBlockStatusBlocked)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 200 {
		pageSize = 50
	}
	var rows []usageSemanticSourceBlockModel
	err := query.Order("updated_at DESC").
		Offset(int((page - 1) * pageSize)).Limit(int(pageSize)).
		Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	out := make([]biz.UsageSemanticBlock, len(rows))
	for i := range rows {
		out[i] = usageSemanticBlockToBiz(&rows[i])
	}
	return out, total, nil
}
