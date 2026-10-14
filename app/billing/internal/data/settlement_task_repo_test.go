package data

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"micro-one-api/app/billing/internal/biz"
	"micro-one-api/pkg/jsonx"
)

func settlementIntakeDB(t *testing.T) *Data {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "billing.db")+"?_busy_timeout=5000&_journal_mode=WAL"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&reservationModel{}, &settlementTaskModel{}))
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return &Data{db: db}
}

func intakeReservation(t *testing.T, d *Data, status string) {
	t.Helper()
	require.NoError(t, d.db.Create(&reservationModel{ReservationID: "res-intake", UserID: "1", RequestID: "attempt-1", Status: status, ExpiredAt: timePtr(time.Now().Add(-time.Minute))}).Error)
}

func intakeTask() *biz.SettleTask {
	return &biz.SettleTask{ReservationID: "res-intake", RequestID: "res-intake", ActualTokens: 10, Success: true, Usage: biz.LedgerUsage{PromptTokens: 10}, Timestamp: time.Now()}
}

func TestSettlementIntakeRejectsChangedPayload(t *testing.T) {
	for _, state := range []string{"pending", "processing", "failed", "completed"} {
		t.Run(state, func(t *testing.T) {
			d := settlementIntakeDB(t)
			intakeReservation(t, d, biz.ReservationStatusReserved)
			store := NewSettlementTaskRepo(d)
			original := intakeTask()
			require.NoError(t, store.SaveTask(context.Background(), original))
			require.NoError(t, d.db.Model(&settlementTaskModel{}).Where("reservation_id = ?", original.ReservationID).Update("status", state).Error)
			changed := *original
			changed.ActualTokens = 11
			require.Error(t, store.SaveTask(context.Background(), &changed), "same final event must never replace accepted financial inputs")
			var row settlementTaskModel
			require.NoError(t, d.db.First(&row).Error)
			var retained biz.SettleTask
			require.NoError(t, jsonx.Unmarshal([]byte(row.Payload), &retained))
			require.EqualValues(t, 10, retained.ActualTokens)
			require.Equal(t, state, row.Status)
		})
	}
}

func TestSettlementIntakeDuplicateKeepsProcessingAndOriginalClock(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	store := NewSettlementTaskRepo(d)
	original := intakeTask()
	require.NoError(t, store.SaveTask(context.Background(), original))
	claimed, err := store.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	replay := *original
	replay.Timestamp = original.Timestamp.Add(time.Hour)
	require.NoError(t, store.SaveTask(context.Background(), &replay))
	var row settlementTaskModel
	require.NoError(t, d.db.First(&row).Error)
	var retained biz.SettleTask
	require.NoError(t, jsonx.Unmarshal([]byte(row.Payload), &retained))
	require.Equal(t, "processing", row.Status, "retry must not revoke an active worker claim")
	require.True(t, retained.Timestamp.Equal(original.Timestamp), "first received clock must remain immutable")
}

func TestSettlementRecoveryRechecksRetryDeadlineAfterCandidateSelection(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	store := NewSettlementTaskRepo(d)
	require.NoError(t, store.SaveTask(context.Background(), intakeTask()))
	retryAt := time.Now().Add(time.Hour).Unix()
	rescheduled := false
	// Reproduce a competing worker that fails and schedules backoff after the
	// candidate SELECT but before this recovery worker's claim UPDATE.
	require.NoError(t, d.db.Callback().Update().Before("gorm:update").Register("reschedule_selected_settlement", func(tx *gorm.DB) {
		if tx.Statement.Table != "billing_settlement_tasks" || rescheduled {
			return
		}
		rescheduled = true
		require.NoError(t, tx.Exec("UPDATE billing_settlement_tasks SET status = 'failed', next_retry_at = ? WHERE reservation_id = ?", retryAt, "res-intake").Error)
	}))
	claimed, err := store.ListPending(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, rescheduled)
	require.Empty(t, claimed, "a stale candidate must not erase a newer worker's retry backoff")
	var row settlementTaskModel
	require.NoError(t, d.db.First(&row).Error)
	require.Equal(t, "failed", row.Status)
	require.Equal(t, retryAt, row.NextRetryAt)
}

func TestSettlementIntakeRejectsTerminalReservation(t *testing.T) {
	for _, status := range []string{biz.ReservationStatusReleased, biz.ReservationStatusExpired, biz.ReservationStatusCommitted} {
		t.Run(status, func(t *testing.T) {
			d := settlementIntakeDB(t)
			intakeReservation(t, d, status)
			require.Error(t, NewSettlementTaskRepo(d).SaveTask(context.Background(), intakeTask()))
			var count int64
			require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestSettlementIntakeArrivalAfterExpirySelection(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	repo := NewReservationRepo(d)
	candidates, err := repo.GetExpiredReservations(context.Background())
	require.NoError(t, err)
	require.Len(t, candidates, 1)
	require.NoError(t, NewSettlementTaskRepo(d).SaveTask(context.Background(), intakeTask()))
	uc := biz.NewBillingUsecaseWithOptions(biz.BillingOptions{ReservationRepo: repo, TxRunner: NewTxRunner(d)})
	_ = uc.ReleaseReservation(context.Background(), candidates[0].ReservationID, "stale expiry candidate", biz.ReservationStatusExpired)
	stored, err := repo.GetReservation(context.Background(), "res-intake")
	require.NoError(t, err)
	require.Equal(t, biz.ReservationStatusReserved, stored.Status, "accepted settlement must survive a stale sweeper candidate")
}

func signedIntakeTask(t *testing.T) *biz.SettleTask {
	t.Helper()
	task := intakeTask()
	task.SettlementReceiptVersion = 1
	task.SettlementEventID = task.ReservationID + "/final/0"
	digest, err := task.PayloadDigest()
	require.NoError(t, err)
	task.SettlementPayloadDigest = digest
	return task
}

func TestSettlementReceiptReleasedIsNotFinancialSuccess(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReleased)
	store := NewSettlementTaskRepo(d).(biz.DurableSettlementTaskStore)
	receipt, err := store.AcceptTask(context.Background(), signedIntakeTask(t))
	require.NoError(t, err)
	require.Equal(t, "released", receipt.Status)
	require.False(t, receipt.Received)
	require.False(t, receipt.SettlementAccepted)
	require.False(t, receipt.Settled)
	var count int64
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestSettlementReceiptRestartAndCommittedZero(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	task := signedIntakeTask(t)
	first, err := NewSettlementTaskRepo(d).(biz.DurableSettlementTaskStore).AcceptTask(context.Background(), task)
	require.NoError(t, err)
	require.True(t, first.Received)
	require.True(t, first.SettlementAccepted)
	require.False(t, first.Settled)
	// A newly constructed repo recovers the original durable receipt.
	restored := NewSettlementTaskRepo(d).(biz.DurableSettlementTaskStore)
	replay := *task
	replay.Timestamp = replay.Timestamp.Add(time.Hour)
	received, err := restored.AcceptTask(context.Background(), &replay)
	require.NoError(t, err)
	require.True(t, received.Duplicate)
	require.Equal(t, first.PayloadDigest, received.PayloadDigest)
	require.NoError(t, d.db.Model(&reservationModel{}).Where("reservation_id = ?", task.ReservationID).Updates(map[string]any{"status": biz.ReservationStatusCommitted, "amount": 100, "actual_cost": 0}).Error)
	completed, err := restored.AcceptTask(context.Background(), &replay)
	require.NoError(t, err)
	require.True(t, completed.Settled)
	require.Equal(t, "settled", completed.Status)
	require.Zero(t, completed.CommittedAmount, "known zero must not become the original reserve estimate")
	replay.ActualTokens++
	digest, err := replay.PayloadDigest()
	require.NoError(t, err)
	replay.SettlementPayloadDigest = digest
	_, err = restored.AcceptTask(context.Background(), &replay)
	require.ErrorIs(t, err, biz.ErrSettlementPayloadConflict)
}

func TestSettlementReceiptValidatesDigestBeforePersistence(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	task := signedIntakeTask(t)
	task.ActualTokens++
	_, err := NewSettlementTaskRepo(d).(biz.DurableSettlementTaskStore).AcceptTask(context.Background(), task)
	require.ErrorIs(t, err, biz.ErrSettlementReceiptInvalid)
	var count int64
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestSettlementIntakeConcurrentConflictingContent(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	store := NewSettlementTaskRepo(d)
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, tokens := range []int64{10, 11} {
		task := intakeTask()
		task.ActualTokens = tokens
		go func(task *biz.SettleTask) { <-start; results <- store.SaveTask(context.Background(), task) }(task)
	}
	close(start)
	successes, conflicts := 0, 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		} else {
			require.ErrorIs(t, err, biz.ErrSettlementPayloadConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	var count int64
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
}

func TestSettlementIntakeCorruptionCannotReachFinancialWorker(t *testing.T) {
	d := settlementIntakeDB(t)
	intakeReservation(t, d, biz.ReservationStatusReserved)
	store := NewSettlementTaskRepo(d)
	require.NoError(t, store.SaveTask(context.Background(), intakeTask()))
	var row settlementTaskModel
	require.NoError(t, d.db.First(&row).Error)
	var task biz.SettleTask
	require.NoError(t, jsonx.Unmarshal([]byte(row.Payload), &task))
	require.NotEmpty(t, task.AcceptedPayloadDigest)
	task.ActualTokens++
	changed, err := jsonx.Marshal(&task)
	require.NoError(t, err)
	require.NoError(t, d.db.Model(&settlementTaskModel{}).Where("id = ?", row.ID).Update("payload", string(changed)).Error)
	claimed, err := store.ListPending(context.Background(), 10)
	require.NoError(t, err)
	require.Empty(t, claimed)
	require.NoError(t, d.db.First(&row).Error)
	require.Equal(t, "invalid", row.Status)
	require.NotEmpty(t, row.LastError)
}
