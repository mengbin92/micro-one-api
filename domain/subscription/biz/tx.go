package biz

import "context"

// Tx is an opaque transaction handle. It is declared in biz (the layer that
// orchestrates transactions) but never imports a storage driver: the concrete
// representation is owned by data, which constructs it from the underlying
// *gorm.DB. This keeps the inversion seam in biz free of storage primitives,
// satisfying AGENTS.md's "biz 永不讲存储客户端" rule (code-review 2026-07-30
// domain-L1 / billing-M6).
//
// A Tx is passed into the *InTx repo methods and to SubscriptionPrimatives
// methods that must run inside the caller's transaction. Implementations (in
// data) are constructed via [TxUnwrap], which is the single extractor; biz
// can neither construct nor introspect a Tx without going through data.
type Tx interface {
	// DB returns the underlying transaction handle. The concrete return
	// type is intentionally interface{} (kept opaque here) so biz does not
	// import the storage driver. Data-layer repo implementations recover
	// the concrete handle via the [TxUnwrap] helper defined alongside them.
	//
	// This exported method replaces the earlier sealed-interface design
	// (unexported underlyingTx): Go does not allow a type in a different
	// package to implement an interface with an unexported method, so the
	// data layer could never have satisfied Tx at all. Returning an
	// opaque interface{} keeps the storage driver out of biz's import
	// graph while remaining implementable from data.
	DB() any
}

// TxRunner runs a function inside a storage transaction. biz calls RunInTx
// to open/commit/rollback the transaction boundary without importing the
// driver; data implements it (wrapping gorm's db.Transaction / Begin+Commit).
//
// The callback receives a Tx it passes to every *InTx repo method in the
// same unit of work. Returning a non-nil error rolls the transaction back.
// Returning nil commits it.
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx Tx) error) error
}
