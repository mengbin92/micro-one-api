package biz

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

type PricingConfig struct {
	GroupRatios      map[string]float64
	ModelRatios      map[string]float64
	CompletionRatios map[string]float64
	ModelPrices      map[string]ModelPrice
	QuotaPerUnit     float64
	PricingStore     PricingConfigStore
}

type ModelPrice struct {
	InputPrice     float64  `json:"input_price"`
	OutputPrice    float64  `json:"output_price"`
	CacheReadPrice *float64 `json:"cache_read_price,omitempty"`
}

type BillingUsecase struct {
	accountRepo      AccountRepo
	reservationRepo  ReservationRepo
	ledgerRepo       LedgerRepo
	redeemRepo       RedeemRepo
	pricingStore     PricingConfigStore
	groupRatios      map[string]float64
	modelRatios      map[string]float64
	completionRatios map[string]float64
	modelPrices      map[string]ModelPrice
	quotaPerUnit     float64
}

func NewBillingUsecase(
	accountRepo AccountRepo,
	reservationRepo ReservationRepo,
	ledgerRepo LedgerRepo,
	redeemRepo RedeemRepo,
	groupRatios map[string]float64,
) *BillingUsecase {
	return NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		GroupRatios: groupRatios,
	})
}

func NewBillingUsecaseWithPricing(
	accountRepo AccountRepo,
	reservationRepo ReservationRepo,
	ledgerRepo LedgerRepo,
	redeemRepo RedeemRepo,
	pricing PricingConfig,
) *BillingUsecase {
	groupRatios := pricing.GroupRatios
	if len(groupRatios) == 0 {
		groupRatios = DefaultGroupRatios()
	}
	return &BillingUsecase{
		accountRepo:      accountRepo,
		reservationRepo:  reservationRepo,
		ledgerRepo:       ledgerRepo,
		redeemRepo:       redeemRepo,
		pricingStore:     pricing.PricingStore,
		groupRatios:      groupRatios,
		modelRatios:      normalizePositiveRatios(pricing.ModelRatios),
		completionRatios: normalizePositiveRatios(pricing.CompletionRatios),
		modelPrices:      normalizeModelPrices(pricing.ModelPrices),
		quotaPerUnit:     normalizeQuotaPerUnit(pricing.QuotaPerUnit),
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

	cost := uc.calculateCost(ctx, account.Group, model, estimatedTokens, 0, 0)

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
	return uc.CommitQuotaWithUsage(ctx, reservationID, actualTokens, success, LedgerUsage{})
}

type LedgerUsage struct {
	TokenName        string
	Endpoint         string
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	ElapsedTime      int64
	IsStream         bool
}

func (uc *BillingUsecase) CommitQuotaWithUsage(ctx context.Context, reservationID string, actualTokens int64, success bool, usage LedgerUsage) (int64, int64, error) {
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

	actualCost := uc.calculateCostWithUsage(ctx, account.Group, reservation.Model, actualTokens, usage)

	if actualCost <= 0 {
		actualCost = 1
	}

	if success {
		diff := reservation.Amount - actualCost
		balanceAfter := account.Quota

		if err := uc.accountRepo.UpdateFrozenQuota(ctx, reservation.UserID, -reservation.Amount); err != nil {
			return 0, 0, fmt.Errorf("release frozen quota: %w", err)
		}

		if diff > 0 {
			newQuota, err := uc.accountRepo.UpdateQuota(ctx, reservation.UserID, diff, LedgerTypeRefund)
			if err != nil {
				return 0, 0, fmt.Errorf("refund quota: %w", err)
			}
			balanceAfter = newQuota
		} else if diff < 0 {
			newQuota, err := uc.accountRepo.UpdateQuota(ctx, reservation.UserID, diff, LedgerTypeConsume)
			if err != nil {
				return 0, 0, fmt.Errorf("charge additional quota: %w", err)
			}
			balanceAfter = newQuota
		}

		if err := uc.reservationRepo.UpdateReservationStatus(ctx, reservationID, ReservationStatusCommitted); err != nil {
			return 0, 0, fmt.Errorf("update reservation status: %w", err)
		}

		ledger := &Ledger{
			UserID:           reservation.UserID,
			Amount:           -actualCost,
			BalanceAfter:     balanceAfter,
			Type:             LedgerTypeConsume,
			ReferenceID:      reservationID,
			Remark:           fmt.Sprintf("model=%s, tokens=%d", reservation.Model, actualTokens),
			TokenName:        usage.TokenName,
			ModelName:        reservation.Model,
			Quota:            actualTokens,
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			ChannelID:        parseInt64Default(reservation.ChannelID, 0),
			ElapsedTime:      usage.ElapsedTime,
			IsStream:         usage.IsStream,
			Endpoint:         usage.Endpoint,
		}

		if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
			return 0, 0, fmt.Errorf("create ledger: %w", err)
		}

		if err := uc.accountRepo.UpdateUsage(ctx, reservation.UserID, actualCost, 1); err != nil {
			return 0, 0, fmt.Errorf("update usage: %w", err)
		}

		refund := int64(0)
		if diff > 0 {
			refund = diff
		}
		return actualCost, refund, nil
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

func (uc *BillingUsecase) BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*Account, error) {
	return uc.accountRepo.BatchGetAccountSnapshots(ctx, userIDs)
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

func (uc *BillingUsecase) ListLedgersWithTimeRange(ctx context.Context, userID string, page, pageSize int32, startTime, endTime time.Time) ([]*Ledger, int64, error) {
	return uc.ledgerRepo.ListLedgersWithTimeRange(ctx, userID, page, pageSize, startTime, endTime)
}

func (uc *BillingUsecase) ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*Ledger, int64, error) {
	return uc.ledgerRepo.ListLedgersWithFilters(ctx, userID, page, pageSize, ledgerType, startTime, endTime)
}

func (uc *BillingUsecase) getGroupRatio(pricing PricingConfig, group string) float64 {
	if ratio, ok := pricing.GroupRatios[group]; ok {
		return ratio
	}
	return 1.0
}

func (uc *BillingUsecase) getModelRatio(pricing PricingConfig, model string) float64 {
	if ratio, ok := pricing.ModelRatios[model]; ok {
		return ratio
	}
	return 1.0
}

func (uc *BillingUsecase) getCompletionRatio(pricing PricingConfig, model string) float64 {
	if ratio, ok := pricing.CompletionRatios[model]; ok {
		return ratio
	}
	return 1.0
}

func (uc *BillingUsecase) calculateCost(ctx context.Context, group, model string, promptTokens, completionTokens, cacheReadTokens int64) int64 {
	pricing := uc.pricingConfig(ctx)
	prompt := float64(maxInt64(promptTokens, 0))
	completion := float64(maxInt64(completionTokens, 0))
	if price, ok := pricing.ModelPrices[model]; ok {
		cacheRead := float64(minInt64(maxInt64(cacheReadTokens, 0), maxInt64(promptTokens, 0)))
		input := prompt - cacheRead
		cacheReadPrice := price.InputPrice
		if price.CacheReadPrice != nil {
			cacheReadPrice = *price.CacheReadPrice
		}
		cost := (input*price.InputPrice + cacheRead*cacheReadPrice + completion*price.OutputPrice) * normalizeQuotaPerUnit(pricing.QuotaPerUnit) * uc.getGroupRatio(pricing, group)
		if cost <= 0 {
			return 0
		}
		return int64(math.Ceil(cost))
	}
	cost := (prompt + completion*uc.getCompletionRatio(pricing, model)) * uc.getModelRatio(pricing, model) * uc.getGroupRatio(pricing, group)
	if cost <= 0 {
		return 0
	}
	return int64(math.Ceil(cost))
}

func (uc *BillingUsecase) calculateCostWithUsage(ctx context.Context, group, model string, actualTokens int64, usage LedgerUsage) int64 {
	if usage.PromptTokens > 0 || usage.CompletionTokens > 0 || usage.CacheReadTokens > 0 {
		return uc.calculateCost(ctx, group, model, usage.PromptTokens, usage.CompletionTokens, usage.CacheReadTokens)
	}
	return uc.calculateCost(ctx, group, model, actualTokens, 0, 0)
}

func (uc *BillingUsecase) pricingConfig(ctx context.Context) PricingConfig {
	config := PricingConfig{
		GroupRatios:      uc.groupRatios,
		ModelRatios:      uc.modelRatios,
		CompletionRatios: uc.completionRatios,
		ModelPrices:      uc.modelPrices,
		QuotaPerUnit:     uc.quotaPerUnit,
	}
	if uc.pricingStore == nil {
		return config
	}
	dynamic, err := uc.pricingStore.GetPricingConfig(ctx)
	if err != nil {
		return config
	}
	if len(dynamic.GroupRatios) > 0 {
		config.GroupRatios = normalizePositiveRatios(dynamic.GroupRatios)
	}
	if len(dynamic.ModelRatios) > 0 {
		config.ModelRatios = normalizePositiveRatios(dynamic.ModelRatios)
	}
	if len(dynamic.CompletionRatios) > 0 {
		config.CompletionRatios = normalizePositiveRatios(dynamic.CompletionRatios)
	}
	if len(dynamic.ModelPrices) > 0 {
		config.ModelPrices = normalizeModelPrices(dynamic.ModelPrices)
	}
	if dynamic.QuotaPerUnit > 0 {
		config.QuotaPerUnit = normalizeQuotaPerUnit(dynamic.QuotaPerUnit)
	}
	return config
}

func normalizePositiveRatios(input map[string]float64) map[string]float64 {
	if len(input) == 0 {
		return map[string]float64{}
	}
	out := make(map[string]float64, len(input))
	for key, ratio := range input {
		if key != "" && ratio > 0 {
			out[key] = ratio
		}
	}
	return out
}

func normalizeModelPrices(input map[string]ModelPrice) map[string]ModelPrice {
	if len(input) == 0 {
		return map[string]ModelPrice{}
	}
	out := make(map[string]ModelPrice, len(input))
	for model, price := range input {
		if model == "" {
			continue
		}
		if price.InputPrice < 0 {
			price.InputPrice = 0
		}
		if price.OutputPrice < 0 {
			price.OutputPrice = 0
		}
		if price.CacheReadPrice != nil && *price.CacheReadPrice < 0 {
			zero := 0.0
			price.CacheReadPrice = &zero
		}
		if price.InputPrice > 0 || price.OutputPrice > 0 || price.CacheReadPrice != nil {
			out[model] = price
		}
	}
	return out
}

func normalizeQuotaPerUnit(value float64) float64 {
	if value > 0 {
		return value
	}
	return 500000
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
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

func parseInt64Default(value string, fallback int64) int64 {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func generateRedeemCode() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		// 如果随机数生成失败，使用时间戳作为后备
		return fmt.Sprintf("rc_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}
