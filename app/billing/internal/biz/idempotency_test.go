package biz

import (
	"errors"
	"strings"
	"testing"
)

// TestLedgerDedupeKeyFor_Format verifies the v0.18 P0 ledger idempotency key
// format: `{action}:{user_id}:{request_id}`. The format is distinct from the
// legacy `{reference_id}:{type}:legacy` fallback, so explicit keys never
// collide with historical rows.
func TestLedgerDedupeKeyFor_Format(t *testing.T) {
	got := ledgerDedupeKeyFor(DedupeActionPurchase, "42", "order-abc")
	if got != "purchase:42:order-abc" {
		t.Fatalf("purchase key = %q, want %q", got, "purchase:42:order-abc")
	}
	got = ledgerDedupeKeyFor(DedupeActionTopup, "7", "topup-1")
	if got != "topup:7:topup-1" {
		t.Fatalf("topup key = %q, want %q", got, "topup:7:topup-1")
	}
}

// TestLedgerDedupeKeyFor_UserIDTruncation verifies the user_id prefix is
// truncated so the total key stays within the ledger_dedupe_key column width
// (VARCHAR(160)): action(≤9) + ':' + user(48) + ':' + request(≤100) = 159.
func TestLedgerDedupeKeyFor_UserIDTruncation(t *testing.T) {
	longUser := strings.Repeat("u", 200)
	got := ledgerDedupeKeyFor(DedupeActionPurchase, longUser, strings.Repeat("r", 100))
	if len(got) > 160 {
		t.Fatalf("key length %d exceeds column width 160: %q", len(got), got)
	}
	if !strings.HasPrefix(got, "purchase:"+strings.Repeat("u", 48)+":") {
		t.Fatalf("user_id not truncated to 48 chars: %q", got)
	}
}

// TestLedgerDedupeKeyFor_EmptyRequestIDGetsDistinctAutoKeys verifies the
// legacy-client path: an empty request id yields a per-call auto key, so two
// key-less purchases never collide (and never collide with legacy rows).
func TestLedgerDedupeKeyFor_EmptyRequestIDGetsDistinctAutoKeys(t *testing.T) {
	k1 := ledgerDedupeKeyFor(DedupeActionPurchase, "42", "")
	k2 := ledgerDedupeKeyFor(DedupeActionPurchase, "42", "")
	if k1 == k2 {
		t.Fatalf("auto keys must be distinct per call, got %q twice", k1)
	}
	if !strings.HasPrefix(k1, "purchase:42:auto:") {
		t.Fatalf("empty request id must yield auto key, got %q", k1)
	}
}

// TestIsDuplicateKeyError covers the three drivers' unique-constraint error
// shapes the idempotency gate maps to ErrDuplicateRequest.
func TestIsDuplicateKeyError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"mysql 1062", errors.New("Error 1062: Duplicate entry 'purchase:1:req' for key 'idx_ledger_dedupe_key'"), true},
		{"postgres 23505", errors.New(`pq: duplicate key value violates unique constraint "idx_ledger_dedupe_key"`), true},
		{"sqlite", errors.New("UNIQUE constraint failed: billing_ledger_dedupe_claims.ledger_dedupe_key"), true},
		{"claim sentinel", ErrLedgerDedupeExists, true},
		{"unrelated", errors.New("insufficient quota"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := isDuplicateKeyError(c.err); got != c.want {
			t.Fatalf("%s: isDuplicateKeyError(%v) = %v, want %v", c.name, c.err, got, c.want)
		}
	}
}
