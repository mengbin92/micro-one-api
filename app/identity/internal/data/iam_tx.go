package data

import (
	"context"
	"database/sql"
	"errors"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"micro-one-api/app/identity/internal/biz"
	"micro-one-api/platform/database/xdb"
	applogger "micro-one-api/platform/logging"
)

type iamTx struct {
	db               *gorm.DB
	owner            *Data
	writable, active bool
	busy             error
	mutated, audited bool
}

func (t *iamTx) Handle() any { return t }

type iamRunner struct{ data *Data }

func NewIAMTxRunner(d *Data) biz.IAMTxRunner { return &iamRunner{data: d} }

func (r *iamRunner) RunIAMWrite(ctx context.Context, fn func(context.Context, biz.IAMTx) error) error {
	if r.data == nil || r.data.db == nil || fn == nil {
		return biz.ErrIAMDependencyUnavailable
	}
	var callbackErr error
	err := xdb.RetryTxOnBusy(ctx, r.data.db, 5, func(db *gorm.DB) error {
		callbackErr = nil
		// ponytail: the single policy row limits management write throughput;
		// split by context only after measured contention becomes a bottleneck.
		if db.Dialector.Name() == "sqlite" {
			result := db.Exec("UPDATE iam_policy_state SET policy_revision = policy_revision WHERE id = 1")
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				callbackErr = biz.ErrIAMDependencyUnavailable
				return callbackErr
			}
		}
		var policy iamPolicyModel
		query := db
		if db.Dialector.Name() != "sqlite" {
			query = query.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if err := query.First(&policy, 1).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				callbackErr = biz.ErrIAMDependencyUnavailable
				return callbackErr
			}
			return err
		}
		if err := policy.toBiz().Validate(); err != nil {
			callbackErr = biz.ErrIAMCutoverBlocked
			return callbackErr
		}
		tx := &iamTx{db: db, owner: r.data, writable: true, active: true}
		defer func() { tx.active = false }()
		callbackErr = fn(ctx, tx)
		if callbackErr == nil && tx.mutated && !tx.audited {
			callbackErr = biz.ErrIAMInvalidRelation
		}
		// Repository methods expose only biz errors. Keep busy errors inside data so
		// RetryTxOnBusy can replay a failed attempt without leaking driver types.
		if callbackErr != nil && tx.busy != nil {
			callbackErr = nil
			return tx.busy
		}
		return callbackErr
	})
	if err != nil && callbackErr != nil {
		return callbackErr
	}
	return iamStorageError(nil, err)
}

func (r *iamRunner) ReadIAMSnapshot(ctx context.Context, fn func(context.Context, biz.IAMTx) error) error {
	if r.data == nil || r.data.db == nil || fn == nil {
		return biz.ErrIAMDependencyUnavailable
	}
	var callbackErr error
	err := r.data.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		tx := &iamTx{db: db, owner: r.data, active: true}
		defer func() { tx.active = false }()
		callbackErr = fn(ctx, tx)
		return callbackErr
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil && callbackErr != nil {
		return callbackErr
	}
	return iamStorageError(nil, err)
}

func iamStorageError(tx *iamTx, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return biz.ErrIAMNotFound
	}
	if tx != nil && xdb.IsSQLiteBusy(err) {
		tx.busy = err
	}
	applogger.Log.Error("IAM storage operation failed", zap.Error(err))
	return biz.ErrIAMDependencyUnavailable
}

func iamDB(ctx context.Context, d *Data, handle biz.IAMTx, write bool) (*iamTx, error) {
	if handle == nil {
		return nil, biz.ErrIAMDependencyUnavailable
	}
	tx, ok := handle.Handle().(*iamTx)
	if !ok || tx == nil || !tx.active || tx.owner != d || (write && !tx.writable) {
		return nil, biz.ErrIAMDependencyUnavailable
	}
	// Bind the current operation's deadline, retaining the same transaction.
	tx.db = tx.db.WithContext(ctx)
	return tx, nil
}
