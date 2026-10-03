package data

import (
	"context"
	"github.com/go-kratos/kratos/v3/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/channel/internal/biz"
	"micro-one-api/domain/authorization"
	"micro-one-api/platform/database/authzquery"
	"slices"
	"time"
)

func (r *Repository) SaveChannelAction(ctx context.Context, expected *biz.Channel, operation string, result *biz.ChannelActionResult, threshold int32, cooldown time.Duration) (*biz.Channel, error) {
	if r.db == nil {
		return nil, authorization.ErrDenied
	}
	var out *biz.Channel
	err := authzquery.RunInTx(ctx, r.db, 3, func(current context.Context, tx *gorm.DB) error {
		owner := &Repository{db: tx, encKey: r.encKey, routingGroupRelations: r.routingGroupRelations}
		var row channelModel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, expected.ID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return biz.ErrChannelNotFound
			}
			return err
		}
		stored, err := owner.FindByID(current, expected.ID)
		if err != nil {
			return err
		}
		if stored.Key != expected.Key || stored.BaseURL != expected.BaseURL || stored.Type != expected.Type || stored.Group != expected.Group || stored.Config != expected.Config || !slices.Equal(stored.Models, expected.Models) {
			return errors.Conflict("CHANNEL_REVISION_CONFLICT", "channel configuration changed during provider request")
		}
		if err := r.checkResourceTx(current, tx, stored.ID, "", false, operation); err != nil {
			return err
		}
		updates := map[string]any{}
		if operation == "channel.channel.test" {
			applyHealthEvent(stored, biz.ChannelHealthEvent{ChannelID: stored.ID, Success: result.Success, Error: result.Message, ResponseTime: result.ResponseTime, CheckedAt: time.Unix(result.CheckedAt, 0)}, threshold, cooldown)
			updates = map[string]any{"test_time": stored.TestTime, "response_time": stored.ResponseTime, "health_status": stored.HealthStatus, "health_last_error": stored.HealthLastError, "health_last_success_time": stored.HealthLastSuccessTime, "health_last_failure_time": stored.HealthLastFailureTime, "health_consecutive_failures": stored.HealthConsecutiveFailures, "circuit_opened_until": stored.CircuitOpenedUntil}
		} else if operation == "channel.channel.balance.refresh" {
			stored.BalanceRefreshLastError = result.BalanceRefreshLastError
			if result.Success {
				stored.Balance = result.Balance
				stored.BalanceUpdatedTime = result.CheckedAt
				stored.BalanceRefreshLastSuccessTime = result.CheckedAt
				stored.ConsecutiveBalanceRefreshFailures = 0
			} else {
				stored.ConsecutiveBalanceRefreshFailures++
			}
			updates = map[string]any{"balance": stored.Balance, "balance_updated_time": stored.BalanceUpdatedTime, "balance_refresh_last_error": stored.BalanceRefreshLastError, "balance_refresh_last_success_time": stored.BalanceRefreshLastSuccessTime, "consecutive_balance_refresh_failures": stored.ConsecutiveBalanceRefreshFailures}
		} else {
			return authorization.ErrDenied
		}
		if err := tx.Model(&row).Updates(updates).Error; err != nil {
			return err
		}

		out = stored
		return nil
	})
	return out, err
}
