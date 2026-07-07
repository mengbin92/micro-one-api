package data

import (
	"context"
	"errors"
	"strconv"

	"micro-one-api/internal/billing/biz"

	"gorm.io/gorm"
)

type accountRepo struct {
	data *Data
}

func NewAccountRepo(data *Data) biz.AccountRepo {
	return &accountRepo{data: data}
}

func (r *accountRepo) GetAccountSnapshot(ctx context.Context, userID string) (*biz.Account, error) {
	var user struct {
		ID           int64  `gorm:"column:id"`
		Username     string `gorm:"column:username"`
		DisplayName  string `gorm:"column:display_name"`
		Group        string `gorm:"column:group"`
		Balance      int64  `gorm:"column:balance"`
		UsedAmount   int64  `gorm:"column:used_amount"`
		RequestCount int64  `gorm:"column:request_count"`
		FrozenAmount int64  `gorm:"column:frozen_amount"`
		Status       int32  `gorm:"column:status"`
	}

	if err := r.data.db.WithContext(ctx).Table("users").Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrAccountNotFound
		}
		return nil, err
	}

	return &biz.Account{
		UserID:       userID,
		Username:     user.Username,
		DisplayName:  user.DisplayName,
		Group:        user.Group,
		Balance:      user.Balance,
		UsedAmount:   user.UsedAmount,
		RequestCount: user.RequestCount,
		FrozenAmount: user.FrozenAmount,
		Status:       user.Status,
	}, nil
}

func (r *accountRepo) UpdateBalance(ctx context.Context, userID string, delta int64, operationType string) (int64, error) {
	tx := r.data.db.WithContext(ctx).Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	newBalance, err := r.UpdateBalanceInTx(ctx, tx, userID, delta, operationType)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return newBalance, nil
}

func (r *accountRepo) UpdateBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, delta int64, operationType string) (int64, error) {
	account, err := r.getAccountForUpdate(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	newBalance := account.Balance + delta
	if newBalance < 0 {
		return 0, biz.ErrInsufficientQuota
	}

	if err := tx.Table("users").Where("id = ?", userID).Update("balance", newBalance).Error; err != nil {
		return 0, err
	}
	return newBalance, nil
}

func (r *accountRepo) UpdateUsage(ctx context.Context, userID string, usedAmountDelta, requestCountDelta int64) error {
	return r.UpdateUsageInTx(ctx, r.data.db.WithContext(ctx), userID, usedAmountDelta, requestCountDelta)
}

func (r *accountRepo) UpdateUsageInTx(ctx context.Context, tx *gorm.DB, userID string, usedAmountDelta, requestCountDelta int64) error {
	if tx == nil {
		tx = r.data.db.WithContext(ctx)
	}
	return tx.WithContext(ctx).Table("users").
		Where("id = ?", userID).
		Updates(map[string]interface{}{
			"used_amount":   gorm.Expr("used_amount + ?", usedQuotaExpr(usedAmountDelta)),
			"request_count": gorm.Expr("request_count + ?", requestCountDelta),
		}).Error
}

func usedQuotaExpr(delta int64) interface{} {
	if delta < 0 {
		// used_amount is monotonically increasing; never let a refund decrement
		// the running total.
		return 0
	}
	return delta
}

func (r *accountRepo) UpdateFrozenAmount(ctx context.Context, userID string, delta int64) error {
	var account struct {
		FrozenAmount int64 `gorm:"column:frozen_amount"`
	}
	if err := r.data.db.WithContext(ctx).Table("users").Where("id = ?", userID).First(&account).Error; err != nil {
		return err
	}

	newFrozenAmount := account.FrozenAmount + delta
	return r.data.db.WithContext(ctx).Table("users").Where("id = ?", userID).Update("frozen_amount", newFrozenAmount).Error
}

func (r *accountRepo) BatchGetAccountSnapshots(ctx context.Context, userIDs []string) (map[string]*biz.Account, error) {
	if len(userIDs) == 0 {
		return map[string]*biz.Account{}, nil
	}

	var users []struct {
		ID           int64  `gorm:"column:id"`
		Username     string `gorm:"column:username"`
		DisplayName  string `gorm:"column:display_name"`
		Group        string `gorm:"column:group"`
		Balance      int64  `gorm:"column:balance"`
		UsedAmount   int64  `gorm:"column:used_amount"`
		RequestCount int64  `gorm:"column:request_count"`
		FrozenAmount int64  `gorm:"column:frozen_amount"`
		Status       int32  `gorm:"column:status"`
	}

	if err := r.data.db.WithContext(ctx).Table("users").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
		return nil, err
	}

	result := make(map[string]*biz.Account, len(users))
	for _, user := range users {
		userID := int64ToString(user.ID)
		result[userID] = &biz.Account{
			UserID:       userID,
			Username:     user.Username,
			DisplayName:  user.DisplayName,
			Group:        user.Group,
			Balance:      user.Balance,
			UsedAmount:   user.UsedAmount,
			RequestCount: user.RequestCount,
			FrozenAmount: user.FrozenAmount,
			Status:       user.Status,
		}
	}

	return result, nil
}

// ReserveBalanceInTx atomically deducts `amount` from the user's wallet and
// increments the frozen counter inside the caller's transaction. The wallet
// update is a single UPDATE so concurrent reservations cannot lose each
// other's pre-deduction. When allowOverdraft is false, the function refuses
// to push the wallet negative; the caller is then expected to roll back
// its transaction so the wallet state stays consistent.
func (r *accountRepo) ReserveBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, amount int64, allowOverdraft bool) (int64, int64, int64, error) {
	if amount < 0 {
		return 0, 0, 0, errors.New("reserve balance: negative amount")
	}
	if amount == 0 {
		// Nothing to do but still surface the current balance so the caller
		// has a coherent snapshot for downstream writes.
		account, err := r.getAccountForUpdate(ctx, tx, userID)
		if err != nil {
			return 0, 0, 0, err
		}
		return account.Balance, account.Balance, account.FrozenAmount, nil
	}
	// We rely on a single conditional UPDATE so concurrent reservations
	// cannot lose each other's pre-deduction. The no-overdraft variant
	// uses a "balance >= amount" guard that is checked atomically with
	// the SET; the overdraft variant drops the guard so the wallet can
	// be pushed negative when the operator has enabled it.
	stmt := `UPDATE users
		SET balance = balance - ?,
		    frozen_amount = frozen_amount + ?
		WHERE id = ?`
	args := []interface{}{amount, amount, userID}
	if !allowOverdraft {
		stmt += ` AND balance >= ?`
		args = append(args, amount)
	}
	res := tx.WithContext(ctx).Exec(stmt, args...)
	if res.Error != nil {
		return 0, 0, 0, res.Error
	}
	if res.RowsAffected == 0 {
		// Either the user does not exist or the balance would have gone
		// negative. Distinguish by re-reading the row.
		account, err := r.getAccountForUpdate(ctx, tx, userID)
		if err != nil {
			if errors.Is(err, biz.ErrAccountNotFound) {
				return 0, 0, 0, biz.ErrAccountNotFound
			}
			return 0, 0, 0, err
		}
		if !allowOverdraft && account.Balance-amount < 0 {
			return account.Balance, account.Balance, account.FrozenAmount, biz.ErrInsufficientQuota
		}
		return account.Balance, account.Balance, account.FrozenAmount, biz.ErrInsufficientQuota
	}
	// Re-read the row to return the exact (old, new) balance. This is a
	// single point-in-time read inside the transaction so it sees the row
	// state we just wrote.
	account, err := r.getAccountForUpdate(ctx, tx, userID)
	if err != nil {
		return 0, 0, 0, err
	}
	newBalance := account.Balance
	oldBalance := newBalance + amount
	return oldBalance, newBalance, account.FrozenAmount, nil
}

// CommitBalanceInTx atomically releases the reserved frozen amount and
// applies the actual settlement in a single UPDATE. The difference
// `reserved - actual` is refunded to the wallet, the actual amount is
// deducted, and the frozen counter is decremented by `reserved`. When
// allowOverdraft is true the wallet can go negative.
func (r *accountRepo) CommitBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, reserved, actual int64, allowOverdraft bool) (int64, int64, error) {
	if reserved < 0 {
		return 0, 0, errors.New("commit balance: negative reserved")
	}
	if actual < 0 {
		return 0, 0, errors.New("commit balance: negative actual")
	}
	if reserved == 0 {
		// Nothing to release but the actual amount still has to be
		// deducted so the wallet reflects the cost.
		account, err := r.getAccountForUpdate(ctx, tx, userID)
		if err != nil {
			return 0, 0, err
		}
		if actual == 0 {
			return account.Balance, account.Balance, nil
		}
		delta := -actual
		stmt := `UPDATE users
			SET balance = balance + ?
			WHERE id = ?`
		args := []interface{}{delta, userID}
		if !allowOverdraft {
			stmt += ` AND balance + ? >= 0`
			args = append(args, delta)
		}
		res := tx.WithContext(ctx).Exec(stmt, args...)
		if res.Error != nil {
			return 0, 0, res.Error
		}
		if res.RowsAffected == 0 {
			return account.Balance, account.Balance, biz.ErrInsufficientQuota
		}
		updated, err := r.getAccountForUpdate(ctx, tx, userID)
		if err != nil {
			return 0, 0, err
		}
		return account.Balance, updated.Balance, nil
	}
	// The reserve step already moved balance by -reserved. Commit releases
	// the frozen amount and applies the true wallet cost, so the net commit
	// delta is reserved - actual.
	// frozen_amount -= reserved
	delta := reserved - actual
	stmt := `UPDATE users
			SET balance = balance + ?,
			    frozen_amount = frozen_amount - ?
			WHERE id = ?`
	args := []interface{}{delta, reserved, userID}
	if !allowOverdraft {
		stmt += ` AND balance + ? >= 0`
		args = append(args, delta)
	}
	res := tx.WithContext(ctx).Exec(stmt, args...)
	if res.Error != nil {
		return 0, 0, res.Error
	}
	if res.RowsAffected == 0 {
		account, err := r.getAccountForUpdate(ctx, tx, userID)
		if err != nil {
			return 0, 0, err
		}
		if !allowOverdraft && account.Balance+delta < 0 {
			return account.Balance, account.Balance, biz.ErrInsufficientQuota
		}
		return account.Balance, account.Balance, biz.ErrInsufficientQuota
	}
	account, err := r.getAccountForUpdate(ctx, tx, userID)
	if err != nil {
		return 0, 0, err
	}
	oldBalance := account.Balance - delta
	return oldBalance, account.Balance, nil
}

// ReleaseBalanceInTx atomically refunds `reserved` to the wallet and
// decrements the frozen counter in a single UPDATE. The function is the
// release counterpart of ReserveBalanceInTx; it never pushes the wallet
// below its pre-reservation value.
func (r *accountRepo) ReleaseBalanceInTx(ctx context.Context, tx *gorm.DB, userID string, reserved int64) (int64, error) {
	if reserved < 0 {
		return 0, errors.New("release balance: negative reserved")
	}
	if reserved == 0 {
		account, err := r.getAccountForUpdate(ctx, tx, userID)
		if err != nil {
			return 0, err
		}
		return account.Balance, nil
	}
	res := tx.WithContext(ctx).Exec(`
		UPDATE users
		SET balance = balance + ?,
		    frozen_amount = frozen_amount - ?
		WHERE id = ?
	`, reserved, reserved, userID)
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, biz.ErrAccountNotFound
	}
	account, err := r.getAccountForUpdate(ctx, tx, userID)
	if err != nil {
		return 0, err
	}
	return account.Balance, nil
}

func (r *accountRepo) getAccountForUpdate(ctx context.Context, tx *gorm.DB, userID string) (*biz.Account, error) {
	var user struct {
		ID           int64  `gorm:"column:id"`
		Username     string `gorm:"column:username"`
		DisplayName  string `gorm:"column:display_name"`
		Group        string `gorm:"column:group"`
		Balance      int64  `gorm:"column:balance"`
		UsedAmount   int64  `gorm:"column:used_amount"`
		RequestCount int64  `gorm:"column:request_count"`
		FrozenAmount int64  `gorm:"column:frozen_amount"`
		Status       int32  `gorm:"column:status"`
	}
	q := tx.WithContext(ctx).Table("users").Where("id = ?", userID)
	if dialectorName(tx) != "sqlite3" {
		// SQLite's BEGIN does not support SELECT ... FOR UPDATE; rely on
		// the writer transaction's serialised semantics instead.
		q = q.Clauses(forUpdateClause(dialectorName(tx)))
	}
	if err := q.First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, biz.ErrAccountNotFound
		}
		return nil, err
	}
	return &biz.Account{
		UserID:       userID,
		Username:     user.Username,
		DisplayName:  user.DisplayName,
		Group:        user.Group,
		Balance:      user.Balance,
		UsedAmount:   user.UsedAmount,
		RequestCount: user.RequestCount,
		FrozenAmount: user.FrozenAmount,
		Status:       user.Status,
	}, nil
}

func int64ToString(v int64) string {
	return strconv.FormatInt(v, 10)
}
