package data

import (
	"context"
	"errors"
	"fmt"
	"time"

	"micro-one-api/internal/billing/biz"
	"micro-one-api/internal/pkg/safecast"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PaymentOrder struct {
	ID               uint       `gorm:"column:id;primaryKey;autoIncrement"`
	UserID           string     `gorm:"column:user_id;type:varchar(64);index;not null"`
	TradeNo          string     `gorm:"column:trade_no;type:varchar(128);uniqueIndex;not null"`
	Channel          string     `gorm:"column:channel;type:varchar(32);index;not null"`
	AssetType        string     `gorm:"column:asset_type;type:varchar(32);index;not null"`
	AssetAmount      int64      `gorm:"column:asset_amount;not null"`
	MoneyCents       int64      `gorm:"column:money_cents;not null"`
	Currency         string     `gorm:"column:currency;type:varchar(16);not null;default:'CNY'"`
	Status           string     `gorm:"column:status;type:varchar(32);index;not null"`
	ProviderTradeNo  string     `gorm:"column:provider_trade_no;type:varchar(128);index"`
	ProviderPayload  string     `gorm:"column:provider_payload;type:text"`
	PayURL           string     `gorm:"column:pay_url;type:text"`
	AssetIssueStatus string     `gorm:"column:asset_issue_status;type:varchar(32);index;not null;default:'pending'"`
	GroupID          int64      `gorm:"column:group_id;type:bigint;default:0"`
	PlanID           int64      `gorm:"column:plan_id;type:bigint;default:0"`
	PlanSnapshot     string     `gorm:"column:plan_snapshot;type:text"`
	PaidAt           *time.Time `gorm:"column:paid_at;index"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
}

func (PaymentOrder) TableName() string { return "payment_orders" }

type paymentRepo struct {
	data *Data
}

func NewPaymentRepo(data *Data) biz.PaymentRepo {
	return &paymentRepo{data: data}
}

func (r *paymentRepo) CreateOrder(ctx context.Context, order *biz.PaymentOrder) (*biz.PaymentOrder, error) {
	po, err := toPOPaymentOrder(order)
	if err != nil {
		return nil, err
	}
	if err := r.data.db.WithContext(ctx).Create(po).Error; err != nil {
		return nil, fmt.Errorf("failed to create payment order: %w", err)
	}
	return toBizPaymentOrder(po)
}

func (r *paymentRepo) GetOrderByTradeNo(ctx context.Context, tradeNo string) (*biz.PaymentOrder, error) {
	var po PaymentOrder
	err := r.data.db.WithContext(ctx).Where("trade_no = ? OR provider_trade_no = ?", tradeNo, tradeNo).First(&po).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get payment order: %w", err)
	}
	return toBizPaymentOrder(&po)
}

func (r *paymentRepo) ListOrders(ctx context.Context, req biz.ListPaymentOrdersRequest) ([]*biz.PaymentOrder, int64, error) {
	page := req.Page
	pageSize := req.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	query := r.data.db.WithContext(ctx).Model(&PaymentOrder{})
	if req.UserID != "" {
		query = query.Where("user_id = ?", req.UserID)
	}
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}
	if req.Channel != "" {
		query = query.Where("channel = ?", req.Channel)
	}
	if req.TradeNo != "" {
		like := "%" + req.TradeNo + "%"
		query = query.Where("trade_no LIKE ? OR provider_trade_no LIKE ?", like, like)
	}
	if req.StartTime > 0 {
		query = query.Where("created_at >= ?", time.Unix(req.StartTime, 0))
	}
	if req.EndTime > 0 {
		query = query.Where("created_at <= ?", time.Unix(req.EndTime, 0))
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count payment orders: %w", err)
	}

	var rows []PaymentOrder
	offset := (page - 1) * pageSize
	if err := query.Offset(int(offset)).Limit(int(pageSize)).Order("id DESC").Find(&rows).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to list payment orders: %w", err)
	}
	orders := make([]*biz.PaymentOrder, len(rows))
	for i := range rows {
		order, err := toBizPaymentOrder(&rows[i])
		if err != nil {
			return nil, 0, err
		}
		orders[i] = order
	}
	return orders, total, nil
}

func (r *paymentRepo) MarkOrderPaid(ctx context.Context, tradeNo, providerTradeNo string, issue func(*biz.PaymentOrder) error) (*biz.PaymentOrder, bool, error) {
	var result *biz.PaymentOrder
	changed := false

	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var po PaymentOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("trade_no = ? OR provider_trade_no = ?", tradeNo, tradeNo).
			First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		order, err := toBizPaymentOrder(&po)
		if err != nil {
			return err
		}
		if providerTradeNo != "" {
			order.ProviderTradeNo = providerTradeNo
		}
		if po.Status == biz.PaymentOrderStatusPaid {
			result = order
			return nil
		}
		if po.Status != biz.PaymentOrderStatusPending {
			return fmt.Errorf("payment order status %q cannot be marked paid", po.Status)
		}
		if issue == nil {
			return errors.New("payment asset issue callback is required")
		}
		if err := issue(order); err != nil {
			return err
		}

		now := time.Now()
		if err := tx.Model(&PaymentOrder{}).Where("id = ?", po.ID).Updates(map[string]interface{}{
			"status":             biz.PaymentOrderStatusPaid,
			"provider_trade_no":  providerTradeNo,
			"asset_issue_status": biz.PaymentAssetIssueStatusIssued,
			"paid_at":            now,
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		po.Status = biz.PaymentOrderStatusPaid
		po.ProviderTradeNo = providerTradeNo
		po.AssetIssueStatus = biz.PaymentAssetIssueStatusIssued
		po.PaidAt = &now
		po.UpdatedAt = now
		result, err = toBizPaymentOrder(&po)
		if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("failed to mark payment order paid: %w", err)
	}
	return result, changed, nil
}

func (r *paymentRepo) MarkOrderClosed(ctx context.Context, tradeNo, providerTradeNo string) (*biz.PaymentOrder, bool, error) {
	var result *biz.PaymentOrder
	changed := false

	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var po PaymentOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("trade_no = ? OR provider_trade_no = ?", tradeNo, tradeNo).
			First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		order, err := toBizPaymentOrder(&po)
		if err != nil {
			return err
		}
		if po.Status == biz.PaymentOrderStatusClosed || po.Status == biz.PaymentOrderStatusPaid {
			result = order
			return nil
		}
		if po.Status != biz.PaymentOrderStatusPending {
			return fmt.Errorf("payment order status %q cannot be marked closed", po.Status)
		}

		now := time.Now()
		if err := tx.Model(&PaymentOrder{}).Where("id = ?", po.ID).Updates(map[string]interface{}{
			"status":            biz.PaymentOrderStatusClosed,
			"provider_trade_no": providerTradeNo,
			"updated_at":        now,
		}).Error; err != nil {
			return err
		}
		po.Status = biz.PaymentOrderStatusClosed
		po.ProviderTradeNo = providerTradeNo
		po.UpdatedAt = now
		result, err = toBizPaymentOrder(&po)
		if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("failed to mark payment order closed: %w", err)
	}
	return result, changed, nil
}

// MarkOrderRefunded transitions a paid order to status="refunded" inside a
// row-locked transaction. The revert callback runs inside the tx and performs
// the wallet credit + ledger reversal + subscription mutation so all three
// commit atomically. Returns changed=false when the order was already
// refunded (idempotent re-entry from a replayed refund callback).
func (r *paymentRepo) MarkOrderRefunded(ctx context.Context, tradeNo, reason string, revert func(*biz.PaymentOrder, *gorm.DB) error) (*biz.PaymentOrder, bool, error) {
	var result *biz.PaymentOrder
	changed := false

	err := r.data.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var po PaymentOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("trade_no = ? OR provider_trade_no = ?", tradeNo, tradeNo).
			First(&po).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}

		order, err := toBizPaymentOrder(&po)
		if err != nil {
			return err
		}
		// Idempotent: an already-refunded order short-circuits without
		// re-running the revert, so a replayed refund callback cannot
		// double-credit the wallet or double-revoke the subscription.
		if po.Status == biz.PaymentOrderStatusRefunded {
			result = order
			return nil
		}
		// Only paid orders can be refunded. Pending orders should be closed,
		// not refunded; closed orders have no wallet movement to reverse.
		if po.Status != biz.PaymentOrderStatusPaid {
			return fmt.Errorf("payment order status %q cannot be refunded", po.Status)
		}
		if revert == nil {
			return errors.New("refund revert callback is required")
		}
		if err := revert(order, tx); err != nil {
			return err
		}

		now := time.Now()
		if err := tx.Model(&PaymentOrder{}).Where("id = ?", po.ID).Updates(map[string]interface{}{
			"status":             biz.PaymentOrderStatusRefunded,
			"provider_payload":   reason,
			"asset_issue_status": "refunded",
			"updated_at":         now,
		}).Error; err != nil {
			return err
		}
		po.Status = biz.PaymentOrderStatusRefunded
		po.UpdatedAt = now
		result, err = toBizPaymentOrder(&po)
		if err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return nil, false, fmt.Errorf("failed to mark payment order refunded: %w", err)
	}
	return result, changed, nil
}

func toPOPaymentOrder(order *biz.PaymentOrder) (*PaymentOrder, error) {
	id, err := safecast.Int64ToUint(order.ID)
	if err != nil {
		return nil, err
	}
	return &PaymentOrder{
		ID:               id,
		UserID:           order.UserID,
		TradeNo:          order.TradeNo,
		Channel:          order.Channel,
		AssetType:        order.AssetType,
		AssetAmount:      order.AssetAmount,
		MoneyCents:       order.MoneyCents,
		Currency:         order.Currency,
		Status:           order.Status,
		ProviderTradeNo:  order.ProviderTradeNo,
		ProviderPayload:  order.ProviderPayload,
		PayURL:           order.PayURL,
		AssetIssueStatus: order.AssetIssueStatus,
		GroupID:          order.GroupID,
		PlanID:           order.PlanID,
		PlanSnapshot:     order.PlanSnapshot,
		PaidAt:           order.PaidAt,
		CreatedAt:        order.CreatedAt,
		UpdatedAt:        order.UpdatedAt,
	}, nil
}

func toBizPaymentOrder(po *PaymentOrder) (*biz.PaymentOrder, error) {
	id, err := safecast.UintToInt64(po.ID)
	if err != nil {
		return nil, err
	}
	return &biz.PaymentOrder{
		ID:               id,
		UserID:           po.UserID,
		TradeNo:          po.TradeNo,
		Channel:          po.Channel,
		AssetType:        po.AssetType,
		AssetAmount:      po.AssetAmount,
		MoneyCents:       po.MoneyCents,
		Currency:         po.Currency,
		Status:           po.Status,
		ProviderTradeNo:  po.ProviderTradeNo,
		ProviderPayload:  po.ProviderPayload,
		PayURL:           po.PayURL,
		AssetIssueStatus: po.AssetIssueStatus,
		GroupID:          po.GroupID,
		PlanID:           po.PlanID,
		PlanSnapshot:     po.PlanSnapshot,
		PaidAt:           po.PaidAt,
		CreatedAt:        po.CreatedAt,
		UpdatedAt:        po.UpdatedAt,
	}, nil
}
