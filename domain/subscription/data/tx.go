package data

import (
	"context"

	"micro-one-api/domain/subscription/biz"
	"micro-one-api/platform/database/xdb"

	"gorm.io/gorm"
)

// gormTx wraps a *gorm.DB so it satisfies biz.Tx without leaking the
// storage driver into the biz layer (code-review 2026-07-30 domain-L1 /
// billing-M6). It is constructed only here, in data, which is the layer
// that owns the *gorm.DB handle.
type gormTx struct{ db *gorm.DB }

// DB implements biz.Tx. The return type is the opaque interface{} that
// biz declares, keeping the gorm type out of biz's import graph. The
// concrete value is recovered via [txDB] on the data side.
func (t *gormTx) DB() any { return t.db }

// txDB extracts the underlying *gorm.DB from a biz.Tx constructed by this
// package. It panics if the handle was not produced here (a programming
// error: every biz.Tx passed into a data repo method must originate from a
// TxRunner in this same package).
func txDB(tx biz.Tx) *gorm.DB {
	if tx == nil {
		return nil
	}
	return tx.DB().(*gorm.DB)
}

// runner is the data-owned implementation of biz.TxRunner. It wraps gorm's
// db.Transaction so the biz layer can open/commit/rollback a unit of work
// without importing gorm.
type runner struct{ db *gorm.DB }

// NewTxRunner builds a biz.TxRunner backed by the repository's database.
func NewTxRunner(r *Repository) biz.TxRunner { return &runner{db: r.db} }

// RunInTx runs fn inside a database transaction. gorm commits when fn
// returns nil and rolls back on any non-nil error, so the biz callback only
// has to signal success/failure — it never manages Begin/Commit/Rollback.
//
// The transaction is retried on SQLite write contention ("database is
// locked"): the assign/change flows this runner serves read the active
// subscription before writing it, so on the shared-file SQLite topology
// (every service on one database) a concurrent cross-service commit between
// the read and the first write stales the WAL snapshot and SQLite fails the
// upgrade immediately (SQLITE_BUSY_SNAPSHOT; busy_timeout cannot fix a stale
// snapshot). A failed attempt has committed nothing and the callback re-reads
// its guards each run, so replaying is safe. On MySQL the retry never fires.
func (r *runner) RunInTx(ctx context.Context, fn func(ctx context.Context, tx biz.Tx) error) error {
	return xdb.RetryTxOnBusy(ctx, r.db, 3, func(tx *gorm.DB) error {
		return fn(ctx, &gormTx{db: tx})
	})
}
