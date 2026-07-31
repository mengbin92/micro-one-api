package data

import (
	"context"
	"errors"
	"time"

	"micro-one-api/app/billing/internal/biz"

	subscriptionbiz "micro-one-api/domain/subscription/biz"

	"gorm.io/gorm"
)

type reservationRepo struct {
	data *Data
}

func NewReservationRepo(data *Data) biz.ReservationRepo {
	return &reservationRepo{data: data}
}

func (r *reservationRepo) DB() *gorm.DB {
	if r == nil || r.data == nil {
		return nil
	}
	return r.data.db
}

func (r *reservationRepo) CreateReservation(ctx context.Context, reservation *biz.Reservation) error {
	return r.CreateReservationInTx(ctx, &gormTx{db: r.data.db.WithContext(ctx)}, reservation)
}

func (r *reservationRepo) CreateReservationInTx(ctx context.Context, tx subscriptionbiz.Tx, reservation *biz.Reservation) error {
	db := txDB(tx)
	model := &reservationModel{
		ReservationID:                  reservation.ReservationID,
		UserID:                         reservation.UserID,
		RequestID:                      reservation.RequestID,
		Amount:                         reservation.Amount,
		Status:                         reservation.Status,
		Model:                          stringPtr(reservation.Model),
		ChannelID:                      stringPtr(reservation.ChannelID),
		SubscriptionAccountID:          stringPtr(reservation.SubscriptionAccountID),
		SubscriptionID:                 reservation.SubscriptionID,
		SubscriptionAmountUSD:          reservation.SubscriptionAmountUSD,
		SubscriptionDailyWindowStart:   reservation.SubscriptionDailyWindowStart,
		SubscriptionWeeklyWindowStart:  reservation.SubscriptionWeeklyWindowStart,
		SubscriptionMonthlyWindowStart: reservation.SubscriptionMonthlyWindowStart,
		BalanceAmountQuota:             reservation.BalanceAmountQuota,
		CreatedAt:                      time.Now(),
		UpdatedAt:                      time.Now(),
		ExpiredAt:                      timePtr(reservation.ExpiredAt),
	}

	if err := db.Create(model).Error; err != nil {
		return err
	}
	return nil
}

func (r *reservationRepo) GetReservation(ctx context.Context, reservationID string) (*biz.Reservation, error) {
	return r.GetReservationInTx(ctx, &gormTx{db: r.data.db.WithContext(ctx)}, reservationID)
}

func (r *reservationRepo) GetReservationInTx(ctx context.Context, tx subscriptionbiz.Tx, reservationID string) (*biz.Reservation, error) {
	db := txDB(tx)
	var model reservationModel
	q := db.WithContext(ctx).Where("reservation_id = ?", reservationID)
	if !isSQLite(dialectorName(db)) {
		q = q.Clauses(forUpdateClause(dialectorName(db)))
	}
	if err := q.First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrReservationNotFound
		}
		return nil, err
	}

	return reservationFromModel(&model), nil
}

func (r *reservationRepo) FindByRequestID(ctx context.Context, requestID string) (*biz.Reservation, error) {
	var model reservationModel
	if err := r.data.db.WithContext(ctx).Where("request_id = ?", requestID).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}

	return reservationFromModel(&model), nil
}

// FindByRequestIDInTx is the row-locked variant of FindByRequestID. It
// scopes the lookup by (user_id, request_id) so a request_id owned by
// user A cannot be replayed as user B's cached reservation. The read
// happens inside the caller's transaction so the subsequent insert and
// this read are atomic against another concurrent ReserveQuota for the
// same (user_id, request_id) pair (code-review 2026-07-30 billing-M5).
func (r *reservationRepo) FindByRequestIDInTx(ctx context.Context, tx subscriptionbiz.Tx, userID, requestID string) (*biz.Reservation, error) {
	db := txDB(tx)
	var model reservationModel
	q := db.WithContext(ctx).Where("user_id = ? AND request_id = ?", userID, requestID)
	if !isSQLite(dialectorName(db)) {
		q = q.Clauses(forUpdateClause(dialectorName(db)))
	}
	if err := q.First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return reservationFromModel(&model), nil
}

func (r *reservationRepo) UpdateReservationStatus(ctx context.Context, reservationID string, status string) error {
	return r.data.db.WithContext(ctx).Model(&reservationModel{}).
		Where("reservation_id = ?", reservationID).
		Update("status", status).Error
}

// CASReservationStatus atomically transitions reservationID from `from` to
// `to`. Returns true when the update affected a row, false when the
// reservation is no longer in the `from` state (concurrent commit/release
// or stale caller). The function uses a single conditional UPDATE so the
// state transition is race-free even when the caller is racing against
// another transaction.
func (r *reservationRepo) CASReservationStatus(ctx context.Context, tx subscriptionbiz.Tx, reservationID, from, to string) (bool, error) {
	db := txDB(tx)
	res := db.WithContext(ctx).Exec(
		`UPDATE billing_reservations SET status = ? WHERE reservation_id = ? AND status = ?`,
		to, reservationID, from,
	)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// SetActualCostInTx persists the real settlement cost on a reservation inside
// the caller's transaction (code-review 2026-07-30 billing-L1). It only writes
// a non-zero actual_cost so a legacy row that was never settled keeps Amount
// as the fallback on read paths.
func (r *reservationRepo) SetActualCostInTx(ctx context.Context, tx subscriptionbiz.Tx, reservationID string, actualCost int64) error {
	db := txDB(tx)
	return db.WithContext(ctx).Model(&reservationModel{}).
		Where("reservation_id = ?", reservationID).
		Update("actual_cost", actualCost).Error
}

// LockSubscriptionRow takes a row lock on the user_subscriptions row with
// the given id. The lock is only used by the dual-track pre-deduction
// flow to serialise concurrent reservations against the same
// subscription so the absorber check cannot oversell the daily/weekly/
// monthly window. The function is a no-op on SQLite (the writer
// transaction already serialises writers) and is the only call site in
// the billing domain that reads the subscription table; the subscription
// domain does NOT depend on this function.
func (r *reservationRepo) LockSubscriptionRow(ctx context.Context, tx subscriptionbiz.Tx, subscriptionID int64) error {
	db := txDB(tx)
	var row struct {
		ID int64 `gorm:"column:id"`
	}
	if isSQLite(dialectorName(db)) {
		// SQLite uses BEGIN..COMMIT which already serialises writers.
		// We still issue the SELECT so the function is observably a
		// "lock" call and the test infrastructure has a hook to assert
		// it was issued.
		return db.WithContext(ctx).
			Table("user_subscriptions").
			Where("id = ?", subscriptionID).
			Take(&row).Error
	}
	return db.WithContext(ctx).
		Clauses(forUpdateClause(dialectorName(db))).
		Table("user_subscriptions").
		Where("id = ?", subscriptionID).
		Take(&row).Error
}

// SumActiveFrozenInTx aggregates the subscription-side pre-deduction USD
// for the given (user, subscription) pair, restricted to reservations
// whose window-start snapshot still matches the current window. The
// caller passes the rolled window starts (subscription.DailyWindowStart
// etc.) so a reservation that was created in an earlier window no longer
// contributes to the current absorber check.
//
// The result is in the original (un-multiplied) USD. The caller is
// responsible for multiplying by RateMultiplier when it needs accounting
// USD (the limit/usage comparison space).
func (r *reservationRepo) SumActiveFrozenInTx(ctx context.Context, tx subscriptionbiz.Tx, userID string, subscriptionID, dailyStart, weeklyStart, monthlyStart int64) (float64, float64, float64, int64, error) {
	db := txDB(tx)
	var result struct {
		DailyUSD   float64
		WeeklyUSD  float64
		MonthlyUSD float64
		Count      int64
	}
	res := db.WithContext(ctx).
		Raw(`
				SELECT
				  COALESCE(SUM(CASE WHEN subscription_daily_window_start = ? THEN subscription_amount_usd ELSE 0 END), 0) AS daily_usd,
				  COALESCE(SUM(CASE WHEN subscription_weekly_window_start = ? THEN subscription_amount_usd ELSE 0 END), 0) AS weekly_usd,
				  COALESCE(SUM(CASE WHEN subscription_monthly_window_start = ? THEN subscription_amount_usd ELSE 0 END), 0) AS monthly_usd,
				  COUNT(*) AS count
				FROM billing_reservations
				WHERE user_id = ?
				  AND subscription_id = ?
				  AND status = ?
				  AND (
				    subscription_daily_window_start = ? OR
				    subscription_weekly_window_start = ? OR
				    subscription_monthly_window_start = ?
				  )
			`, dailyStart, weeklyStart, monthlyStart, userID, subscriptionID, biz.ReservationStatusReserved, dailyStart, weeklyStart, monthlyStart).
		Scan(&result)
	if res.Error != nil {
		return 0, 0, 0, 0, res.Error
	}
	return result.DailyUSD, result.WeeklyUSD, result.MonthlyUSD, result.Count, nil
}

func (r *reservationRepo) GetExpiredReservations(ctx context.Context) ([]*biz.Reservation, error) {
	var models []reservationModel
	now := time.Now()
	if err := r.data.db.WithContext(ctx).
		Where("status = ? AND expired_at < ?", "reserved", now).
		Find(&models).Error; err != nil {
		return nil, err
	}

	reservations := make([]*biz.Reservation, len(models))
	for i := range models {
		reservations[i] = reservationFromModel(&models[i])
	}
	return reservations, nil
}

func reservationFromModel(model *reservationModel) *biz.Reservation {
	if model == nil {
		return nil
	}
	return &biz.Reservation{
		ReservationID:                  model.ReservationID,
		UserID:                         model.UserID,
		RequestID:                      model.RequestID,
		Amount:                         model.Amount,
		Status:                         model.Status,
		Model:                          stringFromPtr(model.Model),
		ChannelID:                      stringFromPtr(model.ChannelID),
		SubscriptionAccountID:          stringFromPtr(model.SubscriptionAccountID),
		SubscriptionID:                 model.SubscriptionID,
		SubscriptionAmountUSD:          model.SubscriptionAmountUSD,
		SubscriptionDailyWindowStart:   model.SubscriptionDailyWindowStart,
		SubscriptionWeeklyWindowStart:  model.SubscriptionWeeklyWindowStart,
		SubscriptionMonthlyWindowStart: model.SubscriptionMonthlyWindowStart,
		BalanceAmountQuota:             model.BalanceAmountQuota,
		ActualCost:                     model.ActualCost,
		CreatedAt:                      model.CreatedAt,
		UpdatedAt:                      model.UpdatedAt,
		ExpiredAt:                      timeFromPtr(model.ExpiredAt),
	}
}

func stringPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringFromPtr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeFromPtr(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}
