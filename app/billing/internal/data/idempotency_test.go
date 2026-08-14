package data

import (
	"context"
	"errors"
	"sync"
	"testing"

	"micro-one-api/app/billing/internal/biz"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupIdempotencyTestDB returns a sqlite DB with the non-partitioned
// billing_ledger_dedupe_claims primary key used by the production write path.
// Writes are serialized (MaxOpenConns=1) because SQLite has a single-writer
// lock; the database primary key, not process-local synchronization, remains
// the authoritative duplicate arbiter.
func setupIdempotencyTestDB(t *testing.T) *gorm.DB {
	db := setupTestDB(t)
	// setupTestDB's billing_ledgers predates the token-split columns; add the
	// missing ones so the full ledgerModel INSERT works.
	require.NoError(t, db.Exec(`ALTER TABLE billing_ledgers ADD COLUMN cache_creation_5m_tokens INTEGER DEFAULT 0`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE billing_ledgers ADD COLUMN cache_creation_1h_tokens INTEGER DEFAULT 0`).Error)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// newIdempotencyUsecase wires a BillingUsecase with the sqlite-backed repos
// and the data-owned transaction runner, exactly like production wiring.
func newIdempotencyUsecase(db *gorm.DB) *biz.BillingUsecase {
	d := &Data{db: db}
	uc := biz.NewBillingUsecase(
		NewAccountRepo(d),
		NewReservationRepo(d),
		NewLedgerRepo(d),
		NewRedeemRepo(d),
		nil, // no pricing store in these tests
	)
	uc.SetTxRunner(NewTxRunner(d))
	return uc
}

func insertUserForIdempotency(t *testing.T, db *gorm.DB, id int64, balance int64) {
	t.Helper()
	require.NoError(t, db.Exec(`INSERT INTO users (id, username, balance) VALUES (?, ?, ?)`, id, "u"+itoa(id), balance).Error)
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func userBalance(t *testing.T, db *gorm.DB, id int64) int64 {
	t.Helper()
	var bal int64
	require.NoError(t, db.Raw(`SELECT balance FROM users WHERE id = ?`, id).Scan(&bal).Error)
	return bal
}

func ledgerCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&ledgerModel{}).Count(&n).Error)
	return n
}

// TestPurchaseSubscription_ConcurrentDuplicateRequestChargesOnce is the P0
// acceptance test: N concurrent purchases with the SAME (user_id, request_id)
// must charge the wallet exactly once. This is the double-charge scenario from
// M6 known-boundary #1, now closed by the global dedupe-claim primary key.
func TestPurchaseSubscription_ConcurrentDuplicateRequestChargesOnce(t *testing.T) {
	db := setupIdempotencyTestDB(t)
	insertUserForIdempotency(t, db, 1, 10000)
	uc := newIdempotencyUsecase(db)

	const n = 8
	results := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "req-dup-1")
			results[i] = err
		}(i)
	}
	wg.Wait()

	success, dup := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			success++
		case errors.Is(err, biz.ErrDuplicateRequest):
			dup++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	require.Equal(t, 1, success, "exactly one of %d concurrent purchases must succeed", n)
	require.Equal(t, n-1, dup, "all other concurrent purchases must be rejected as duplicates")

	// Wallet charged exactly once: 10000 - 1000 = 9000; exactly one ledger row.
	require.Equal(t, int64(9000), userBalance(t, db, 1))
	require.Equal(t, int64(1), ledgerCount(t, db))
}

// TestPurchaseSubscription_DifferentUsersSameGroupBothSucceed guards the
// legacy dedupe-key collision bug fixed in v0.18 P0: previously the purchase
// ledger fell back to "{group_id}:subscription:legacy" (no user_id), so the
// second purchase of the same group — even by a DIFFERENT user — hit the
// unique index and failed. Explicit (user, request) keys must not collide.
func TestPurchaseSubscription_DifferentUsersSameGroupBothSucceed(t *testing.T) {
	db := setupIdempotencyTestDB(t)
	insertUserForIdempotency(t, db, 1, 5000)
	insertUserForIdempotency(t, db, 2, 5000)
	uc := newIdempotencyUsecase(db)

	_, err := uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "req-a")
	require.NoError(t, err, "user 1 buying group 5 must succeed")
	_, err = uc.PurchaseSubscription(context.Background(), "2", 1000, 5, "purchase group=5", "req-b")
	require.NoError(t, err, "user 2 buying the same group 5 must ALSO succeed (legacy collision fixed)")

	require.Equal(t, int64(4000), userBalance(t, db, 1))
	require.Equal(t, int64(4000), userBalance(t, db, 2))
	require.Equal(t, int64(2), ledgerCount(t, db))
}

// TestPurchaseSubscription_SameUserNewRequestSucceeds covers the legitimate
// re-purchase (renewal after expiry): the same user buying the same group
// under a NEW request id is a distinct request and must succeed.
func TestPurchaseSubscription_SameUserNewRequestSucceeds(t *testing.T) {
	db := setupIdempotencyTestDB(t)
	insertUserForIdempotency(t, db, 1, 10000)
	uc := newIdempotencyUsecase(db)

	_, err := uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "req-r1")
	require.NoError(t, err)
	_, err = uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "req-r2")
	require.NoError(t, err, "re-purchase with a fresh request id is not a duplicate")

	require.Equal(t, int64(8000), userBalance(t, db, 1))
	require.Equal(t, int64(2), ledgerCount(t, db))
}

// TestTopUpQuota_DuplicateRequestRejected verifies the recharge path carries
// the same request-level idempotency: a replayed (user_id, request_id) top-up
// must not credit the wallet twice.
func TestTopUpQuota_DuplicateRequestRejected(t *testing.T) {
	db := setupIdempotencyTestDB(t)
	insertUserForIdempotency(t, db, 1, 1000)
	uc := newIdempotencyUsecase(db)

	bal, err := uc.TopUpQuota(context.Background(), "1", "admin", 500, "topup", "req-t1")
	require.NoError(t, err)
	require.Equal(t, int64(1500), bal)

	_, err = uc.TopUpQuota(context.Background(), "1", "admin", 500, "topup", "req-t1")
	require.ErrorIs(t, err, biz.ErrDuplicateRequest, "replayed top-up must be rejected")

	require.Equal(t, int64(1500), userBalance(t, db, 1))
	require.Equal(t, int64(1), ledgerCount(t, db))
}

// TestPurchaseSubscription_EmptyRequestIDNeverCollides verifies the
// legacy-client compatibility path: a purchase without an Idempotency-Key gets
// an auto key, so it never collides with legacy rows NOR with another
// key-less purchase (no idempotency guarantee, same as before v0.18, but the
// legacy {group}:subscription:legacy collision is gone).
func TestPurchaseSubscription_EmptyRequestIDNeverCollides(t *testing.T) {
	db := setupIdempotencyTestDB(t)
	insertUserForIdempotency(t, db, 1, 10000)
	uc := newIdempotencyUsecase(db)

	_, err := uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "")
	require.NoError(t, err)
	_, err = uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "")
	require.NoError(t, err, "key-less purchases use distinct auto keys and must not collide")

	require.Equal(t, int64(8000), userBalance(t, db, 1))
	require.Equal(t, int64(2), ledgerCount(t, db))
}

// TestPurchaseSubscription_ConflictRollsBackBalanceChange asserts that when
// the duplicate claim fails inside the transaction, the balance deduction
// performed earlier in the SAME transaction is rolled back — the wallet is
// left untouched.
func TestPurchaseSubscription_ConflictRollsBackBalanceChange(t *testing.T) {
	db := setupIdempotencyTestDB(t)
	insertUserForIdempotency(t, db, 1, 5000)
	uc := newIdempotencyUsecase(db)

	_, err := uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "req-x")
	require.NoError(t, err)
	_, err = uc.PurchaseSubscription(context.Background(), "1", 1000, 5, "purchase group=5", "req-x")
	require.ErrorIs(t, err, biz.ErrDuplicateRequest)

	// The failed request deducted 1000 inside its transaction, then the insert
	// hit the dedupe-claim primary key and the whole transaction rolled back: balance
	// must be 5000 - 1000 = 4000, NOT 3000.
	require.Equal(t, int64(4000), userBalance(t, db, 1))
}
