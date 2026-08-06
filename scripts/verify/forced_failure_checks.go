// Command forced_failure_checks implements the v0.17 roadmap P1.3 ledger-side
// assertions for the post-release forced-failure verification:
//
//  1. 账单只落一次 — exactly one consume ledger row per (reference_id,
//     cost_source) and no duplicate ledger_dedupe_key for the reservation;
//  2. 归属正确 — the serving-source dimension (channel_id XOR
//     subscription_account_id) matches -serving-kind and the other dimension
//     is zero, so credential/usage/cost/bill belong only to the source that
//     actually served;
//  3. (optional) the charged quota matches -expect-charged-quota.
//
// Usage:
//
//	go run ./scripts/verify/forced_failure_checks.go \
//	    -dsn "$DATABASE_DSN" \
//	    -reservation-id <reservation_id> \
//	    -serving-kind channel \
//	    [-expect-charged-quota 123]
//
// Or locate the reservation created after a baseline scan (used when the
// client cannot know the server-generated request id in advance):
//
//	go run ./scripts/verify/forced_failure_checks.go \
//	    -dsn "$DATABASE_DSN" \
//	    -user <user_id> -after-reservation-id <max_id_at_baseline> \
//	    -serving-kind channel
//
// Exit code: 0 pass, 1 assertion failed, 2 configuration/runtime error.
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"os"

	_ "github.com/go-sql-driver/mysql"
)

func main() {
	var (
		dsn            string
		reservation    string
		user           string
		afterID        int64
		servingKind    string
		expectQuota    int64
		maxReservation bool
	)
	flag.StringVar(&dsn, "dsn", "", "MySQL DSN (default: $RECONCILE_DSN, then $DATABASE_DSN)")
	flag.StringVar(&reservation, "reservation-id", "", "billing reservation id to verify")
	flag.StringVar(&user, "user", "", "user id to scan for a new reservation")
	flag.Int64Var(&afterID, "after-reservation-id", 0, "baseline MAX(id) of billing_reservations; finds the next new reservation for -user")
	flag.StringVar(&servingKind, "serving-kind", "channel", "expected serving source kind: channel | subscription")
	flag.Int64Var(&expectQuota, "expect-charged-quota", 0, "expected total charged quota for the reservation (0 = skip)")
	flag.BoolVar(&maxReservation, "max-reservation-id", false, "print MAX(id) of billing_reservations for -user (baseline scan) and exit")
	flag.Parse()

	if dsn == "" {
		dsn = os.Getenv("RECONCILE_DSN")
	}
	if dsn == "" {
		dsn = os.Getenv("DATABASE_DSN")
	}
	if dsn == "" {
		fail("no DSN provided (set -dsn, RECONCILE_DSN or DATABASE_DSN)")
	}
	if servingKind != "channel" && servingKind != "subscription" {
		fail("serving-kind must be channel or subscription, got %q", servingKind)
	}
	if maxReservation {
		if user == "" {
			fail("-max-reservation-id requires -user")
		}
	} else if reservation == "" && (user == "" || afterID <= 0) {
		fail("provide -reservation-id, or -user with -after-reservation-id")
	}

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		fail("open db: %v", err)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fail("ping db: %v", err)
	}

	if maxReservation {
		var maxID int64
		if err := db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM billing_reservations WHERE user_id = ?", user).Scan(&maxID); err != nil {
			fail("scan max reservation id: %v", err)
		}
		fmt.Println(maxID)
		return
	}

	if reservation == "" {
		reservation, err = findNewReservation(db, user, afterID)
		if err != nil {
			fail("find new reservation: %v", err)
		}
		if reservation == "" {
			fail("no new reservation found for user %s after id %d — did the fallback request bill?", user, afterID)
		}
		fmt.Printf("located reservation %s\n", reservation)
	}

	ok := verify(db, reservation, servingKind, expectQuota, expectQuota > 0)
	if !ok {
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS — attribution correct and billing landed exactly once")
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(2)
}

func findNewReservation(db *sql.DB, user string, afterID int64) (string, error) {
	var id string
	// The committed reservation is the one that actually served: failed
	// candidate attempts create reservations that get released (no consume
	// ledger), so they must not be picked for attribution checks.
	err := db.QueryRow(
		"SELECT reservation_id FROM billing_reservations "+
			"WHERE user_id = ? AND id > ? AND status = 'committed' ORDER BY id ASC LIMIT 1",
		user, afterID,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return id, err
}

type ledgerGroup struct {
	costSource string
	count      int64
	dedupeKeys []string
	charged    int64
	channelID  int64
	subAcctID  int64
}

func verify(db *sql.DB, reservation, servingKind string, expectQuota int64, hasExpectQ bool) bool {
	ok := true

	rows, err := db.Query(
		"SELECT reference_id, cost_source, COUNT(*), "+
			"GROUP_CONCAT(ledger_dedupe_key), COALESCE(SUM(ABS(amount)), 0), "+
			"COALESCE(MAX(channel_id), 0), COALESCE(MAX(subscription_account_id), 0) "+
			"FROM billing_ledgers WHERE type = 'consume' AND reference_id = ? "+
			"GROUP BY reference_id, cost_source",
		reservation,
	)
	if err != nil {
		fail("query ledgers: %v", err)
	}
	defer rows.Close()

	groups := []ledgerGroup{}
	seenKeys := map[string]bool{}
	for rows.Next() {
		var g ledgerGroup
		var reference string
		var keys string
		if err := rows.Scan(&reference, &g.costSource, &g.count, &keys, &g.charged, &g.channelID, &g.subAcctID); err != nil {
			fail("scan ledgers: %v", err)
		}
		_ = reference // constant per query
		g.dedupeKeys = splitKeys(keys)
		groups = append(groups, g)
	}
	if len(groups) == 0 {
		fmt.Printf("  ✗ no consume ledger rows for reservation %s\n", reservation)
		return false
	}

	fmt.Printf("== ledger attribution & single-billing check (reservation %s) ==\n", reservation)
	totalCharged := int64(0)
	for _, g := range groups {
		totalCharged += g.charged
		fmt.Printf("  cost_source=%-12s rows=%d charged=%d channel_id=%d subscription_account_id=%d\n",
			g.costSource, g.count, g.charged, g.channelID, g.subAcctID)
		if g.count != 1 {
			fmt.Printf("  ✗ cost_source %s has %d rows — expected exactly 1 (账单只落一次)\n", g.costSource, g.count)
			ok = false
		}
		for _, k := range g.dedupeKeys {
			if seenKeys[k] {
				fmt.Printf("  ✗ duplicate ledger_dedupe_key %q — repeated charge\n", k)
				ok = false
			}
			seenKeys[k] = true
		}
	}

	// Attribution: exactly one dimension participated and it matches
	// the expected serving source kind.
	channelRows := 0
	subRows := 0
	for _, g := range groups {
		if g.channelID > 0 {
			channelRows++
		}
		if g.subAcctID > 0 {
			subRows++
		}
	}
	switch servingKind {
	case "channel":
		if channelRows == 0 || subRows > 0 {
			fmt.Printf("  ✗ expected serving source kind=channel (channel rows=%d, subscription rows=%d)\n", channelRows, subRows)
			ok = false
		} else {
			fmt.Printf("  ✓ all ledger rows attributed to channel (channel rows=%d, subscription rows=0)\n", channelRows)
		}
	case "subscription":
		if subRows == 0 || channelRows > 0 {
			fmt.Printf("  ✗ expected serving source kind=subscription (subscription rows=%d, channel rows=%d)\n", subRows, channelRows)
			ok = false
		} else {
			fmt.Printf("  ✓ all ledger rows attributed to subscription account (subscription rows=%d, channel rows=0)\n", subRows)
		}
	}

	if hasExpectQ && totalCharged != expectQuota {
		fmt.Printf("  ✗ total charged %d != expected %d\n", totalCharged, expectQuota)
		ok = false
	}
	fmt.Printf("  total charged quota: %d\n", totalCharged)
	return ok
}

func splitKeys(keys string) []string {
	if keys == "" {
		return nil
	}
	out := []string{}
	start := 0
	for i := 0; i < len(keys); i++ {
		if keys[i] == ',' {
			out = append(out, keys[start:i])
			start = i + 1
		}
	}
	return append(out, keys[start:])
}
