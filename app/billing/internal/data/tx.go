package data

import (
	"context"

	"micro-one-api/domain/subscription/biz"

	"gorm.io/gorm"
)

// gormTx wraps a *gorm.DB so it satisfies subscriptionbiz.Tx without leaking
// the storage driver into the biz layer (code-review 2026-07-30 billing-M6 /
// domain-L1). It is constructed only here, in data, which is the layer that
// owns the *gorm.DB handle.
type gormTx struct{ db *gorm.DB }

// DB implements subscriptionbiz.Tx. The return type is the opaque interface{}
// that biz declares, keeping the gorm type out of biz's import graph.
func (t *gormTx) DB() any { return t.db }

// txDB extracts the underlying *gorm.DB from a subscriptionbiz.Tx constructed
// by this package.
func txDB(tx biz.Tx) *gorm.DB {
	if tx == nil {
		return nil
	}
	return tx.DB().(*gorm.DB)
}

// runner is the data-owned implementation of subscriptionbiz.TxRunner.
type runner struct{ db *gorm.DB }

// NewTxRunner builds a subscriptionbiz.TxRunner backed by the billing Data's
// database.
func NewTxRunner(d *Data) biz.TxRunner {
	if d == nil {
		return nil
	}
	return &runner{db: d.db}
}

// RunInTx runs fn inside a database transaction. gorm commits when fn returns
// nil and rolls back on any non-nil error.
func (r *runner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx biz.Tx) error) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(ctx, &gormTx{db: tx})
	})
}
