package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 保留原有的 mock 实现
type mockAccountRepo struct {
	account          *Account
	updateQuota      int64
	updateUsageCalls int
}

func (m *mockAccountRepo) GetAccountSnapshot(ctx context.Context, userID string) (*Account, error) {
	return m.account, nil
}

func (m *mockAccountRepo) BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*Account, error) {
	result := make(map[string]*Account, len(userIDs))
	for _, userID := range userIDs {
		result[userID] = m.account
	}
	return result, nil
}

func (m *mockAccountRepo) UpdateBalance(ctx context.Context, userID string, delta int64, operationType string) (int64, error) {
	if delta < 0 && m.account.Balance+delta < 0 {
		return 0, ErrInsufficientQuota
	}
	m.account.Balance += delta
	m.updateQuota = delta
	return m.account.Balance, nil
}

func (m *mockAccountRepo) UpdateBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, delta int64, operationType string) (int64, error) {
	return m.UpdateBalance(ctx, userID, delta, operationType)
}

func (m *mockAccountRepo) UpdateUsage(ctx context.Context, userID string, usedAmountDelta, requestCountDelta int64) error {
	m.account.UsedAmount += usedAmountDelta
	m.account.RequestCount += requestCountDelta
	m.updateUsageCalls++
	return nil
}

func (m *mockAccountRepo) UpdateUsageInTx(ctx context.Context, tx *gorm.DB, userID string, usedAmountDelta, requestCountDelta int64) error {
	return m.UpdateUsage(ctx, userID, usedAmountDelta, requestCountDelta)
}

func (m *mockAccountRepo) UpdateFrozenAmount(ctx context.Context, userID string, delta int64) error {
	m.account.FrozenAmount += delta
	return nil
}

func (m *mockAccountRepo) ReserveBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, amount int64, allowOverdraft bool) (int64, int64, int64, error) {
	if amount < 0 {
		return 0, 0, 0, errors.New("negative amount")
	}
	if m.account.Balance-amount < 0 && !allowOverdraft {
		return m.account.Balance, m.account.Balance, m.account.FrozenAmount, ErrInsufficientQuota
	}
	oldBalance := m.account.Balance
	m.account.Balance -= amount
	m.account.FrozenAmount += amount
	return oldBalance, m.account.Balance, m.account.FrozenAmount, nil
}

func (m *mockAccountRepo) CommitBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, reserved, actual int64, allowOverdraft bool) (int64, int64, error) {
	oldBalance := m.account.Balance
	m.account.FrozenAmount -= reserved
	m.account.Balance += reserved - actual
	return oldBalance, m.account.Balance, nil
}

func (m *mockAccountRepo) ReleaseBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, reserved int64) (int64, error) {
	m.account.FrozenAmount -= reserved
	m.account.Balance += reserved
	return m.account.Balance, nil
}

type mockReservationRepo struct {
	reservations map[string]*Reservation
}

func (m *mockReservationRepo) CreateReservation(ctx context.Context, reservation *Reservation) error {
	m.reservations[reservation.ReservationID] = reservation
	return nil
}

func (m *mockReservationRepo) GetReservation(ctx context.Context, reservationID string) (*Reservation, error) {
	res, ok := m.reservations[reservationID]
	if !ok {
		return nil, ErrReservationNotFound
	}
	return res, nil
}

func (m *mockReservationRepo) UpdateReservationStatus(ctx context.Context, reservationID string, status string) error {
	if res, ok := m.reservations[reservationID]; ok {
		res.Status = status
	}
	return nil
}

func (m *mockReservationRepo) FindByRequestID(ctx context.Context, requestID string) (*Reservation, error) {
	for _, res := range m.reservations {
		if res.RequestID == requestID {
			return res, nil
		}
	}
	return nil, nil
}

func (m *mockReservationRepo) GetExpiredReservations(ctx context.Context) ([]*Reservation, error) {
	var expired []*Reservation
	for _, res := range m.reservations {
		if res.Status == ReservationStatusReserved && res.ExpiredAt.Before(time.Now()) {
			expired = append(expired, res)
		}
	}
	return expired, nil
}

func (m *mockReservationRepo) CreateReservationInTx(ctx context.Context, tx *gorm.DB, reservation *Reservation) error {
	return m.CreateReservation(ctx, reservation)
}

func (m *mockReservationRepo) GetReservationInTx(ctx context.Context, tx *gorm.DB, reservationID string) (*Reservation, error) {
	return m.GetReservation(ctx, reservationID)
}

func (m *mockReservationRepo) CASReservationStatus(ctx context.Context, tx *gorm.DB, reservationID, from, to string) (bool, error) {
	res, ok := m.reservations[reservationID]
	if !ok {
		return false, ErrReservationNotFound
	}
	if res.Status != from {
		return false, nil
	}
	res.Status = to
	return true, nil
}

func (m *mockReservationRepo) LockSubscriptionRow(ctx context.Context, tx *gorm.DB, subscriptionID int64) error {
	return nil
}

func (m *mockReservationRepo) SumActiveFrozenInTx(ctx context.Context, tx *gorm.DB, userID string, subscriptionID, dailyStart, weeklyStart, monthlyStart int64) (float64, float64, float64, int64, error) {
	var daily, weekly, monthly float64
	var count int64
	for _, res := range m.reservations {
		if res.UserID != userID || res.SubscriptionID != subscriptionID || res.Status != ReservationStatusReserved {
			continue
		}
		matched := false
		if res.SubscriptionDailyWindowStart == dailyStart {
			daily += res.SubscriptionAmountUSD
			matched = true
		}
		if res.SubscriptionWeeklyWindowStart == weeklyStart {
			weekly += res.SubscriptionAmountUSD
			matched = true
		}
		if res.SubscriptionMonthlyWindowStart == monthlyStart {
			monthly += res.SubscriptionAmountUSD
			matched = true
		}
		if matched {
			count++
		}
	}
	return daily, weekly, monthly, count, nil
}

type mockLedgerRepo struct {
	ledgers []*Ledger
}

func (m *mockLedgerRepo) CreateLedger(ctx context.Context, ledger *Ledger) error {
	m.ledgers = append(m.ledgers, ledger)
	return nil
}

func (m *mockLedgerRepo) ListLedgers(ctx context.Context, userID string, page, pageSize int32) ([]*Ledger, int64, error) {
	return m.ledgers, int64(len(m.ledgers)), nil
}

func (m *mockLedgerRepo) ListLedgersWithTimeRange(ctx context.Context, userID string, page, pageSize int32, startTime, endTime time.Time) ([]*Ledger, int64, error) {
	return m.ledgers, int64(len(m.ledgers)), nil
}

func (m *mockLedgerRepo) ListLedgersWithFilters(ctx context.Context, userID string, page, pageSize int32, ledgerType string, startTime, endTime time.Time) ([]*Ledger, int64, error) {
	return m.ledgers, int64(len(m.ledgers)), nil
}

func (m *mockLedgerRepo) AggregateLedgerByDate(ctx context.Context, userID string, ledgerType string, startTime, endTime time.Time) ([]*DailyAggregate, []*ModelAggregate, error) {
	return nil, nil, nil
}

func (m *mockLedgerRepo) ListLedgersBySubscriptionAccount(ctx context.Context, subscriptionAccountID int64, page, pageSize int32) ([]*Ledger, int64, error) {
	return m.ledgers, int64(len(m.ledgers)), nil
}

func (m *mockLedgerRepo) AggregateUsage(ctx context.Context, filter UsageFilter) ([]*UsageBucket, *UsageTotals, error) {
	return nil, &UsageTotals{}, nil
}

func (m *mockLedgerRepo) CreateLedgerInTx(ctx context.Context, tx *gorm.DB, ledger *Ledger) error {
	return m.CreateLedger(ctx, ledger)
}

func (m *mockLedgerRepo) FindByDedupeKey(ctx context.Context, tx *gorm.DB, key string) (*Ledger, error) {
	for _, ledger := range m.ledgers {
		if ledger.LedgerDedupeKey == key {
			return ledger, nil
		}
	}
	return nil, nil
}

func (m *mockLedgerRepo) SumSubscriptionCostByReservation(ctx context.Context, reservationIDs []string) (int64, error) {
	return 0, nil
}

type mockPricingStore struct {
	config PricingConfig
}

func (m mockPricingStore) GetPricingConfig(context.Context) (PricingConfig, error) {
	return m.config, nil
}

type mockRedeemRepo struct {
	codes   map[string]*RedeemCode
	records []*RedeemRecord
}

func (m *mockRedeemRepo) CreateRedeemCodesBatch(ctx context.Context, codes []*RedeemCode) error {
	for _, code := range codes {
		if err := m.CreateRedeemCode(ctx, code); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockRedeemRepo) ListRedeemCodes(ctx context.Context, page, pageSize int32) ([]*RedeemCode, int64, error) {
	var result []*RedeemCode
	for _, code := range m.codes {
		result = append(result, code)
	}
	return result, int64(len(result)), nil
}

func (m *mockRedeemRepo) SearchRedeemCodes(ctx context.Context, keyword string) ([]*RedeemCode, error) {
	var result []*RedeemCode
	for _, code := range m.codes {
		if code.Code == keyword || (code.Name != "" && code.Name == keyword) {
			result = append(result, code)
		}
	}
	return result, nil
}

func (m *mockRedeemRepo) UpdateRedeemCode(ctx context.Context, code *RedeemCode) error {
	if existing, ok := m.codes[code.Code]; ok {
		if code.Name != "" {
			existing.Name = code.Name
		}
		if code.Amount > 0 {
			existing.Amount = code.Amount
		}
		if code.Status > 0 {
			existing.Status = code.Status
		}
	}
	return nil
}

func (m *mockRedeemRepo) DeleteRedeemCode(ctx context.Context, code string) error {
	delete(m.codes, code)
	return nil
}

func (m *mockRedeemRepo) CreateRedeemCode(ctx context.Context, code *RedeemCode) error {
	if m.codes == nil {
		m.codes = make(map[string]*RedeemCode)
	}
	m.codes[code.Code] = code
	return nil
}

func (m *mockRedeemRepo) GetRedeemCode(ctx context.Context, code string) (*RedeemCode, error) {
	c, ok := m.codes[code]
	if !ok {
		return nil, ErrRedeemCodeNotFound
	}
	return c, nil
}

func (m *mockRedeemRepo) UpdateRedeemCodeCount(ctx context.Context, code string, delta int) error {
	if c, ok := m.codes[code]; ok {
		c.Count -= int32(delta)
	}
	return nil
}

func (m *mockRedeemRepo) CreateRedeemRecord(ctx context.Context, record *RedeemRecord) error {
	m.records = append(m.records, record)
	return nil
}

// 正确的预扣测试 - 从 1000 开始，预扣 100，最终应该是 900
func TestReserveQuota_CorrectLogic(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 0,
		Group:        "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req1", 100, "gpt-4o-mini", "channel1", 0)

	require.NoError(t, err)
	assert.NotNil(t, reservation)
	assert.Equal(t, "user1", reservation.UserID)
	assert.Equal(t, "req1", reservation.RequestID)
	assert.Equal(t, int64(100), reservation.Amount)
	assert.Equal(t, ReservationStatusReserved, reservation.Status)
	assert.Equal(t, int64(100), account.FrozenAmount) // 预扣后冻结配额应该是 100
	assert.Equal(t, int64(900), account.Balance)      // 预扣后可用配额应该减少到 900
}

// 正确的提交流程测试 - 从 1000 开始，预扣 100，提交 80
func TestCommitQuota_Success_CorrectLogic(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      900, // 预扣后的状态
		FrozenAmount: 100,
		Group:        "default",
	}

	reservation := &Reservation{
		ReservationID: "res1",
		UserID:        "user1",
		Amount:        100,
		Status:        ReservationStatusReserved,
		Model:         "gpt-4o-mini",
		CreatedAt:     time.Now(),
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{"res1": reservation}}
	ledgerRepo := &mockLedgerRepo{ledgers: make([]*Ledger, 0)}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	committed, refund, err := uc.CommitQuota(context.Background(), "res1", 80, true)

	require.NoError(t, err)
	assert.Equal(t, int64(80), committed)
	assert.Equal(t, int64(20), refund)              // 100 - 80 = 20 退还
	assert.Equal(t, int64(0), account.FrozenAmount) // 冻结配额应该被释放
	assert.Equal(t, int64(920), account.Balance)    // 预扣 100 后退还 20，实际净消费 80
	assert.Len(t, ledgerRepo.ledgers, 1)            // 只记录真实消费，预扣恢复不写流水
}

// 正确的失败提交流程测试 - 从 1000 开始，预扣 100，请求失败
func TestCommitQuota_Failed_CorrectLogic(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      900, // 预扣后的状态
		FrozenAmount: 100,
		Group:        "default",
	}

	reservation := &Reservation{
		ReservationID: "res1",
		UserID:        "user1",
		Amount:        100,
		Status:        ReservationStatusReserved,
		CreatedAt:     time.Now(),
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{"res1": reservation}}
	ledgerRepo := &mockLedgerRepo{ledgers: make([]*Ledger, 0)}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	committed, refund, err := uc.CommitQuota(context.Background(), "res1", 0, false)

	require.NoError(t, err)
	assert.Equal(t, int64(0), committed)            // 没有实际消费
	assert.Equal(t, int64(100), refund)             // 全部退还
	assert.Equal(t, int64(0), account.FrozenAmount) // 冻结配额应该被释放
	// 注意：ReleaseQuota 中会调用 UpdateBalance 增加 100，但由于 mock 的实现，Quota 会从 900 增加到 1000
	assert.Equal(t, int64(1000), account.Balance)
	assert.Len(t, ledgerRepo.ledgers, 1) // 应该只有 1 个 ledger: 退还
}

// 测试配额不足的情况
func TestReserveQuota_InsufficientQuota(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      50,
		FrozenAmount: 0,
		Group:        "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	_, err := uc.ReserveQuota(context.Background(), "user1", "req1", 100, "gpt-4o-mini", "channel1", 0)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientQuota)
}

// 测试提交已提交的预扣
func TestCommitQuota_AlreadyCommitted(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      900,
		FrozenAmount: 100,
		Group:        "default",
	}

	reservation := &Reservation{
		ReservationID: "res1",
		UserID:        "user1",
		Amount:        100,
		Status:        ReservationStatusCommitted,
		CreatedAt:     time.Now(),
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{"res1": reservation}}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	// Idempotent re-entry: the reservation was already committed
	// during a previous call, so the retried commit returns the
	// stored Amount (100) as the committed cost with no refund.
	amount, refund, err := uc.CommitQuota(context.Background(), "res1", 80, true)

	assert.NoError(t, err)
	assert.Equal(t, int64(100), amount)
	assert.Equal(t, int64(0), refund)
}

// 测试释放预扣
func TestReleaseQuota_Success(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      900,
		FrozenAmount: 100,
		Group:        "default",
	}

	reservation := &Reservation{
		ReservationID: "res1",
		UserID:        "user1",
		Amount:        100,
		Status:        ReservationStatusReserved,
		CreatedAt:     time.Now(),
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{"res1": reservation}}
	ledgerRepo := &mockLedgerRepo{ledgers: make([]*Ledger, 0)}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	err := uc.ReleaseQuota(context.Background(), "res1", "test release")

	require.NoError(t, err)
	assert.Equal(t, ReservationStatusReleased, reservation.Status)
	assert.Equal(t, int64(0), account.FrozenAmount)
	assert.Equal(t, int64(1000), account.Balance)
	assert.Len(t, ledgerRepo.ledgers, 1)
	assert.Equal(t, LedgerTypeRefund, ledgerRepo.ledgers[0].Type)
}

// 测试释放不存在的预扣
func TestReleaseQuota_NotFound(t *testing.T) {
	accountRepo := &mockAccountRepo{account: &Account{}}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	err := uc.ReleaseQuota(context.Background(), "nonexistent", "test")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrReservationNotFound)
}

// 测试获取账户快照
func TestGetAccountSnapshot_Success(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 100,
		Group:        "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	snapshot, err := uc.GetAccountSnapshot(context.Background(), "user1")

	require.NoError(t, err)
	assert.NotNil(t, snapshot)
	assert.Equal(t, "user1", snapshot.UserID)
	assert.Equal(t, int64(1000), snapshot.Balance)
	assert.Equal(t, int64(100), snapshot.FrozenAmount)
}

// 测试充值
func TestTopUpQuota_Success(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 0,
		Group:        "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{ledgers: make([]*Ledger, 0)}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	newQuota, err := uc.TopUpQuota(context.Background(), "user1", "admin", 500, "test topup")

	require.NoError(t, err)
	assert.Equal(t, int64(1500), newQuota)
	assert.Equal(t, int64(1500), account.Balance)
	assert.Len(t, ledgerRepo.ledgers, 1)
	assert.Equal(t, LedgerTypeRecharge, ledgerRepo.ledgers[0].Type)
	assert.Equal(t, int64(500), ledgerRepo.ledgers[0].Amount)
}

// 测试创建兑换码
func TestCreateRedeemCode_Success(t *testing.T) {
	accountRepo := &mockAccountRepo{account: &Account{}}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{codes: make(map[string]*RedeemCode)}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	err := uc.CreateRedeemCode(context.Background(), "CODE123", "测试兑换码", 1000, 10, "admin")

	require.NoError(t, err)
	code, err := redeemRepo.GetRedeemCode(context.Background(), "CODE123")
	require.NoError(t, err)
	assert.Equal(t, "CODE123", code.Code)
	assert.Equal(t, "测试兑换码", code.Name)
	assert.Equal(t, int64(1000), code.Amount)
	assert.Equal(t, int32(10), code.Count)
	assert.Equal(t, RedeemCodeStatusEnabled, code.Status)
	assert.Equal(t, "admin", code.CreatedBy)
	// 检查 redeemRepo 中是否有这个码
	assert.Contains(t, redeemRepo.codes, "CODE123")
}

// 测试使用兑换码
func TestRedeemCode_Success(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 0,
		Group:        "default",
	}

	redeemCode := &RedeemCode{
		Code:      "CODE123",
		Amount:    500,
		Count:     10,
		Status:    RedeemCodeStatusEnabled,
		CreatedBy: "admin",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{ledgers: make([]*Ledger, 0)}
	redeemRepo := &mockRedeemRepo{
		codes:   map[string]*RedeemCode{"CODE123": redeemCode},
		records: make([]*RedeemRecord, 0),
	}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	amount, newQuota, err := uc.RedeemCode(context.Background(), "user1", "CODE123")

	require.NoError(t, err)
	assert.Equal(t, int64(500), amount)
	assert.Equal(t, int64(1500), newQuota)
	assert.Equal(t, int64(1500), account.Balance)
	assert.Equal(t, int32(9), redeemCode.Count)
	assert.Len(t, ledgerRepo.ledgers, 1)
	assert.Equal(t, LedgerTypeRedeem, ledgerRepo.ledgers[0].Type)
	assert.Len(t, redeemRepo.records, 1)
}

// 测试使用不存在的兑换码
func TestRedeemCode_NotFound(t *testing.T) {
	accountRepo := &mockAccountRepo{account: &Account{}}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{codes: make(map[string]*RedeemCode)}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	_, _, err := uc.RedeemCode(context.Background(), "user1", "NONEXISTENT")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrRedeemCodeNotFound)
}

// 测试使用已用完的兑换码
func TestRedeemCode_UsedUp(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 0,
		Group:        "default",
	}

	redeemCode := &RedeemCode{
		Code:      "CODE123",
		Amount:    500,
		Count:     0,
		Status:    RedeemCodeStatusEnabled,
		CreatedBy: "admin",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{codes: map[string]*RedeemCode{"CODE123": redeemCode}}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	_, _, err := uc.RedeemCode(context.Background(), "user1", "CODE123")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrRedeemCodeUsedUp)
}

// 测试使用已禁用的兑换码
func TestRedeemCode_Disabled(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 0,
		Group:        "default",
	}

	redeemCode := &RedeemCode{
		Code:      "CODE123",
		Amount:    500,
		Count:     10,
		Status:    RedeemCodeStatusDisabled,
		CreatedBy: "admin",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{codes: map[string]*RedeemCode{"CODE123": redeemCode}}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	_, _, err := uc.RedeemCode(context.Background(), "user1", "CODE123")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrRedeemCodeDisabled)
}

// 测试查询流水
func TestListLedgers_Success(t *testing.T) {
	ledgers := []*Ledger{
		{UserID: "user1", Amount: 100, Type: LedgerTypeConsume},
		{UserID: "user1", Amount: 200, Type: LedgerTypeRecharge},
		{UserID: "user1", Amount: 50, Type: LedgerTypeRefund},
	}

	accountRepo := &mockAccountRepo{account: &Account{}}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{ledgers: ledgers}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	result, total, err := uc.ListLedgers(context.Background(), "user1", 1, 10)

	require.NoError(t, err)
	assert.Len(t, result, 3)
	assert.Equal(t, int64(3), total)
}

func TestCommitQuotaWithUsage_UpdatesUsageCounters(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		UsedAmount:   20,
		RequestCount: 2,
		FrozenAmount: 100,
		Group:        "default",
	}
	reservation := &Reservation{
		ReservationID: "res-1",
		UserID:        "user1",
		Amount:        100,
		Status:        ReservationStatusReserved,
		Model:         "mimo-v2.5",
		ChannelID:     "2",
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: map[string]*Reservation{"res-1": reservation}}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	committed, refund, err := uc.CommitQuotaWithUsage(context.Background(), "res-1", 80, true, LedgerUsage{
		TokenName:        "token-7",
		Endpoint:         "/v1/chat/completions",
		PromptTokens:     30,
		CompletionTokens: 50,
		ElapsedTime:      1234,
	})

	require.NoError(t, err)
	assert.Equal(t, int64(80), committed)
	assert.Equal(t, int64(20), refund)
	assert.Equal(t, int64(100), account.UsedAmount)
	assert.Equal(t, int64(3), account.RequestCount)
	assert.Equal(t, 1, accountRepo.updateUsageCalls)
	require.Len(t, ledgerRepo.ledgers, 1)
	assert.Equal(t, LedgerTypeConsume, ledgerRepo.ledgers[0].Type)
	assert.Equal(t, int64(80), ledgerRepo.ledgers[0].Quota)
	assert.Equal(t, int64(30), ledgerRepo.ledgers[0].PromptTokens)
	assert.Equal(t, int64(50), ledgerRepo.ledgers[0].CompletionTokens)
}

// 测试分组倍率影响
func TestReserveQuota_WithGroupRatio(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 0,
		Group:        "vip",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req1", 100, "gpt-4o-mini", "channel1", 0)

	require.NoError(t, err)
	assert.NotNil(t, reservation)
	// VIP 组的倍率是 0.5，所以 100 tokens 只需要 50 配额
	assert.Equal(t, int64(50), reservation.Amount)
}

func TestCommitQuota_UsesModelAndCompletionRatios(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		ModelRatios:      map[string]float64{"gpt-4o-mini": 2},
		CompletionRatios: map[string]float64{"gpt-4o-mini": 3},
	})

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req-price", 100, "gpt-4o-mini", "channel1", 0)
	require.NoError(t, err)
	assert.Equal(t, int64(200), reservation.Amount)

	committed, refund, err := uc.CommitQuotaWithUsage(context.Background(), reservation.ReservationID, 100, true, LedgerUsage{
		PromptTokens:     10,
		CompletionTokens: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(140), committed)
	assert.Equal(t, int64(60), refund)
	assert.Equal(t, int64(860), account.Balance)
	require.Len(t, ledgerRepo.ledgers, 1)
	assert.Equal(t, int64(-140), ledgerRepo.ledgers[0].Amount)
	assert.Equal(t, int64(860), ledgerRepo.ledgers[0].BalanceAfter)
}

func TestCommitQuota_UsesModelPrices(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	cacheReadPrice := 0.10 / 1000000
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		ModelPrices: map[string]ModelPrice{
			"gpt-5.5": {
				InputPrice:     0.65 / 1000000,
				OutputPrice:    3.90 / 1000000,
				CacheReadPrice: &cacheReadPrice,
			},
		},
	})

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req-model-price", 100, "gpt-5.5", "channel1", 0)
	require.NoError(t, err)
	assert.Equal(t, int64(1), reservation.Amount)

	committed, refund, err := uc.CommitQuotaWithUsage(context.Background(), reservation.ReservationID, 100, true, LedgerUsage{
		PromptTokens:     10,
		CompletionTokens: 20,
		CacheReadTokens:  4,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), committed)
	assert.Equal(t, int64(0), refund)
	assert.Equal(t, int64(999), account.Balance)
}

func TestReserveQuota_UsesDynamicPricingStore(t *testing.T) {
	account := &Account{UserID: "user1", Balance: 1000, Group: "vip"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		GroupRatios: map[string]float64{"vip": 10},
		ModelRatios: map[string]float64{"gpt-4o-mini": 10},
		PricingStore: mockPricingStore{config: PricingConfig{
			GroupRatios: map[string]float64{"vip": 0.5},
			ModelRatios: map[string]float64{"gpt-4o-mini": 0.2},
		}},
	})

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req-dynamic", 100, "gpt-4o-mini", "channel1", 0)
	require.NoError(t, err)
	assert.Equal(t, int64(10), reservation.Amount)
}

// 测试零成本请求
func TestReserveQuota_ZeroCost(t *testing.T) {
	account := &Account{
		UserID:       "user1",
		Balance:      1000,
		FrozenAmount: 0,
		Group:        "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req1", 0, "gpt-4o-mini", "channel1", 0)

	require.NoError(t, err)
	assert.NotNil(t, reservation)
	// 零成本请求最少需要 1 配额
	assert.Equal(t, int64(1), reservation.Amount)
}
