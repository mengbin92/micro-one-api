package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type BillingUsecase struct {
	accountRepo     AccountRepo
	reservationRepo ReservationRepo
	ledgerRepo      LedgerRepo
	redeemRepo      RedeemRepo
	groupRatios     map[string]float64
}

func NewBillingUsecase(
	accountRepo AccountRepo,
	reservationRepo ReservationRepo,
	ledgerRepo LedgerRepo,
	redeemRepo RedeemRepo,
	groupRatios map[string]float64,
) *BillingUsecase {
	if len(groupRatios) == 0 {
		groupRatios = DefaultGroupRatios()
	}
	return &BillingUsecase{
		accountRepo:     accountRepo,
		reservationRepo: reservationRepo,
		ledgerRepo:      ledgerRepo,
		redeemRepo:      redeemRepo,
		groupRatios:     groupRatios,
	}
}

func (uc *BillingUsecase) ReserveQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string) (*Reservation, error) {
	if requestID != "" {
		existing, err := uc.reservationRepo.FindByRequestID(ctx, requestID)
		if err != nil {
			return nil, fmt.Errorf("find by request id: %w", err)
		}
		if existing != nil && (existing.IsReserved() || existing.IsCommitted()) {
			return existing, nil
		}
	}

	account, err := uc.accountRepo.GetAccountSnapshot(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get account snapshot: %w", err)
	}

	groupRatio := uc.getGroupRatio(account.Group)
	cost := int64(float64(estimatedTokens) * groupRatio)

	if cost <= 0 {
		cost = 1
	}

	if account.AvailableQuota() < cost {
		return nil, ErrInsufficientQuota
	}

	reservationID := generateReservationID()
	now := time.Now()
	expiredAt := now.Add(5 * time.Minute)

	reservation := &Reservation{
		ReservationID: reservationID,
		UserID:        userID,
		RequestID:     requestID,
		Amount:        cost,
		Status:        ReservationStatusReserved,
		Model:         model,
		ChannelID:     channelID,
		CreatedAt:     now,
		UpdatedAt:     now,
		ExpiredAt:     expiredAt,
	}

	if err := uc.reservationRepo.CreateReservation(ctx, reservation); err != nil {
		return nil, fmt.Errorf("create reservation: %w", err)
	}

	if _, err := uc.accountRepo.UpdateQuota(ctx, userID, -cost, LedgerTypeConsume); err != nil {
		return nil, fmt.Errorf("update quota: %w", err)
	}

	if err := uc.accountRepo.UpdateFrozenQuota(ctx, userID, cost); err != nil {
		return nil, fmt.Errorf("update frozen quota: %w", err)
	}

	return reservation, nil
}

func (uc *BillingUsecase) CommitQuota(ctx context.Context, reservationID string, actualTokens int64, success bool) (int64, int64, error) {
	reservation, err := uc.reservationRepo.GetReservation(ctx, reservationID)
	if err != nil {
		return 0, 0, fmt.Errorf("get reservation: %w", err)
	}

	if !reservation.IsReserved() {
		return 0, 0, errors.Join(ErrReservationCommitted, ErrReservationReleased)
	}

	account, err := uc.accountRepo.GetAccountSnapshot(ctx, reservation.UserID)
	if err != nil {
		return 0, 0, fmt.Errorf("get account snapshot: %w", err)
	}

	groupRatio := uc.getGroupRatio(account.Group)
	actualCost := int64(float64(actualTokens) * groupRatio)

	if actualCost <= 0 {
		actualCost = 1
	}

	if success {
		diff := reservation.Amount - actualCost

		if err := uc.reservationRepo.UpdateReservationStatus(ctx, reservationID, ReservationStatusCommitted); err != nil {
			return 0, 0, fmt.Errorf("update reservation status: %w", err)
		}

		if err := uc.accountRepo.UpdateFrozenQuota(ctx, reservation.UserID, -reservation.Amount); err != nil {
			return 0, 0, fmt.Errorf("release frozen quota: %w", err)
		}

		ledger := &Ledger{
			UserID:       reservation.UserID,
			Amount:       -actualCost,
			BalanceAfter: account.Quota,
			Type:         LedgerTypeConsume,
			ReferenceID:  reservationID,
			Remark:       fmt.Sprintf("model=%s, tokens=%d", reservation.Model, actualTokens),
		}

		if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
			return 0, 0, fmt.Errorf("create ledger: %w", err)
		}

		if diff > 0 {
			refundBalanceAfter, err := uc.accountRepo.UpdateQuota(ctx, reservation.UserID, diff, LedgerTypeRefund)
			if err != nil {
				return 0, 0, fmt.Errorf("refund quota: %w", err)
			}

			refundLedger := &Ledger{
				UserID:       reservation.UserID,
				Amount:       diff,
				BalanceAfter: refundBalanceAfter,
				Type:         LedgerTypeRefund,
				ReferenceID:  reservationID,
				Remark:       "refund from reservation",
			}

			if err := uc.ledgerRepo.CreateLedger(ctx, refundLedger); err != nil {
				return 0, 0, fmt.Errorf("create refund ledger: %w", err)
			}
		}

		return actualCost, diff, nil
	} else {
		if err := uc.reservationRepo.UpdateReservationStatus(ctx, reservationID, ReservationStatusReleased); err != nil {
			return 0, 0, fmt.Errorf("update reservation status: %w", err)
		}

		if err := uc.accountRepo.UpdateFrozenQuota(ctx, reservation.UserID, -reservation.Amount); err != nil {
			return 0, 0, fmt.Errorf("release frozen quota: %w", err)
		}

		newQuota, err := uc.accountRepo.UpdateQuota(ctx, reservation.UserID, reservation.Amount, LedgerTypeRefund)
		if err != nil {
			return 0, 0, fmt.Errorf("update quota: %w", err)
		}

		balanceAfter := newQuota
		ledger := &Ledger{
			UserID:       reservation.UserID,
			Amount:       reservation.Amount,
			BalanceAfter: balanceAfter,
			Type:         LedgerTypeRefund,
			ReferenceID:  reservationID,
			Remark:       "request failed, release reservation",
		}

		if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
			return 0, 0, fmt.Errorf("create ledger: %w", err)
		}

		return 0, reservation.Amount, nil
	}
}

func (uc *BillingUsecase) ReleaseQuota(ctx context.Context, reservationID, reason string) error {
	reservation, err := uc.reservationRepo.GetReservation(ctx, reservationID)
	if err != nil {
		return fmt.Errorf("get reservation: %w", err)
	}

	if !reservation.IsReserved() {
		return errors.Join(ErrReservationCommitted, ErrReservationReleased)
	}

	if err := uc.reservationRepo.UpdateReservationStatus(ctx, reservationID, ReservationStatusReleased); err != nil {
		return fmt.Errorf("update reservation status: %w", err)
	}

	if err := uc.accountRepo.UpdateFrozenQuota(ctx, reservation.UserID, -reservation.Amount); err != nil {
		return fmt.Errorf("release frozen quota: %w", err)
	}

	newQuota, err := uc.accountRepo.UpdateQuota(ctx, reservation.UserID, reservation.Amount, LedgerTypeRefund)
	if err != nil {
		return fmt.Errorf("update quota: %w", err)
	}

	balanceAfter := newQuota
	ledger := &Ledger{
		UserID:       reservation.UserID,
		Amount:       reservation.Amount,
		BalanceAfter: balanceAfter,
		Type:         LedgerTypeRefund,
		ReferenceID:  reservationID,
		Remark:       reason,
	}

	if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
		return fmt.Errorf("create ledger: %w", err)
	}

	return nil
}

func (uc *BillingUsecase) GetAccountSnapshot(ctx context.Context, userID string) (*Account, error) {
	return uc.accountRepo.GetAccountSnapshot(ctx, userID)
}

func (uc *BillingUsecase) TopUpQuota(ctx context.Context, userID, operatorID string, amount int64, remark string) (int64, error) {
	_, err := uc.accountRepo.GetAccountSnapshot(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("get account snapshot: %w", err)
	}

	newQuota, err := uc.accountRepo.UpdateQuota(ctx, userID, amount, LedgerTypeRecharge)
	if err != nil {
		return 0, fmt.Errorf("update quota: %w", err)
	}

	balanceAfter := newQuota
	ledger := &Ledger{
		UserID:       userID,
		Amount:       amount,
		BalanceAfter: balanceAfter,
		Type:         LedgerTypeRecharge,
		Remark:       fmt.Sprintf("operator=%s, remark=%s", operatorID, remark),
	}

	if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
		return 0, fmt.Errorf("create ledger: %w", err)
	}

	return newQuota, nil
}

func (uc *BillingUsecase) CreateRedeemCode(ctx context.Context, code, name string, amount int64, count int32, operatorID string) error {
	redeemCode := &RedeemCode{
		Code:      code,
		Name:      name,
		Amount:    amount,
		Count:     count,
		Status:    RedeemCodeStatusEnabled,
		CreatedBy: operatorID,
	}

	if err := uc.redeemRepo.CreateRedeemCode(ctx, redeemCode); err != nil {
		return fmt.Errorf("create redeem code: %w", err)
	}

	return nil
}

func (uc *BillingUsecase) CreateRedeemCodesBatch(ctx context.Context, name string, amount int64, count, batchSize int32, operatorID string) ([]string, error) {
	if count <= 0 || count > 100 {
		return nil, fmt.Errorf("count must be between 1 and 100")
	}
	if batchSize <= 0 || batchSize > 100 {
		batchSize = count
	}

	codes := make([]string, 0, count)
	now := time.Now()

	// 生成兑换码
	for i := int32(0); i < count; i++ {
		code := generateRedeemCode()
		codes = append(codes, code)
	}

	// 批量创建
	redeemCodes := make([]*RedeemCode, len(codes))
	for i, code := range codes {
		redeemCodes[i] = &RedeemCode{
			Code:      code,
			Name:      name,
			Amount:    amount,
			Count:     1, // 每个兑换码只能使用一次
			Status:    RedeemCodeStatusEnabled,
			CreatedBy: operatorID,
			CreatedAt: now,
			UpdatedAt: now,
		}
	}

	if err := uc.redeemRepo.CreateRedeemCodesBatch(ctx, redeemCodes); err != nil {
		return nil, fmt.Errorf("create redeem codes batch: %w", err)
	}

	return codes, nil
}

func (uc *BillingUsecase) GetRedeemCode(ctx context.Context, code string) (*RedeemCode, error) {
	return uc.redeemRepo.GetRedeemCode(ctx, code)
}

func (uc *BillingUsecase) ListRedeemCodes(ctx context.Context, page, pageSize int32) ([]*RedeemCode, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	return uc.redeemRepo.ListRedeemCodes(ctx, page, pageSize)
}

func (uc *BillingUsecase) SearchRedeemCodes(ctx context.Context, keyword string) ([]*RedeemCode, error) {
	return uc.redeemRepo.SearchRedeemCodes(ctx, keyword)
}

func (uc *BillingUsecase) UpdateRedeemCode(ctx context.Context, code, name string, amount int64, status int32) error {
	redeemCode := &RedeemCode{
		Code:   code,
		Name:   name,
		Amount: amount,
		Status: status,
	}

	if err := uc.redeemRepo.UpdateRedeemCode(ctx, redeemCode); err != nil {
		return fmt.Errorf("update redeem code: %w", err)
	}

	return nil
}

func (uc *BillingUsecase) DeleteRedeemCode(ctx context.Context, code string) error {
	if err := uc.redeemRepo.DeleteRedeemCode(ctx, code); err != nil {
		return fmt.Errorf("delete redeem code: %w", err)
	}
	return nil
}

func (uc *BillingUsecase) RedeemCode(ctx context.Context, userID, code string) (int64, int64, error) {
	redeemCode, err := uc.redeemRepo.GetRedeemCode(ctx, code)
	if err != nil {
		return 0, 0, fmt.Errorf("get redeem code: %w", err)
	}

	if !redeemCode.IsAvailable() {
		if !redeemCode.IsEnabled() {
			return 0, 0, ErrRedeemCodeDisabled
		}
		return 0, 0, ErrRedeemCodeUsedUp
	}

	account, err := uc.accountRepo.GetAccountSnapshot(ctx, userID)
	if err != nil {
		return 0, 0, fmt.Errorf("get account snapshot: %w", err)
	}

	quotaBefore := account.Quota

	if err := uc.redeemRepo.UpdateRedeemCodeCount(ctx, code, 1); err != nil {
		return 0, 0, fmt.Errorf("update redeem code count: %w", err)
	}

	newQuota, err := uc.accountRepo.UpdateQuota(ctx, userID, redeemCode.Amount, LedgerTypeRedeem)
	if err != nil {
		return 0, 0, fmt.Errorf("update quota: %w", err)
	}

	balanceAfter := newQuota
	ledger := &Ledger{
		UserID:       userID,
		Amount:       redeemCode.Amount,
		BalanceAfter: balanceAfter,
		Type:         LedgerTypeRedeem,
		ReferenceID:  code,
		Remark:       fmt.Sprintf("redeem code=%s", code),
	}

	if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
		return 0, 0, fmt.Errorf("create ledger: %w", err)
	}

	record := &RedeemRecord{
		UserID:      userID,
		Code:        code,
		Amount:      redeemCode.Amount,
		QuotaBefore: quotaBefore,
		QuotaAfter:  newQuota,
	}

	if err := uc.redeemRepo.CreateRedeemRecord(ctx, record); err != nil {
		return 0, 0, fmt.Errorf("create redeem record: %w", err)
	}

	return redeemCode.Amount, newQuota, nil
}

func (uc *BillingUsecase) ListLedgers(ctx context.Context, userID string, page, pageSize int32) ([]*Ledger, int64, error) {
	return uc.ledgerRepo.ListLedgers(ctx, userID, page, pageSize)
}

func (uc *BillingUsecase) getGroupRatio(group string) float64 {
	if ratio, ok := uc.groupRatios[group]; ok {
		return ratio
	}
	return 1.0
}

// DefaultGroupRatios returns the default group-to-ratio mapping.
func DefaultGroupRatios() map[string]float64 {
	return map[string]float64{
		"default": 1.0,
		"vip":     0.5,
		"svip":    0.3,
	}
}

func generateReservationID() string {
	return fmt.Sprintf("res_%d_%d", time.Now().UnixNano(), time.Now().Unix())
}

func generateRedeemCode() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// 如果随机数生成失败，使用时间戳作为后备
		return fmt.Sprintf("rc_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}
