package biz

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func (m *mockAccountRepo) UpdateQuota(ctx context.Context, userID string, delta int64, operationType string) (int64, error) {
	if delta < 0 && m.account.Quota+delta < 0 {
		return 0, ErrInsufficientQuota
	}
	m.account.Quota += delta
	m.updateQuota = delta
	return m.account.Quota, nil
}

func (m *mockAccountRepo) UpdateUsage(ctx context.Context, userID string, usedQuotaDelta, requestCountDelta int64) error {
	m.account.UsedQuota += usedQuotaDelta
	m.account.RequestCount += requestCountDelta
	m.updateUsageCalls++
	return nil
}

func (m *mockAccountRepo) UpdateFrozenQuota(ctx context.Context, userID string, delta int64) error {
	m.account.FrozenQuota += delta
	return nil
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
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 0,
		Group:       "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req1", 100, "gpt-4o-mini", "channel1")

	require.NoError(t, err)
	assert.NotNil(t, reservation)
	assert.Equal(t, "user1", reservation.UserID)
	assert.Equal(t, "req1", reservation.RequestID)
	assert.Equal(t, int64(100), reservation.Amount)
	assert.Equal(t, ReservationStatusReserved, reservation.Status)
	assert.Equal(t, int64(100), account.FrozenQuota) // 预扣后冻结配额应该是 100
	assert.Equal(t, int64(900), account.Quota)       // 预扣后可用配额应该减少到 900
}

// 正确的提交流程测试 - 从 1000 开始，预扣 100，提交 80
func TestCommitQuota_Success_CorrectLogic(t *testing.T) {
	account := &Account{
		UserID:      "user1",
		Quota:       900, // 预扣后的状态
		FrozenQuota: 100,
		Group:       "default",
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
	assert.Equal(t, int64(20), refund)             // 100 - 80 = 20 退还
	assert.Equal(t, int64(0), account.FrozenQuota) // 冻结配额应该被释放
	assert.Equal(t, int64(920), account.Quota)     // 预扣 100 后退还 20，实际净消费 80
	assert.Len(t, ledgerRepo.ledgers, 1)           // 只记录真实消费，预扣恢复不写流水
}

// 正确的失败提交流程测试 - 从 1000 开始，预扣 100，请求失败
func TestCommitQuota_Failed_CorrectLogic(t *testing.T) {
	account := &Account{
		UserID:      "user1",
		Quota:       900, // 预扣后的状态
		FrozenQuota: 100,
		Group:       "default",
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
	assert.Equal(t, int64(0), committed)           // 没有实际消费
	assert.Equal(t, int64(100), refund)            // 全部退还
	assert.Equal(t, int64(0), account.FrozenQuota) // 冻结配额应该被释放
	// 注意：ReleaseQuota 中会调用 UpdateQuota 增加 100，但由于 mock 的实现，Quota 会从 900 增加到 1000
	assert.Equal(t, int64(1000), account.Quota)
	assert.Len(t, ledgerRepo.ledgers, 1) // 应该只有 1 个 ledger: 退还
}

// 测试配额不足的情况
func TestReserveQuota_InsufficientQuota(t *testing.T) {
	account := &Account{
		UserID:      "user1",
		Quota:       50,
		FrozenQuota: 0,
		Group:       "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	_, err := uc.ReserveQuota(context.Background(), "user1", "req1", 100, "gpt-4o-mini", "channel1")

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrInsufficientQuota)
}

// 测试提交已提交的预扣
func TestCommitQuota_AlreadyCommitted(t *testing.T) {
	account := &Account{
		UserID:      "user1",
		Quota:       900,
		FrozenQuota: 100,
		Group:       "default",
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

	_, _, err := uc.CommitQuota(context.Background(), "res1", 80, true)

	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrReservationCommitted)
}

// 测试释放预扣
func TestReleaseQuota_Success(t *testing.T) {
	account := &Account{
		UserID:      "user1",
		Quota:       900,
		FrozenQuota: 100,
		Group:       "default",
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
	assert.Equal(t, int64(0), account.FrozenQuota)
	assert.Equal(t, int64(1000), account.Quota)
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
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 100,
		Group:       "default",
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
	assert.Equal(t, int64(1000), snapshot.Quota)
	assert.Equal(t, int64(100), snapshot.FrozenQuota)
}

// 测试充值
func TestTopUpQuota_Success(t *testing.T) {
	account := &Account{
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 0,
		Group:       "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{ledgers: make([]*Ledger, 0)}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	newQuota, err := uc.TopUpQuota(context.Background(), "user1", "admin", 500, "test topup")

	require.NoError(t, err)
	assert.Equal(t, int64(1500), newQuota)
	assert.Equal(t, int64(1500), account.Quota)
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
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 0,
		Group:       "default",
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
	assert.Equal(t, int64(1500), account.Quota)
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
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 0,
		Group:       "default",
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
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 0,
		Group:       "default",
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
		Quota:        1000,
		UsedQuota:    20,
		RequestCount: 2,
		FrozenQuota:  100,
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
	assert.Equal(t, int64(100), account.UsedQuota)
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
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 0,
		Group:       "vip",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req1", 100, "gpt-4o-mini", "channel1")

	require.NoError(t, err)
	assert.NotNil(t, reservation)
	// VIP 组的倍率是 0.5，所以 100 tokens 只需要 50 配额
	assert.Equal(t, int64(50), reservation.Amount)
}

func TestCommitQuota_UsesModelAndCompletionRatios(t *testing.T) {
	account := &Account{UserID: "user1", Quota: 1000, Group: "default"}
	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}
	uc := NewBillingUsecaseWithPricing(accountRepo, reservationRepo, ledgerRepo, redeemRepo, PricingConfig{
		ModelRatios:      map[string]float64{"gpt-4o-mini": 2},
		CompletionRatios: map[string]float64{"gpt-4o-mini": 3},
	})

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req-price", 100, "gpt-4o-mini", "channel1")
	require.NoError(t, err)
	assert.Equal(t, int64(200), reservation.Amount)

	committed, refund, err := uc.CommitQuotaWithUsage(context.Background(), reservation.ReservationID, 100, true, LedgerUsage{
		PromptTokens:     10,
		CompletionTokens: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(140), committed)
	assert.Equal(t, int64(60), refund)
	assert.Equal(t, int64(860), account.Quota)
	require.Len(t, ledgerRepo.ledgers, 1)
	assert.Equal(t, int64(-140), ledgerRepo.ledgers[0].Amount)
	assert.Equal(t, int64(860), ledgerRepo.ledgers[0].BalanceAfter)
}

func TestReserveQuota_UsesDynamicPricingStore(t *testing.T) {
	account := &Account{UserID: "user1", Quota: 1000, Group: "vip"}
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

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req-dynamic", 100, "gpt-4o-mini", "channel1")
	require.NoError(t, err)
	assert.Equal(t, int64(10), reservation.Amount)
}

// 测试零成本请求
func TestReserveQuota_ZeroCost(t *testing.T) {
	account := &Account{
		UserID:      "user1",
		Quota:       1000,
		FrozenQuota: 0,
		Group:       "default",
	}

	accountRepo := &mockAccountRepo{account: account}
	reservationRepo := &mockReservationRepo{reservations: make(map[string]*Reservation)}
	ledgerRepo := &mockLedgerRepo{}
	redeemRepo := &mockRedeemRepo{}

	uc := NewBillingUsecase(accountRepo, reservationRepo, ledgerRepo, redeemRepo, nil)

	reservation, err := uc.ReserveQuota(context.Background(), "user1", "req1", 0, "gpt-4o-mini", "channel1")

	require.NoError(t, err)
	assert.NotNil(t, reservation)
	// 零成本请求最少需要 1 配额
	assert.Equal(t, int64(1), reservation.Amount)
}
