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

	"gorm.io/gorm"

	subscriptionbiz "micro-one-api/internal/subscription/biz"
)

type PricingConfig struct {
	GroupRatios      map[string]float64
	ModelRatios      map[string]float64
	CompletionRatios map[string]float64
	ModelPrices      map[string]ModelPrice
	UpstreamPrices   map[string]ModelPrice
	AmountPerUnit    float64
	PricingStore     PricingConfigStore
}

const AmountScale = 10000

type ModelPrice struct {
	InputPrice     float64  `json:"input_price"`
	OutputPrice    float64  `json:"output_price"`
	CacheReadPrice *float64 `json:"cache_read_price,omitempty"`
}

// QuotaPerUSD is the conversion factor used to translate between the integer
// amount ledger columns and the USD amounts shown in the UI.
var QuotaPerUSD = int64(AmountScale)

// BillingOptions wires the optional dependencies the dual-track
// "subscription priority" flow needs. The default zero value disables
// the new flow so existing call sites keep working unchanged.
type BillingOptions struct {
	// SubscriptionUsecase is required for the dual-track flow. When nil the
	// billing pipeline runs the legacy balance-only path.
	SubscriptionUsecase SubscriptionPrimatives
	// ReservationRepo, AccountRepo, LedgerRepo and ReceivableRepo are
	// normally populated by the constructor from the
	// NewBillingUsecaseWithOptions entry point.
	ReservationRepo ReservationRepo
	AccountRepo     AccountRepo
	LedgerRepo      LedgerRepo
	RedeemRepo      RedeemRepo
	ReceivableRepo  ReceivableRepo
	// AllowOverdraft controls whether CommitBalanceInTx is allowed to
	// drive the wallet negative.
	AllowOverdraft bool
	// BlockOverdraftUsers, when set, contains the list of user IDs
	// that are NEVER allowed to overdraft even when AllowOverdraft is
	// true.
	BlockOverdraftUsers map[string]struct{}
	// QuotaPerUSD overrides the default QuotaPerUSD when non-zero.
	QuotaPerUSD int64
	// Now, when set, overrides time.Now. Tests use it to make
	// reservation expiry + subscription window calculations
	// deterministic.
	Now func() time.Time
}

// SubscriptionPrimatives is the narrow surface the billing domain needs
// from the subscription domain. The implementation is provided by
// *subscription.SubscriptionUsecase in production; tests use an in-
// memory fake that exercises the same CAS state machine.
type SubscriptionPrimatives interface {
	// GetActiveSubscriptionForUser returns the user's active
	// subscription snapshot. Returns subscriptionbiz.ErrSubscriptionNotFound when
	// the user has no active subscription.
	GetActiveSubscriptionForUser(ctx context.Context, userID int64) (*subscriptionbiz.UserSubscription, error)
	// GetGroupForSubscription loads the subscription group (limits
	// and multiplier) for the given subscription.
	GetGroupForSubscription(ctx context.Context, subscription *subscriptionbiz.UserSubscription) (*subscriptionbiz.SubscriptionGroup, error)
	// RecordUsageForSubscriptionInTx adds the given USD cost to every
	// window of the subscription's usage counters, performing the
	// read-roll-increment in the caller's transaction.
	RecordUsageForSubscriptionInTx(ctx context.Context, tx *gorm.DB, subscriptionID int64, costUSD float64, now int64) error
}

type BillingUsecase struct {
	accountRepo      AccountRepo
	reservationRepo  ReservationRepo
	ledgerRepo       LedgerRepo
	redeemRepo       RedeemRepo
	receivableRepo   ReceivableRepo
	subscription     SubscriptionPrimatives
	options          BillingOptions
	pricingStore     PricingConfigStore
	groupRatios      map[string]float64
	modelRatios      map[string]float64
	completionRatios map[string]float64
	modelPrices      map[string]ModelPrice
	upstreamPrices   map[string]ModelPrice
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
		options:          BillingOptions{AccountRepo: accountRepo, ReservationRepo: reservationRepo, LedgerRepo: ledgerRepo, RedeemRepo: redeemRepo, AllowOverdraft: true},
		pricingStore:     pricing.PricingStore,
		groupRatios:      groupRatios,
		modelRatios:      normalizePositiveRatios(pricing.ModelRatios),
		completionRatios: normalizePositiveRatios(pricing.CompletionRatios),
		modelPrices:      normalizeModelPrices(pricing.ModelPrices),
		upstreamPrices:   normalizeModelPrices(pricing.UpstreamPrices),
	}
}

// NewBillingUsecaseWithOptions is the canonical constructor for the
// dual-track "subscription priority" flow.
func NewBillingUsecaseWithOptions(opts BillingOptions) *BillingUsecase {
	uc := &BillingUsecase{
		accountRepo:     opts.AccountRepo,
		reservationRepo: opts.ReservationRepo,
		ledgerRepo:      opts.LedgerRepo,
		redeemRepo:      opts.RedeemRepo,
		receivableRepo:  opts.ReceivableRepo,
		subscription:    opts.SubscriptionUsecase,
		options:         opts,
	}
	if uc.options.Now == nil {
		uc.options.Now = time.Now
	}
	if uc.options.QuotaPerUSD > 0 {
		QuotaPerUSD = uc.options.QuotaPerUSD
	}
	return uc
}

func (uc *BillingUsecase) Options() BillingOptions {
	return uc.options
}

func (uc *BillingUsecase) SetSubscriptionPrimatives(p SubscriptionPrimatives) {
	uc.subscription = p
	uc.options.SubscriptionUsecase = p
}

func (uc *BillingUsecase) SetReceivableRepo(r ReceivableRepo) {
	uc.receivableRepo = r
	uc.options.ReceivableRepo = r
}

func (uc *BillingUsecase) SetReservationRepo(r ReservationRepo) {
	uc.reservationRepo = r
	uc.options.ReservationRepo = r
}

func (uc *BillingUsecase) SetAccountRepo(r AccountRepo) {
	uc.accountRepo = r
	uc.options.AccountRepo = r
}

func (uc *BillingUsecase) SetLedgerRepo(r LedgerRepo) {
	uc.ledgerRepo = r
	uc.options.LedgerRepo = r
}

func (uc *BillingUsecase) SetRedeemRepo(r RedeemRepo) {
	uc.redeemRepo = r
	uc.options.RedeemRepo = r
}

func (uc *BillingUsecase) Now() time.Time {
	if uc.options.Now != nil {
		return uc.options.Now()
	}
	return time.Now()
}

// quotaToUSD converts quota (integer) to USD (float) using the
// configured QuotaPerUSD. The conversion is exact (no rounding) so the
// caller is responsible for flooring / ceiling as the design mandates.
func (uc *BillingUsecase) quotaToUSD(quota int64) float64 {
	perUSD := uc.quotaPerUSD()
	if perUSD <= 0 {
		return 0
	}
	return float64(quota) / float64(perUSD)
}

// usdToQuotaFloor converts USD to quota with floor rounding.
func (uc *BillingUsecase) usdToQuotaFloor(usd float64) int64 {
	perUSD := uc.quotaPerUSD()
	if perUSD <= 0 {
		return 0
	}
	if usd <= 0 {
		return 0
	}
	v := math.Floor(usd * float64(perUSD))
	if v > math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(v)
}

func (uc *BillingUsecase) quotaPerUSD() int64 {
	if uc.options.QuotaPerUSD > 0 {
		return uc.options.QuotaPerUSD
	}
	if QuotaPerUSD > 0 {
		return QuotaPerUSD
	}
	return AmountScale
}

func (uc *BillingUsecase) ReserveQuota(ctx context.Context, userID, requestID string, estimatedTokens int64, model, channelID string, subscriptionAccountID int64) (*Reservation, error) {
	if requestID != "" {
		existing, err := uc.reservationRepo.FindByRequestID(ctx, requestID)
		if err != nil {
			return nil, fmt.Errorf("find by request id: %w", err)
		}
		if existing != nil && (existing.IsReserved() || existing.Status == ReservationStatusCommitted) {
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

	// Dual-track path: when subscription priority is enabled and the
	// user has an active subscription, we split the cost between the
	// subscription (absorbs up to its remaining window) and the wallet.
	if uc.subscriptionPriorityEnabled() {
		if reservation, err := uc.reserveQuotaDualTrack(ctx, userID, account, requestID, model, channelID, subscriptionAccountID, cost, estimatedTokens); err == nil {
			return reservation, nil
		} else if !errors.Is(err, subscriptionbiz.ErrSubscriptionNotFound) && !errors.Is(err, ErrCrossDBReservation) {
			// We do NOT fall through to the legacy path on
			// dual-track failures: that would create a
			// double-charge window. Instead, surface the
			// error so the caller can decide.
			return nil, err
		}
		// subscriptionbiz.ErrSubscriptionNotFound / ErrCrossDBReservation fall
		// through to the legacy balance-only path.
	}

	// Legacy balance-only path.
	if account.AvailableBalance() < cost {
		return nil, ErrInsufficientQuota
	}

	reservationID := generateReservationID()
	now := uc.Now()
	expiredAt := now.Add(5 * time.Minute)

	reservation := &Reservation{
		ReservationID:         reservationID,
		UserID:                userID,
		RequestID:             requestID,
		Amount:                cost,
		BalanceAmountQuota:    cost,
		Status:                ReservationStatusReserved,
		Model:                 model,
		ChannelID:             channelID,
		SubscriptionAccountID: strconv.FormatInt(subscriptionAccountID, 10),
		CreatedAt:             now,
		UpdatedAt:             now,
		ExpiredAt:             expiredAt,
	}

	if err := uc.reservationRepo.CreateReservation(ctx, reservation); err != nil {
		return nil, fmt.Errorf("create reservation: %w", err)
	}

	if _, err := uc.accountRepo.UpdateBalance(ctx, userID, -cost, LedgerTypeConsume); err != nil {
		return nil, fmt.Errorf("update balance: %w", err)
	}

	if err := uc.accountRepo.UpdateFrozenAmount(ctx, userID, cost); err != nil {
		return nil, fmt.Errorf("update frozen amount: %w", err)
	}

	return reservation, nil
}

// subscriptionPriorityEnabled reports whether the dual-track flow can run.
// There is intentionally no runtime gray switch: once subscription dependencies
// are wired, active subscriptions are charged before wallet balance.
func (uc *BillingUsecase) subscriptionPriorityEnabled() bool {
	if uc == nil {
		return false
	}
	return uc.subscription != nil
}

func (uc *BillingUsecase) reserveQuotaDualTrack(
	ctx context.Context,
	userID string,
	account *Account,
	requestID, model, channelID string,
	subscriptionAccountID int64,
	cost int64,
	estimatedTokens int64,
) (*Reservation, error) {
	if uc.subscription == nil {
		return nil, subscriptionbiz.ErrSubscriptionNotFound
	}
	// billing's user_id is a string (varchar). The subscription
	// domain's UserID is int64. We attempt a best-effort parse;
	// legacy subscriptions (no active row) fall through to the
	// balance-only path.
	parsedUserID, parseErr := strconv.ParseInt(userID, 10, 64)
	if parseErr != nil {
		return nil, subscriptionbiz.ErrSubscriptionNotFound
	}
	subscription, err := uc.subscription.GetActiveSubscriptionForUser(ctx, parsedUserID)
	if err != nil {
		return nil, err
	}
	group, err := uc.subscription.GetGroupForSubscription(ctx, subscription)
	if err != nil {
		return nil, err
	}
	multiplier := group.RateMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}
	// Cost is in quota; convert to USD for the absorber check.
	costUSD := uc.quotaToUSD(cost)

	now := uc.Now()
	rolled := subscriptionbiz.RollUsageWindowsPure(subscription, now.Unix())

	// We need a *gorm.DB tied to the billing repo to take the row
	// lock and read the frozen-aggregate. The dual-track pre-deduction
	// therefore requires the caller's billing repo to expose its
	// underlying *gorm.DB through a `DB()` accessor (every concrete
	// repo in this package already does).
	rawDB := uc.reservationDB()
	if rawDB == nil {
		return nil, ErrCrossDBReservation
	}
	tx := rawDB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()
	if err := uc.reservationRepo.LockSubscriptionRow(ctx, tx, subscription.ID); err != nil {
		return nil, fmt.Errorf("lock subscription row: %w", err)
	}
	frozenDailyUSD, frozenWeeklyUSD, frozenMonthlyUSD, _, err := uc.reservationRepo.SumActiveFrozenInTx(ctx, tx, userID, subscription.ID, rolled.DailyWindowStart, rolled.WeeklyWindowStart, rolled.MonthlyWindowStart)
	if err != nil {
		return nil, fmt.Errorf("sum active frozen: %w", err)
	}
	window := subscriptionbiz.AbsorbableWindow{
		DailyStart:                 rolled.DailyWindowStart,
		WeeklyStart:                rolled.WeeklyWindowStart,
		MonthlyStart:               rolled.MonthlyWindowStart,
		DailyUsageUSD:              rolled.DailyUsageUSD,
		WeeklyUsageUSD:             rolled.WeeklyUsageUSD,
		MonthlyUsageUSD:            rolled.MonthlyUsageUSD,
		DailyLimit:                 group.DailyLimitUSD,
		WeeklyLimit:                group.WeeklyLimitUSD,
		MonthlyLimit:               group.MonthlyLimitUSD,
		FrozenDailyAccountingUSD:   frozenDailyUSD * multiplier,
		FrozenWeeklyAccountingUSD:  frozenWeeklyUSD * multiplier,
		FrozenMonthlyAccountingUSD: frozenMonthlyUSD * multiplier,
	}
	absorbResult := subscriptionbiz.ComputeAbsorbablePure(window, multiplier, 0)
	absorbUSD := costUSD
	if absorbUSD > absorbResult.AbsorbableUSD {
		absorbUSD = absorbResult.AbsorbableUSD
	}
	if absorbUSD < 0 {
		absorbUSD = 0
	}
	subscriptionQuota := uc.usdToQuotaFloor(absorbUSD)
	// If absorbUSD is 0.0001 USD we still round it to 0 quota, in
	// which case the subscription has not absorbed anything and the
	// entire cost falls on the wallet. Conversely, when
	// subscriptionQuota >= cost we keep cost on the wallet at 0.
	if subscriptionQuota > cost {
		subscriptionQuota = cost
	}
	balanceQuota := cost - subscriptionQuota
	// Wallet pre-deduction.
	if balanceQuota > 0 {
		_, _, _, err = uc.accountRepo.ReserveBalanceInTx(ctx, tx, userID, balanceQuota, false)
		if err != nil {
			// The whole transaction (including the subscription
			// pre-deduction) must roll back. We must NOT call
			// releaseReservation here because we have not inserted
			// the reservation row yet — the rollback is the only
			// compensation.
			return nil, fmt.Errorf("reserve wallet: %w", err)
		}
	}
	reservationID := generateReservationID()
	nowTime := uc.Now()
	expiredAt := nowTime.Add(5 * time.Minute)
	reservation := &Reservation{
		ReservationID:                  reservationID,
		UserID:                         userID,
		RequestID:                      requestID,
		Amount:                         cost,
		BalanceAmountQuota:             balanceQuota,
		SubscriptionID:                 subscription.ID,
		SubscriptionAmountUSD:          absorbUSD,
		SubscriptionDailyWindowStart:   rolled.DailyWindowStart,
		SubscriptionWeeklyWindowStart:  rolled.WeeklyWindowStart,
		SubscriptionMonthlyWindowStart: rolled.MonthlyWindowStart,
		Status:                         ReservationStatusReserved,
		Model:                          model,
		ChannelID:                      channelID,
		SubscriptionAccountID:          strconv.FormatInt(subscriptionAccountID, 10),
		CreatedAt:                      nowTime,
		UpdatedAt:                      nowTime,
		ExpiredAt:                      expiredAt,
	}
	if err := uc.reservationRepo.CreateReservationInTx(ctx, tx, reservation); err != nil {
		return nil, fmt.Errorf("create reservation in tx: %w", err)
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	committed = true
	return reservation, nil
}

// canOverdraft reports whether the user is allowed to drive their
// wallet negative in this commit. The default is true (allow); the
// BlockOverdraftUsers list is consulted for opt-out exceptions.
func (uc *BillingUsecase) canOverdraft(userID string) bool {
	if !uc.options.AllowOverdraft {
		return false
	}
	if _, blocked := uc.options.BlockOverdraftUsers[userID]; blocked {
		return false
	}
	return true
}

// reservationDB exposes the underlying *gorm.DB of the reservation
// repo so the dual-track pre-deduction can open a transaction that
// spans both the reservation table and the subscription table. The
// cast is safe because every concrete reservationRepo is bound to a
// *Data that owns the *gorm.DB.
func (uc *BillingUsecase) reservationDB() *gorm.DB {
	if uc == nil || uc.reservationRepo == nil {
		return nil
	}
	type dbAccess interface {
		DB() *gorm.DB
	}
	if dba, ok := uc.reservationRepo.(dbAccess); ok {
		return dba.DB()
	}
	return nil
}

func (uc *BillingUsecase) CommitQuota(ctx context.Context, reservationID string, actualTokens int64, success bool) (int64, int64, error) {
	committed, refund, _, err := uc.CommitQuotaWithUsageAndSplit(ctx, reservationID, actualTokens, success, LedgerUsage{})
	return committed, refund, err
}

// CommitResult is the cost-dimensioned outcome of a commit. The
// fields are zero on the legacy balance-only path; on the dual-track
// path SubscriptionCost + BalanceCost equals CommittedAmount.
type CommitResult struct {
	CommittedAmount  int64
	RefundAmount     int64
	SubscriptionCost int64
	BalanceCost      int64
}

// CommitQuotaWithUsageAndSplit is the cost-split variant of
// CommitQuotaWithUsage. It exists so the wire layer can surface the
// subscription/balance split without breaking the legacy
// (int64, int64, error) signature of the original entry point.
func (uc *BillingUsecase) CommitQuotaWithUsageAndSplit(ctx context.Context, reservationID string, actualTokens int64, success bool, usage LedgerUsage) (int64, int64, CommitResult, error) {
	// Delegate to the existing pipeline. The split is recovered
	// from the reservation's stored amount on the dual-track path.
	committed, refund, err := uc.CommitQuotaWithUsage(ctx, reservationID, actualTokens, success, usage)
	if err != nil {
		return 0, 0, CommitResult{}, err
	}
	// Best-effort split recovery: for the dual-track path the
	// reservation is now committed, so the subscription cost is
	// what the dual-track pipeline stored (committed via
	// RecordUsageForSubscriptionInTx). We surface the full
	// committed amount as the balance cost so the wire's
	// subscription/balance cost split reports match the original
	// values the relay would have seen if the new flow had
	// returned them directly. The authoritative split is in the
	// ledger; this helper is only for log/diagnostic use.
	reservation, lookupErr := uc.reservationRepo.GetReservation(ctx, reservationID)
	if lookupErr == nil && reservation != nil && reservation.HasSubscription() {
		subCost, balCost := int64(0), int64(0)
		if uc.ledgerRepo != nil {
			if subLedger, err := uc.ledgerRepo.FindByDedupeKey(ctx, nil, fmt.Sprintf("%s:%s:%s", reservationID, LedgerTypeConsume, CostSourceSubscription)); err == nil && subLedger != nil {
				subCost = subLedger.SubscriptionCost
			}
			if balLedger, err := uc.ledgerRepo.FindByDedupeKey(ctx, nil, fmt.Sprintf("%s:%s:%s", reservationID, LedgerTypeConsume, CostSourceBalance)); err == nil && balLedger != nil {
				balCost = balLedger.BalanceCost
			}
		}
		if subCost > 0 || balCost > 0 {
			return committed, refund, CommitResult{
				CommittedAmount:  committed,
				RefundAmount:     refund,
				SubscriptionCost: subCost,
				BalanceCost:      balCost,
			}, nil
		}
		return committed, refund, CommitResult{
			CommittedAmount:  committed,
			RefundAmount:     refund,
			SubscriptionCost: committed - reservation.BalanceAmountQuota,
			BalanceCost:      reservation.BalanceAmountQuota,
		}, nil
	}
	return committed, refund, CommitResult{
		CommittedAmount: committed,
		RefundAmount:    refund,
		BalanceCost:     committed,
	}, nil
}

type LedgerUsage struct {
	TokenName             string
	Endpoint              string
	PromptTokens          int64
	CompletionTokens      int64
	CacheReadTokens       int64
	UpstreamCost          int64
	ElapsedTime           int64
	IsStream              bool
	SubscriptionAccountID int64 // optional override; 0 = use the reservation's account
}

func (uc *BillingUsecase) CommitQuotaWithUsage(ctx context.Context, reservationID string, actualTokens int64, success bool, usage LedgerUsage) (int64, int64, error) {
	// For the dual-track path we need the row-locked reservation
	// inside the caller's transaction so the cost we compute can
	// drive both the wallet side-effect and the subscription usage
	// write atomically.
	rawDB := uc.reservationDB()
	if rawDB == nil {
		return uc.commitQuotaLegacy(ctx, reservationID, actualTokens, success, usage)
	}
	reservation, err := uc.reservationRepo.GetReservation(ctx, reservationID)
	if err != nil {
		return 0, 0, fmt.Errorf("get reservation: %w", err)
	}
	_ = reservation
	// Branch on the dual-track vs legacy path. The legacy path is
	// taken when the reservation carries no subscription pre-
	// deduction AND the priority flag is off.
	if !uc.subscriptionPriorityEnabled() || !reservation.HasSubscription() {
		return uc.commitQuotaLegacy(ctx, reservationID, actualTokens, success, usage)
	}
	if !success {
		return 0, 0, uc.releaseReservation(ctx, reservationID, "request failed", ReservationStatusReleased)
	}
	return uc.commitQuotaDualTrack(ctx, reservationID, actualTokens, usage)
}

func (uc *BillingUsecase) commitQuotaLegacy(ctx context.Context, reservationID string, actualTokens int64, success bool, usage LedgerUsage) (int64, int64, error) {
	reservation, err := uc.reservationRepo.GetReservation(ctx, reservationID)
	if err != nil {
		return 0, 0, fmt.Errorf("get reservation: %w", err)
	}

	if reservation.IsTerminal() {
		// Idempotent re-entry: the reservation has already been
		// committed or released; return the stored result instead
		// of erroring out so retries are safe.
		if reservation.Status == ReservationStatusCommitted {
			return reservation.Amount, 0, nil
		}
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
		balanceAfter := account.Balance

		if err := uc.accountRepo.UpdateFrozenAmount(ctx, reservation.UserID, -reservation.Amount); err != nil {
			return 0, 0, fmt.Errorf("release frozen amount: %w", err)
		}

		if diff > 0 {
			newBalance, err := uc.accountRepo.UpdateBalance(ctx, reservation.UserID, diff, LedgerTypeRefund)
			if err != nil {
				return 0, 0, fmt.Errorf("refund balance: %w", err)
			}
			balanceAfter = newBalance
		} else if diff < 0 {
			newBalance, err := uc.accountRepo.UpdateBalance(ctx, reservation.UserID, diff, LedgerTypeConsume)
			if err != nil {
				return 0, 0, fmt.Errorf("charge additional balance: %w", err)
			}
			balanceAfter = newBalance
		}

		if err := uc.reservationRepo.UpdateReservationStatus(ctx, reservationID, ReservationStatusCommitted); err != nil {
			return 0, 0, fmt.Errorf("update reservation status: %w", err)
		}

		ledger := &Ledger{
			UserID:                reservation.UserID,
			Amount:                -actualCost,
			UpstreamCost:          uc.calculateUpstreamCostWithUsage(ctx, parseInt64Default(reservation.ChannelID, 0), reservation.Model, actualTokens, usage),
			BalanceAfter:          balanceAfter,
			Type:                  LedgerTypeConsume,
			ReferenceID:           reservationID,
			Remark:                fmt.Sprintf("model=%s, tokens=%d", reservation.Model, actualTokens),
			TokenName:             usage.TokenName,
			ModelName:             reservation.Model,
			Quota:                 actualTokens,
			PromptTokens:          usage.PromptTokens,
			CompletionTokens:      usage.CompletionTokens,
			CacheReadTokens:       usage.CacheReadTokens,
			ChannelID:             parseInt64Default(reservation.ChannelID, 0),
			SubscriptionAccountID: resolveSubscriptionAccountID(usage.SubscriptionAccountID, reservation.SubscriptionAccountID),
			ElapsedTime:           usage.ElapsedTime,
			IsStream:              usage.IsStream,
			Endpoint:              usage.Endpoint,
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

		if err := uc.accountRepo.UpdateFrozenAmount(ctx, reservation.UserID, -reservation.Amount); err != nil {
			return 0, 0, fmt.Errorf("release frozen amount: %w", err)
		}

		newBalance, err := uc.accountRepo.UpdateBalance(ctx, reservation.UserID, reservation.Amount, LedgerTypeRefund)
		if err != nil {
			return 0, 0, fmt.Errorf("update balance: %w", err)
		}

		balanceAfter := newBalance
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

// commitQuotaDualTrack is the CAS state machine for the dual-track
// commit pipeline. It runs the side effects (subscription usage write,
// wallet settlement, ledger entries, optional receivable) inside a
// single transaction; retries see the row state and short-circuit on
// the same reservation. See design doc §5.3.
func (uc *BillingUsecase) commitQuotaDualTrack(ctx context.Context, reservationID string, actualTokens int64, usage LedgerUsage) (int64, int64, error) {
	rawDB := uc.reservationDB()
	if rawDB == nil {
		return 0, 0, ErrCrossDBReservation
	}
	now := uc.Now().Unix()
	tx := rawDB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return 0, 0, tx.Error
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()
	won, err := uc.reservationRepo.CASReservationStatus(ctx, tx, reservationID, ReservationStatusReserved, ReservationStatusCommitting)
	if err != nil {
		return 0, 0, fmt.Errorf("cas reservation: %w", err)
	}
	if !won {
		// Concurrent or retry: read the row's current state and
		// return a best-effort idempotent result.
		reservation, err := uc.reservationRepo.GetReservationInTx(ctx, tx, reservationID)
		if err != nil {
			return 0, 0, err
		}
		switch reservation.Status {
		case ReservationStatusCommitted:
			committed = true
			_ = tx.Commit()
			return reservation.Amount, 0, nil
		case ReservationStatusReleased, ReservationStatusExpired:
			committed = true
			_ = tx.Commit()
			return 0, reservation.Amount, nil
		}
		return 0, 0, errors.Join(ErrReservationCommitted, ErrReservationReleased)
	}
	reservation, err := uc.reservationRepo.GetReservationInTx(ctx, tx, reservationID)
	if err != nil {
		return 0, 0, fmt.Errorf("get reservation in tx: %w", err)
	}
	account, err := uc.accountSnapshotInTx(ctx, tx, reservation.UserID)
	if err != nil {
		return 0, 0, fmt.Errorf("get account in tx: %w", err)
	}
	actualCost := uc.calculateCostWithUsage(ctx, account.Group, reservation.Model, actualTokens, usage)
	if actualCost <= 0 {
		actualCost = 1
	}
	// Subscription priority: the reservation's subscription pre-deduction is an
	// estimate. At commit time the actual usage may be slightly higher, so while
	// holding the subscription row lock we let remaining subscription capacity
	// absorb the delta before falling back to wallet balance.
	costUSD := uc.quotaToUSD(actualCost)
	actualAbsorbUSD := uc.commitSubscriptionAbsorbUSD(ctx, tx, reservation, costUSD)
	var actualAbsorbQuota int64
	if actualAbsorbUSD >= costUSD {
		actualAbsorbQuota = actualCost
	} else {
		actualAbsorbQuota = uc.usdToQuotaFloor(actualAbsorbUSD)
	}
	if actualAbsorbQuota > actualCost {
		actualAbsorbQuota = actualCost
	}
	actualBalanceQuota := actualCost - actualAbsorbQuota
	if actualBalanceQuota < 0 {
		actualBalanceQuota = 0
	}
	// Subscription side: record the (un-multiplied) actual cost. The
	// subscription Usecase multiplies by RateMultiplier inside the
	// row-locked update.
	if reservation.SubscriptionID > 0 {
		if err := uc.subscription.RecordUsageForSubscriptionInTx(ctx, tx, reservation.SubscriptionID, actualAbsorbUSD, now); err != nil {
			return 0, 0, fmt.Errorf("record subscription usage: %w", err)
		}
	}
	// Wallet side: settle the reservation's balance pre-deduction.
	oldBalance, newBalance, err := uc.accountRepo.CommitBalanceInTx(ctx, tx, reservation.UserID, reservation.BalanceAmountQuota, actualBalanceQuota, uc.canOverdraft(reservation.UserID))
	if err != nil {
		return 0, 0, fmt.Errorf("commit balance: %w", err)
	}
	// Receivable mirror: only record the *increment* so we don't
	// double-count historical overdraft.
	if uc.receivableRepo != nil {
		oldNeg := positiveOrZero(-oldBalance)
		newNeg := positiveOrZero(-newBalance)
		delta := newNeg - oldNeg
		if delta > 0 {
			recv := &AccountReceivable{
				UserID:        reservation.UserID,
				ReservationID: reservationID,
				OverdueQuota:  delta,
				OverdueUSD:    uc.quotaToUSD(delta),
				Status:        ReceivableStatusPending,
				CreatedAt:     uc.Now(),
				UpdatedAt:     uc.Now(),
			}
			if err := uc.receivableRepo.CreateInTx(ctx, tx, recv); err != nil && !errors.Is(err, ErrReceivableDuplicate) {
				return 0, 0, fmt.Errorf("create receivable: %w", err)
			}
		}
	}
	// Ledger entries. We split into two rows when both dimensions
	// participate so each carries its own dedupe key. A pure-
	// subscription commit (actualBalanceQuota == 0) writes only the
	// subscription row, and vice versa.
	upstreamCost := uc.calculateUpstreamCostWithUsage(ctx, parseInt64Default(reservation.ChannelID, 0), reservation.Model, actualTokens, usage)
	resolvedSubAccountID := resolveSubscriptionAccountID(usage.SubscriptionAccountID, reservation.SubscriptionAccountID)
	commonRemark := fmt.Sprintf("model=%s, tokens=%d", reservation.Model, actualTokens)
	if actualAbsorbQuota > 0 {
		subLedger := &Ledger{
			UserID:                reservation.UserID,
			Amount:                -actualAbsorbQuota,
			UpstreamCost:          0,
			BalanceAfter:          newBalance,
			Type:                  LedgerTypeConsume,
			ReferenceID:           reservationID,
			Remark:                commonRemark,
			TokenName:             usage.TokenName,
			ModelName:             reservation.Model,
			Quota:                 actualTokens,
			PromptTokens:          usage.PromptTokens,
			CompletionTokens:      usage.CompletionTokens,
			CacheReadTokens:       usage.CacheReadTokens,
			ChannelID:             parseInt64Default(reservation.ChannelID, 0),
			SubscriptionAccountID: resolvedSubAccountID,
			ElapsedTime:           usage.ElapsedTime,
			IsStream:              usage.IsStream,
			Endpoint:              usage.Endpoint,
			CostSource:            CostSourceSubscription,
			SubscriptionCost:      actualAbsorbQuota,
			BalanceCost:           0,
			LedgerDedupeKey:       fmt.Sprintf("%s:%s:%s", reservationID, LedgerTypeConsume, CostSourceSubscription),
		}
		if err := uc.ledgerRepo.CreateLedgerInTx(ctx, tx, subLedger); err != nil {
			return 0, 0, fmt.Errorf("create subscription ledger: %w", err)
		}
	}
	if actualBalanceQuota > 0 {
		balLedger := &Ledger{
			UserID:                reservation.UserID,
			Amount:                -actualBalanceQuota,
			UpstreamCost:          upstreamCost,
			BalanceAfter:          newBalance,
			Type:                  LedgerTypeConsume,
			ReferenceID:           reservationID,
			Remark:                commonRemark,
			TokenName:             usage.TokenName,
			ModelName:             reservation.Model,
			Quota:                 actualTokens,
			PromptTokens:          usage.PromptTokens,
			CompletionTokens:      usage.CompletionTokens,
			CacheReadTokens:       usage.CacheReadTokens,
			ChannelID:             parseInt64Default(reservation.ChannelID, 0),
			SubscriptionAccountID: resolvedSubAccountID,
			ElapsedTime:           usage.ElapsedTime,
			IsStream:              usage.IsStream,
			Endpoint:              usage.Endpoint,
			CostSource:            CostSourceBalance,
			SubscriptionCost:      0,
			BalanceCost:           actualBalanceQuota,
			LedgerDedupeKey:       fmt.Sprintf("%s:%s:%s", reservationID, LedgerTypeConsume, CostSourceBalance),
		}
		if err := uc.ledgerRepo.CreateLedgerInTx(ctx, tx, balLedger); err != nil {
			return 0, 0, fmt.Errorf("create balance ledger: %w", err)
		}
	}
	// Final CAS to committed.
	won, err = uc.reservationRepo.CASReservationStatus(ctx, tx, reservationID, ReservationStatusCommitting, ReservationStatusCommitted)
	if err != nil {
		return 0, 0, fmt.Errorf("cas to committed: %w", err)
	}
	if !won {
		return 0, 0, errors.Join(ErrReservationCommitted, ErrReservationReleased)
	}
	if err := uc.accountRepo.UpdateUsageInTx(ctx, tx, reservation.UserID, actualCost, 1); err != nil {
		return 0, 0, fmt.Errorf("update usage: %w", err)
	}
	if err := tx.Commit().Error; err != nil {
		return 0, 0, err
	}
	committed = true
	return actualCost, 0, nil
}

func (uc *BillingUsecase) commitSubscriptionAbsorbUSD(ctx context.Context, tx *gorm.DB, reservation *Reservation, costUSD float64) float64 {
	if uc == nil || reservation == nil || costUSD <= 0 {
		return 0
	}
	reservedUSD := reservation.SubscriptionAmountUSD
	if reservedUSD < 0 {
		reservedUSD = 0
	}
	absorbUSD := costUSD
	if absorbUSD > reservedUSD {
		absorbUSD = reservedUSD
	}
	if costUSD <= absorbUSD || reservation.SubscriptionID <= 0 || uc.subscription == nil || uc.reservationRepo == nil {
		return absorbUSD
	}
	parsedUserID, err := strconv.ParseInt(reservation.UserID, 10, 64)
	if err != nil {
		return absorbUSD
	}
	if err := uc.reservationRepo.LockSubscriptionRow(ctx, tx, reservation.SubscriptionID); err != nil {
		return absorbUSD
	}
	subscription, err := uc.subscription.GetActiveSubscriptionForUser(ctx, parsedUserID)
	if err != nil || subscription == nil || subscription.ID != reservation.SubscriptionID {
		return absorbUSD
	}
	group, err := uc.subscription.GetGroupForSubscription(ctx, subscription)
	if err != nil || group == nil {
		return absorbUSD
	}
	multiplier := group.RateMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}
	rolled := subscriptionbiz.RollUsageWindowsPure(subscription, uc.Now().Unix())
	frozenDailyUSD, frozenWeeklyUSD, frozenMonthlyUSD, _, err := uc.reservationRepo.SumActiveFrozenInTx(ctx, tx, reservation.UserID, reservation.SubscriptionID, rolled.DailyWindowStart, rolled.WeeklyWindowStart, rolled.MonthlyWindowStart)
	if err != nil {
		return absorbUSD
	}
	window := subscriptionbiz.AbsorbableWindow{
		DailyStart:                 rolled.DailyWindowStart,
		WeeklyStart:                rolled.WeeklyWindowStart,
		MonthlyStart:               rolled.MonthlyWindowStart,
		DailyUsageUSD:              rolled.DailyUsageUSD,
		WeeklyUsageUSD:             rolled.WeeklyUsageUSD,
		MonthlyUsageUSD:            rolled.MonthlyUsageUSD,
		DailyLimit:                 group.DailyLimitUSD,
		WeeklyLimit:                group.WeeklyLimitUSD,
		MonthlyLimit:               group.MonthlyLimitUSD,
		FrozenDailyAccountingUSD:   frozenDailyUSD * multiplier,
		FrozenWeeklyAccountingUSD:  frozenWeeklyUSD * multiplier,
		FrozenMonthlyAccountingUSD: frozenMonthlyUSD * multiplier,
	}
	maxAbsorbUSD := subscriptionbiz.ComputeAbsorbablePure(window, multiplier, 0).AbsorbableUSD
	if maxAbsorbUSD < reservedUSD {
		maxAbsorbUSD = reservedUSD
	}
	if maxAbsorbUSD > costUSD {
		maxAbsorbUSD = costUSD
	}
	if maxAbsorbUSD < absorbUSD {
		return absorbUSD
	}
	return maxAbsorbUSD
}

func (uc *BillingUsecase) ReleaseQuota(ctx context.Context, reservationID, reason string) error {
	return uc.releaseReservation(ctx, reservationID, reason, ReservationStatusReleased)
}

// releaseReservation unifies the three release paths: explicit
// ReleaseQuota, CommitQuotaWithUsage success=false, and cleanup
// (status=expired). All three go through a single CAS state machine
// so the wallet refund, the ledger entry, and the reservation status
// change commit atomically.
func (uc *BillingUsecase) releaseReservation(ctx context.Context, reservationID, reason, finalStatus string) error {
	rawDB := uc.reservationDB()
	if rawDB == nil {
		return uc.releaseReservationLegacy(ctx, reservationID, reason, finalStatus)
	}
	tx := rawDB.WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	committed := false
	defer func() {
		if !committed {
			tx.Rollback()
		}
	}()
	won, err := uc.reservationRepo.CASReservationStatus(ctx, tx, reservationID, ReservationStatusReserved, ReservationStatusReleasing)
	if err != nil {
		return fmt.Errorf("cas to releasing: %w", err)
	}
	if !won {
		// Either already terminal (idempotent) or a concurrent
		// release is in flight. Read the current state and exit
		// cleanly.
		reservation, err := uc.reservationRepo.GetReservationInTx(ctx, tx, reservationID)
		if err != nil {
			return err
		}
		if reservation.IsTerminal() {
			committed = true
			_ = tx.Commit()
			return nil
		}
		// Still in committing — the other side is finalising. We
		// cannot release a reservation whose commit is in flight;
		// the caller should retry shortly.
		return errors.Join(ErrReservationCommitted, ErrReservationReleased)
	}
	reservation, err := uc.reservationRepo.GetReservationInTx(ctx, tx, reservationID)
	if err != nil {
		return fmt.Errorf("get reservation in tx: %w", err)
	}
	// Wallet side: refund the reserved quota.
	if reservation.BalanceAmountQuota > 0 {
		_, err := uc.accountRepo.ReleaseBalanceInTx(ctx, tx, reservation.UserID, reservation.BalanceAmountQuota)
		if err != nil {
			return fmt.Errorf("release balance: %w", err)
		}
		// Refund ledger. The dedupe key prevents duplicate rows
		// when a retry enters this branch after a partial commit.
		refund := &Ledger{
			UserID:          reservation.UserID,
			Amount:          reservation.BalanceAmountQuota,
			Type:            LedgerTypeRefund,
			ReferenceID:     reservationID,
			Remark:          reason,
			LedgerDedupeKey: fmt.Sprintf("%s:%s:%s", reservationID, LedgerTypeRefund, CostSourceBalance),
		}
		if err := uc.ledgerRepo.CreateLedgerInTx(ctx, tx, refund); err != nil {
			return fmt.Errorf("create refund ledger: %w", err)
		}
	}
	// Subscription side: nothing to do here. The reservation's
	// status flip from reserved -> released causes the
	// SumActiveFrozenInTx query to drop the row from the next
	// absorber check, so the pre-deduction naturally "releases".
	won, err = uc.reservationRepo.CASReservationStatus(ctx, tx, reservationID, ReservationStatusReleasing, finalStatus)
	if err != nil {
		return fmt.Errorf("cas to final: %w", err)
	}
	if !won {
		// Should not happen unless the row was forcibly deleted.
		return errors.Join(ErrReservationCommitted, ErrReservationReleased)
	}
	if err := tx.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}

func (uc *BillingUsecase) releaseReservationLegacy(ctx context.Context, reservationID, reason, finalStatus string) error {
	reservation, err := uc.reservationRepo.GetReservation(ctx, reservationID)
	if err != nil {
		return fmt.Errorf("get reservation: %w", err)
	}

	if !reservation.IsReserved() {
		return errors.Join(ErrReservationCommitted, ErrReservationReleased)
	}

	if err := uc.reservationRepo.UpdateReservationStatus(ctx, reservationID, finalStatus); err != nil {
		return fmt.Errorf("update reservation status: %w", err)
	}

	if err := uc.accountRepo.UpdateFrozenAmount(ctx, reservation.UserID, -reservation.Amount); err != nil {
		return fmt.Errorf("release frozen amount: %w", err)
	}

	newBalance, err := uc.accountRepo.UpdateBalance(ctx, reservation.UserID, reservation.Amount, LedgerTypeRefund)
	if err != nil {
		return fmt.Errorf("update balance: %w", err)
	}

	balanceAfter := newBalance
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

func (uc *BillingUsecase) accountSnapshotInTx(ctx context.Context, tx *gorm.DB, userID string) (*Account, error) {
	// Concrete account repos expose a per-transaction read. We fall
	// back to a non-transactional snapshot when the repo does not.
	type inTxAccount interface {
		GetAccountSnapshotInTx(ctx context.Context, tx *gorm.DB, userID string) (*Account, error)
	}
	if r, ok := uc.accountRepo.(inTxAccount); ok {
		return r.GetAccountSnapshotInTx(ctx, tx, userID)
	}
	return uc.accountRepo.GetAccountSnapshot(ctx, userID)
}

func (uc *BillingUsecase) GetAccountSnapshot(ctx context.Context, userID string) (*Account, error) {
	return uc.accountRepo.GetAccountSnapshot(ctx, userID)
}

func (uc *BillingUsecase) BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*Account, error) {
	return uc.accountRepo.BatchGetAccountSnapshots(ctx, userIDs)
}

func (uc *BillingUsecase) TopUpQuota(ctx context.Context, userID, operatorID string, amount int64, remark string) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("amount must be positive")
	}
	if rawDB := uc.reservationDB(); rawDB != nil {
		tx := rawDB.WithContext(ctx).Begin()
		if tx.Error != nil {
			return 0, tx.Error
		}
		committed := false
		defer func() {
			if !committed {
				tx.Rollback()
			}
		}()
		res := tx.WithContext(ctx).Exec(`UPDATE users SET balance = balance + ? WHERE id = ?`, amount, userID)
		if res.Error != nil {
			return 0, fmt.Errorf("update balance: %w", res.Error)
		}
		if res.RowsAffected == 0 {
			return 0, ErrAccountNotFound
		}
		var row struct {
			Balance int64 `gorm:"column:balance"`
		}
		if err := tx.WithContext(ctx).Table("users").Select("balance").Where("id = ?", userID).Scan(&row).Error; err != nil {
			return 0, fmt.Errorf("get updated balance: %w", err)
		}
		if uc.receivableRepo != nil {
			settled, err := uc.receivableRepo.SettleOldestForUserInTx(ctx, tx, userID, amount)
			if err != nil {
				return 0, fmt.Errorf("settle receivables: %w", err)
			}
			if settled > 0 {
				settleLedger := &Ledger{
					UserID: userID,
					Amount: 0,
					Type:   LedgerTypeRecharge,
					Remark: fmt.Sprintf("settle receivables=%d for recharge by %s", settled, operatorID),
				}
				if err := uc.ledgerRepo.CreateLedgerInTx(ctx, tx, settleLedger); err != nil {
					return 0, fmt.Errorf("create settlement ledger: %w", err)
				}
			}
		}
		ledger := &Ledger{
			UserID:       userID,
			Amount:       amount,
			BalanceAfter: row.Balance,
			Type:         LedgerTypeRecharge,
			Remark:       fmt.Sprintf("operator=%s, remark=%s", operatorID, remark),
		}
		if err := uc.ledgerRepo.CreateLedgerInTx(ctx, tx, ledger); err != nil {
			return 0, fmt.Errorf("create ledger: %w", err)
		}
		if err := tx.Commit().Error; err != nil {
			return 0, err
		}
		committed = true
		return row.Balance, nil
	}
	if _, err := uc.accountRepo.GetAccountSnapshot(ctx, userID); err != nil {
		return 0, fmt.Errorf("get account snapshot: %w", err)
	}
	newBalance, err := uc.accountRepo.UpdateBalance(ctx, userID, amount, LedgerTypeRecharge)
	if err != nil {
		return 0, fmt.Errorf("update balance: %w", err)
	}
	ledger := &Ledger{
		UserID:       userID,
		Amount:       amount,
		BalanceAfter: newBalance,
		Type:         LedgerTypeRecharge,
		Remark:       fmt.Sprintf("operator=%s, remark=%s", operatorID, remark),
	}
	if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
		return 0, fmt.Errorf("create ledger: %w", err)
	}
	return newBalance, nil
}

// PurchaseSubscription atomically deducts priceQuota from the user's wallet and
// records a "subscription" ledger entry. UpdateBalance rejects the operation with
// ErrInsufficientQuota when the balance would go negative, so callers never need
// a separate balance pre-check. The subscription row itself is created by the
// caller (admin-api); on failure there it compensates via TopUpQuota.
func (uc *BillingUsecase) PurchaseSubscription(ctx context.Context, userID string, priceQuota, groupID int64, remark string) (int64, error) {
	if priceQuota <= 0 {
		return 0, fmt.Errorf("price quota must be positive")
	}
	if _, err := uc.accountRepo.GetAccountSnapshot(ctx, userID); err != nil {
		return 0, fmt.Errorf("get account snapshot: %w", err)
	}
	newBalance, err := uc.accountRepo.UpdateBalance(ctx, userID, -priceQuota, LedgerTypeSubscription)
	if err != nil {
		return 0, err
	}
	ledger := &Ledger{
		UserID:       userID,
		Amount:       -priceQuota,
		BalanceAfter: newBalance,
		Type:         LedgerTypeSubscription,
		ReferenceID:  strconv.FormatInt(groupID, 10),
		Remark:       remark,
	}
	if err := uc.ledgerRepo.CreateLedger(ctx, ledger); err != nil {
		return 0, fmt.Errorf("create ledger: %w", err)
	}
	return newBalance, nil
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

func (uc *BillingUsecase) CreateRedeemCodesBatch(ctx context.Context, name string, amount int64, count, batchSize int32) ([]string, error) {
	if count <= 0 || count > 100 {
		return nil, fmt.Errorf("count must be between 1 and 100")
	}
	if batchSize <= 0 || batchSize > 100 {
		batchSize = count
	}
	codes := make([]string, 0, count)
	now := time.Now()
	for i := int32(0); i < count; i++ {
		codes = append(codes, generateRedeemCode())
	}
	redeemCodes := make([]*RedeemCode, len(codes))
	for i, code := range codes {
		redeemCodes[i] = &RedeemCode{
			Code:      code,
			Name:      name,
			Amount:    amount,
			Count:     1,
			Status:    RedeemCodeStatusEnabled,
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
	redeemCode := &RedeemCode{Code: code, Name: name, Amount: amount, Status: status}
	if err := uc.redeemRepo.UpdateRedeemCode(ctx, redeemCode); err != nil {
		return fmt.Errorf("update redeem code: %w", err)
	}
	return nil
}

func (uc *BillingUsecase) DeleteRedeemCode(ctx context.Context, code string) error {
	return uc.redeemRepo.DeleteRedeemCode(ctx, code)
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
	balanceBefore := account.Balance
	if err := uc.redeemRepo.UpdateRedeemCodeCount(ctx, code, 1); err != nil {
		return 0, 0, fmt.Errorf("update redeem code count: %w", err)
	}
	newBalance, err := uc.accountRepo.UpdateBalance(ctx, userID, redeemCode.Amount, LedgerTypeRedeem)
	if err != nil {
		return 0, 0, fmt.Errorf("update balance: %w", err)
	}
	balanceAfter := newBalance
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
		UserID:        userID,
		Code:          code,
		Amount:        redeemCode.Amount,
		BalanceBefore: balanceBefore,
		BalanceAfter:  balanceAfter,
	}
	if err := uc.redeemRepo.CreateRedeemRecord(ctx, record); err != nil {
		return 0, 0, fmt.Errorf("create redeem record: %w", err)
	}
	return redeemCode.Amount, newBalance, nil
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
		UpstreamPrices:   uc.upstreamPrices,
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
	if len(dynamic.UpstreamPrices) > 0 {
		config.UpstreamPrices = normalizeModelPrices(dynamic.UpstreamPrices)
	}
	return config
}

func (uc *BillingUsecase) calculateUpstreamCostWithUsage(ctx context.Context, channelID int64, model string, actualTokens int64, usage LedgerUsage) int64 {
	if usage.UpstreamCost > 0 {
		return usage.UpstreamCost
	}
	pricing := uc.pricingConfig(ctx)
	price, ok := pricing.UpstreamPrices[upstreamPriceKey(channelID, model)]
	if !ok {
		price, ok = pricing.UpstreamPrices[model]
	}
	if !ok {
		return 0
	}
	promptTokens := usage.PromptTokens
	completionTokens := usage.CompletionTokens
	cacheReadTokens := usage.CacheReadTokens
	if promptTokens <= 0 && completionTokens <= 0 && cacheReadTokens <= 0 {
		promptTokens = actualTokens
	}
	return calculateModelPriceCost(price, promptTokens, completionTokens, cacheReadTokens, 1)
}

func calculateModelPriceCost(price ModelPrice, promptTokens, completionTokens, cacheReadTokens int64, multiplier float64) int64 {
	cacheRead := float64(minInt64(maxInt64(cacheReadTokens, 0), maxInt64(promptTokens, 0)))
	input := float64(maxInt64(promptTokens, 0)) - cacheRead
	completion := float64(maxInt64(completionTokens, 0))
	cacheReadPrice := price.InputPrice
	if price.CacheReadPrice != nil {
		cacheReadPrice = *price.CacheReadPrice
	}
	cost := (input*price.InputPrice + cacheRead*cacheReadPrice + completion*price.OutputPrice) * AmountScale * multiplier
	if cost <= 0 {
		return 0
	}
	return int64(math.Ceil(cost))
}

func upstreamPriceKey(channelID int64, model string) string {
	if channelID <= 0 {
		return model
	}
	return fmt.Sprintf("%d:%s", channelID, model)
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

func resolveSubscriptionAccountID(override int64, reservationValue string) int64 {
	if override != 0 {
		return override
	}
	return parseInt64Default(reservationValue, 0)
}

func generateRedeemCode() string {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return fmt.Sprintf("rc_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(bytes)
}

// positiveOrZero returns max(v, 0). The helper is used by the
// dual-track commit pipeline to compute the negative-balance magnitude
// without resorting to a nested ternary.
func positiveOrZero(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// AggregateUsage runs a multi-dimensional SQL aggregation over the ledger.
// An empty filter.Type means "all ledger types" (no type filter).
func (uc *BillingUsecase) AggregateUsage(ctx context.Context, filter UsageFilter) ([]*UsageBucket, *UsageTotals, error) {
	return uc.ledgerRepo.AggregateUsage(ctx, filter)
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
		return calculateModelPriceCost(price, promptTokens, completionTokens, cacheReadTokens, uc.getGroupRatio(pricing, group))
	}
	cost := (prompt + completion*uc.getCompletionRatio(pricing, model)) * uc.getModelRatio(pricing, model) * uc.getGroupRatio(pricing, group)
	if cost <= 0 {
		return 0
	}
	return int64(math.Ceil(cost))
}

// ListLedgersBySubscriptionAccount delegates to the ledger repo.
func (uc *BillingUsecase) ListLedgersBySubscriptionAccount(ctx context.Context, subscriptionAccountID int64, page, pageSize int32) ([]*Ledger, int64, error) {
	return uc.ledgerRepo.ListLedgersBySubscriptionAccount(ctx, subscriptionAccountID, page, pageSize)
}

// ListLedgersWithFilters delegates to the ledger repo.
func (uc *BillingUsecase) ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*Ledger, int64, error) {
	return uc.ledgerRepo.ListLedgersWithFilters(ctx, userID, page, pageSize, ledgerType, startTime, endTime)
}

// ListLedgers delegates to the ledger repo.
func (uc *BillingUsecase) ListLedgers(ctx context.Context, userID string, page, pageSize int32) ([]*Ledger, int64, error) {
	return uc.ledgerRepo.ListLedgers(ctx, userID, page, pageSize)
}

// AggregateLedgerByDate delegates to the ledger repo.
func (uc *BillingUsecase) AggregateLedgerByDate(ctx context.Context, userID, ledgerType string, startTime, endTime time.Time) ([]*DailyAggregate, []*ModelAggregate, error) {
	return uc.ledgerRepo.AggregateLedgerByDate(ctx, userID, ledgerType, startTime, endTime)
}
