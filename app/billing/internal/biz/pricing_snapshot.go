package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	subscriptionbiz "micro-one-api/domain/subscription/biz"
	"micro-one-api/pkg/jsonx"
)

// PricingSnapshotVersion is the hash payload format version. Bumping it
// changes every config_hash so a re-formatted snapshot never collides with a
// v1 row (migration 088, token-usage-billing-semantics-remediation §6.3).
const PricingSnapshotVersion int32 = 1

// PricingSnapshot is the immutable pricing evidence for a consume ledger row:
// the exact per-bucket unit prices, group ratio and cache-creation mode the
// pricing function consumed. Snapshots are deduplicated by ConfigHash and
// claimed in the same transaction as the ledger insert, so historical
// re-pricing no longer depends on the mutable system_options.ModelPrice map.
//
// Only the ModelPrice path produces snapshots: ratio-priced models have no
// per-bucket unit prices to freeze, so their ledger rows keep an empty
// pricing_config_hash (their prices stay unknown by design, §8.2).
type PricingSnapshot struct {
	ID uint
	// ConfigHash is sha256 over the canonical snapshot payload; identical
	// charge inputs collapse to a single row.
	ConfigHash string
	// ModelName is the normalized pricing key the ModelPrice lookup used.
	ModelName string
	// The five EFFECTIVE per-token prices actually charged: CacheReadPrice
	// already carries the InputPrice fallback and unpriced cache-creation
	// buckets are zero, so two configurations that charge identically hash
	// identically.
	InputPrice           float64
	OutputPrice          float64
	CacheReadPrice       float64
	CacheCreation5mPrice float64
	CacheCreation1hPrice float64
	GroupRatio           float64
	// CacheCreationMode participates in the hash: observe vs charge settles a
	// different amount for the same prices, so they are distinct evidence.
	CacheCreationMode string
	SnapshotVersion   int32
	CreatedAt         time.Time
}

// pricingSnapshotHashInput is the canonical hashing payload. Field order is
// fixed by the struct definition; float64 values are encoded by encoding/json
// in their shortest round-trip form, so the hash is deterministic for the
// same input bits across runs and platforms.
type pricingSnapshotHashInput struct {
	SnapshotVersion int32   `json:"v"`
	Model           string  `json:"model"`
	Input           float64 `json:"in"`
	Output          float64 `json:"out"`
	CacheRead       float64 `json:"cr"`
	Creation5m      float64 `json:"c5"`
	Creation1h      float64 `json:"c1"`
	GroupRatio      float64 `json:"grp"`
	Mode            string  `json:"mode"`
}

// buildPricingSnapshot freezes the pricing evidence of one request. The price
// resolution MUST mirror calculateCanonicalCost: cache-read falls back to
// InputPrice when unconfigured, and unpriced cache-creation buckets charge
// zero — storing the effective values is what makes the snapshot reproduce
// the historical charge.
func buildPricingSnapshot(modelKey string, price ModelPrice, multiplier float64, mode CacheCreationMode) *PricingSnapshot {
	cacheReadPrice := price.InputPrice
	if price.CacheReadPrice != nil {
		cacheReadPrice = *price.CacheReadPrice
	}
	creation5m := 0.0
	if price.CacheCreation5mPrice != nil {
		creation5m = *price.CacheCreation5mPrice
	}
	creation1h := 0.0
	if price.CacheCreation1hPrice != nil {
		creation1h = *price.CacheCreation1hPrice
	}
	s := &PricingSnapshot{
		ModelName:            modelKey,
		InputPrice:           price.InputPrice,
		OutputPrice:          price.OutputPrice,
		CacheReadPrice:       cacheReadPrice,
		CacheCreation5mPrice: creation5m,
		CacheCreation1hPrice: creation1h,
		GroupRatio:           multiplier,
		CacheCreationMode:    string(mode),
		SnapshotVersion:      PricingSnapshotVersion,
	}
	payload, err := jsonx.Marshal(pricingSnapshotHashInput{
		SnapshotVersion: s.SnapshotVersion,
		Model:           s.ModelName,
		Input:           s.InputPrice,
		Output:          s.OutputPrice,
		CacheRead:       s.CacheReadPrice,
		Creation5m:      s.CacheCreation5mPrice,
		Creation1h:      s.CacheCreation1hPrice,
		GroupRatio:      s.GroupRatio,
		Mode:            s.CacheCreationMode,
	})
	if err != nil {
		// Non-finite floats are rejected during pricing normalization, but keep
		// the low-level builder collision-safe as well: hashing their exact IEEE
		// bits is deterministic and never aliases unrelated invalid configs.
		fallback := fmt.Sprintf("%d|%q|%016x|%016x|%016x|%016x|%016x|%016x|%q",
			s.SnapshotVersion, s.ModelName,
			math.Float64bits(s.InputPrice), math.Float64bits(s.OutputPrice),
			math.Float64bits(s.CacheReadPrice), math.Float64bits(s.CacheCreation5mPrice),
			math.Float64bits(s.CacheCreation1hPrice), math.Float64bits(s.GroupRatio),
			s.CacheCreationMode)
		sum := sha256.Sum256([]byte(fallback))
		s.ConfigHash = hex.EncodeToString(sum[:])
		return s
	}
	sum := sha256.Sum256(payload)
	s.ConfigHash = hex.EncodeToString(sum[:])
	return s
}

// GetPricingSnapshot returns the snapshot a ledger row's pricing_config_hash
// references. Empty hash (legacy rows, ratio-priced models) returns
// (nil, nil): there is deliberately no evidence to show.
func (uc *BillingUsecase) GetPricingSnapshot(ctx context.Context, configHash string) (*PricingSnapshot, error) {
	if uc == nil || uc.pricingSnapshotRepo == nil || configHash == "" {
		return nil, nil
	}
	return uc.pricingSnapshotRepo.GetPricingSnapshotByHash(ctx, configHash)
}

// claimPricingSnapshotInTx claims the snapshot inside the caller's commit
// transaction; nil snapshot/repo (ratio-priced models, unwired deployments)
// is a no-op.
func (uc *BillingUsecase) claimPricingSnapshotInTx(ctx context.Context, tx subscriptionbiz.Tx, snapshot *PricingSnapshot) error {
	if uc == nil || snapshot == nil || uc.pricingSnapshotRepo == nil {
		return nil
	}
	return uc.pricingSnapshotRepo.ClaimPricingSnapshotInTx(ctx, tx, snapshot)
}

// claimPricingSnapshot is the own-transaction variant for the legacy commit
// path, which has no shared transaction to join.
func (uc *BillingUsecase) claimPricingSnapshot(ctx context.Context, snapshot *PricingSnapshot) error {
	if uc == nil || snapshot == nil || uc.pricingSnapshotRepo == nil {
		return nil
	}
	return uc.pricingSnapshotRepo.ClaimPricingSnapshot(ctx, snapshot)
}
