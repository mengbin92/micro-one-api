// Command checks implements the repeatable v0.17 roadmap P1.2 reconciliation
// DB checks against the billing database. It is invoked by reconcile.sh (or
// directly with go run) and exits 0 when no discrepancies are found, 1 when
// discrepancies exist, and 2 on configuration / runtime errors.
//
// Usage:
//
//	go run ./scripts/reconcile/checks.go \
//	    -dsn "$DATABASE_DSN" \
//	    [-since 24h] \
//	    [-vendor-bill vendor_bill.csv -vendor-manifest manifest.json -vendor-out /absolute/report.json] \
//	    [-unpriced-max 0] \
//	    [-cache-hit-min 0.5]
//
// DSN resolution: -dsn flag, then RECONCILE_DSN, then DATABASE_DSN. Only the
// MySQL driver (go-sql-driver) is supported; Postgres/SQLite are out of scope
// for the production billing reconciliation period check.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type report struct {
	lines []string
	fail  bool
}

func (r *report) add(format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *report) discrepancy(format string, args ...any) {
	r.fail = true
	r.add("  ✗ "+format, args...)
}

func (r *report) ok(format string, args ...any) {
	r.add("  ✓ "+format, args...)
}

type options struct {
	dsn             string
	since           time.Duration
	vendorBill      string
	vendorManifest  string
	vendorOutput    string
	vendorTolerance float64
	unpricedMax     int
	cacheHitMin     float64
}

func main() {
	var (
		opts      options
		dsnFlag   string
		sinceFlag string
	)
	flag.StringVar(&dsnFlag, "dsn", "", "MySQL DSN (default: $RECONCILE_DSN, then $DATABASE_DSN)")
	flag.StringVar(&sinceFlag, "since", "24h", "reporting window, e.g. 24h / 7d")
	flag.StringVar(&opts.vendorBill, "vendor-bill", "", "optional vendor invoice CSV path")
	flag.Float64Var(&opts.vendorTolerance, "vendor-tolerance", 0, "deprecated; external tolerances must be fixed in the evidenced report manifest")
	flag.StringVar(&opts.vendorManifest, "vendor-manifest", "", "fixed supplier scope/watermark/account mapping manifest JSON")
	flag.StringVar(&opts.vendorOutput, "vendor-out", "", "absolute new external reconciliation JSON report")
	flag.IntVar(&opts.unpricedMax, "unpriced-max", 0, "max tolerated counted-but-unbilled cache-creation ledger rows in window")
	flag.Float64Var(&opts.cacheHitMin, "cache-hit-min", 0, "warn when cache hit rate (口径 §2.5) is below this fraction; 0 disables")
	flag.Parse()

	opts.dsn = resolveDSN(dsnFlag)
	if opts.dsn == "" {
		fmt.Fprintln(os.Stderr, "error: no DSN provided (set -dsn, RECONCILE_DSN or DATABASE_DSN)")
		os.Exit(2)
	}
	since, err := parseSince(sinceFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: invalid -since %q: %v\n", sinceFlag, err)
		os.Exit(2)
	}
	opts.since = since

	db, err := sql.Open("mysql", opts.dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open db: %v\n", err)
		os.Exit(2)
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "error: ping db: %v\n", err)
		os.Exit(2)
	}

	rep := &report{}
	runChecks(db, opts, rep)

	fmt.Println("== billing reconciliation DB checks ==")
	fmt.Printf("window: last %s (since %s)\n", opts.since, time.Now().Add(-opts.since).Format("2006-01-02 15:04:05"))
	for _, l := range rep.lines {
		fmt.Println(l)
	}
	if rep.fail {
		fmt.Println("\nRESULT: FAIL (discrepancies found — investigate and re-run)")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: PASS (no discrepancies)")
}

func resolveDSN(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	if v := os.Getenv("RECONCILE_DSN"); v != "" {
		return v
	}
	return os.Getenv("DATABASE_DSN")
}

func parseSince(s string) (time.Duration, error) {
	if s == "" {
		return 24 * time.Hour, nil
	}
	if before, ok := strings.CutSuffix(s, "d"); ok {
		days := before
		var n int
		if _, err := fmt.Sscanf(days, "%d", &n); err != nil || n <= 0 {
			return 0, fmt.Errorf("expected e.g. 24h or 7d")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func runChecks(db *sql.DB, opts options, rep *report) {
	cutoff := time.Now().Add(-opts.since)

	rep.add("== ledger dedupe key ==")
	checkDedupeKeys(db, rep)

	rep.add("== ledger dedupe claim coverage (migration 078 gate) ==")
	checkClaimCoverage(db, rep)

	rep.add("== cache-creation counted-but-unbilled (since %s) ==", opts.since.String())
	checkUnbilledCacheCreation(db, cutoff, opts.unpricedMax, rep)

	rep.add("== cache hit rate (口径: cache_read / (cache_read + cache_creation)) ==")
	checkCacheHitRate(db, cutoff, opts.cacheHitMin, rep)

	rep.add("== configured ledger margin estimate, not supplier-confirmed (since %s) ==", opts.since.String())
	checkGrossMargin(db, cutoff, rep)

	rep.add("== token buckets by provider family (since %s) ==", opts.since.String())
	printBucketTotals(db, cutoff, rep)

	if opts.vendorBill != "" {
		rep.add("== vendor bill comparison ==")
		checkVendorBill(db, cutoff, opts, rep)
	}
}

func checkDedupeKeys(db *sql.DB, rep *report) {
	var empty int64
	if err := db.QueryRow("SELECT COUNT(*) FROM billing_ledgers WHERE ledger_dedupe_key = ''").Scan(&empty); err != nil {
		rep.discrepancy("count empty dedupe keys: %v", err)
		return
	}
	if empty > 0 {
		rep.discrepancy("empty ledger_dedupe_key rows: %d (all consume/refund rows must carry a dedupe key)", empty)
	} else {
		rep.ok("empty ledger_dedupe_key rows: 0")
	}

	rows, err := db.Query("SELECT ledger_dedupe_key, COUNT(*) FROM billing_ledgers " +
		"WHERE ledger_dedupe_key != '' AND ledger_dedupe_key NOT LIKE '%:legacy' " +
		"GROUP BY ledger_dedupe_key HAVING COUNT(*) > 1")
	if err != nil {
		rep.discrepancy("query duplicate dedupe keys: %v", err)
		return
	}
	defer rows.Close()
	dups := 0
	for rows.Next() {
		var key string
		var n int64
		if err := rows.Scan(&key, &n); err != nil {
			rep.discrepancy("scan duplicate dedupe key: %v", err)
			return
		}
		dups++
		rep.discrepancy("duplicate non-legacy ledger_dedupe_key %q x%d", key, n)
	}
	if dups == 0 {
		rep.ok("duplicate non-legacy dedupe keys: 0")
	}

	var legacy int64
	if err := db.QueryRow("SELECT COUNT(*) FROM billing_ledgers WHERE ledger_dedupe_key LIKE '%:legacy'").Scan(&legacy); err == nil {
		rep.add("  ℹ legacy backfilled dedupe keys: %d (informational)", legacy)
	}
}

// checkClaimCoverage verifies the migration-078 idempotency gate end to end:
// every ledger row carrying a dedupe key must have a matching claim in
// billing_ledger_dedupe_claims, and every claim must resolve to a ledger.
// A ledger without a claim is a window-period orphan (old code wrote the
// ledger before the claim gate was live); once phase3 partitioning drops the
// global unique index on billing_ledgers, claims are the only idempotency
// guard, so orphan ledgers must fail the check.
func checkClaimCoverage(db *sql.DB, rep *report) {
	var orphanLedgers int64
	err := db.QueryRow(
		"SELECT COUNT(*) FROM billing_ledgers l " +
			"LEFT JOIN billing_ledger_dedupe_claims c ON l.ledger_dedupe_key = c.ledger_dedupe_key " +
			"WHERE l.ledger_dedupe_key <> '' AND c.ledger_dedupe_key IS NULL",
	).Scan(&orphanLedgers)
	if err != nil {
		rep.discrepancy("count ledgers without claim: %v", err)
		return
	}
	if orphanLedgers > 0 {
		rep.discrepancy("ledgers without claim: %d (window-period orphans; backfill claims before phase3 partitioning drops the unique index)", orphanLedgers)
	} else {
		rep.ok("ledgers without claim: 0")
	}

	var orphanClaims int64
	err = db.QueryRow(
		"SELECT COUNT(*) FROM billing_ledger_dedupe_claims c " +
			"LEFT JOIN billing_ledgers l ON l.ledger_dedupe_key = c.ledger_dedupe_key " +
			"WHERE l.ledger_dedupe_key IS NULL",
	).Scan(&orphanClaims)
	if err != nil {
		rep.discrepancy("count claims without ledger: %v", err)
		return
	}
	if orphanClaims > 0 {
		rep.discrepancy("claims without ledger: %d (claims must always resolve to a ledger row)", orphanClaims)
	} else {
		rep.ok("claims without ledger: 0")
	}
}

func checkUnbilledCacheCreation(db *sql.DB, cutoff time.Time, max int, rep *report) {
	var rows int64
	var tokens int64
	err := db.QueryRow(
		"SELECT COUNT(*), COALESCE(SUM(cache_creation_5m_tokens + cache_creation_1h_tokens), 0) "+
			"FROM billing_ledgers "+
			"WHERE type = 'consume' AND created_at >= ? "+
			"AND (cache_creation_5m_tokens + cache_creation_1h_tokens) > 0 "+
			"AND COALESCE(cache_creation_5m_cost, 0) + COALESCE(cache_creation_1h_cost, 0) = 0",
		cutoff,
	).Scan(&rows, &tokens)
	if err != nil {
		rep.discrepancy("count counted-but-unbilled rows: %v", err)
		return
	}
	if rows > int64(max) {
		rep.discrepancy("counted-but-unbilled cache-creation rows: %d (tokens %d) — cache_creation 桶存在 token 但两桶成本均为 0，疑似未配价", rows, tokens)
		return
	}
	rep.ok("counted-but-unbilled cache-creation rows: %d (tokens %d)", rows, tokens)
}

func checkCacheHitRate(db *sql.DB, cutoff time.Time, min float64, rep *report) {
	var read, cacheable int64
	err := db.QueryRow(
		"SELECT COALESCE(SUM(cache_read_tokens), 0), "+
			"COALESCE(SUM(cache_read_tokens + cache_creation_5m_tokens + cache_creation_1h_tokens), 0) "+
			"FROM billing_ledgers WHERE type = 'consume' AND created_at >= ?",
		cutoff,
	).Scan(&read, &cacheable)
	if err != nil {
		rep.discrepancy("compute cache hit rate: %v", err)
		return
	}
	if cacheable <= 0 {
		rep.add("  ℹ no cacheable traffic in window — hit rate n/a")
		return
	}
	rate := float64(read) / float64(cacheable)
	if min > 0 && rate < min {
		rep.discrepancy("cache hit rate %.2f%% below minimum %.0f%% (read=%d, cacheable=%d)", rate*100, min*100, read, cacheable)
		return
	}
	rep.ok("cache hit rate: %.2f%% (read=%d, cacheable=%d)", rate*100, read, cacheable)
}

const ledgerReferenceGroup = "CASE WHEN reference_id <> '' THEN CONCAT('reservation:', reference_id) ELSE CONCAT('ledger:', id) END"

func checkGrossMargin(db *sql.DB, cutoff time.Time, rep *report) {
	var gross int64
	var rows int64
	err := db.QueryRow(
		"SELECT COALESCE(SUM(customer_quota - estimated_quota), 0), COUNT(*) FROM ("+
			"SELECT SUM(ABS(amount)) AS customer_quota, MAX(upstream_cost) AS estimated_quota "+
			"FROM billing_ledgers WHERE type = 'consume' AND created_at >= ? GROUP BY "+ledgerReferenceGroup+", model_name) AS reservations",
		cutoff,
	).Scan(&gross, &rows)
	if err != nil {
		rep.discrepancy("compute gross margin: %v", err)
		return
	}
	if gross < 0 {
		rep.discrepancy("configured ledger margin estimate negative: %d quota over %d ledger rows (售价低于上游成本)", gross, rows)
		return
	}
	rep.ok("configured ledger margin estimate: %d quota over %d ledger rows", gross, rows)
}

type familyTotals struct {
	prompt, completion, cacheRead, creation5m, creation1h, upstreamQuota int64
	rows                                                                 int64
}

func printBucketTotals(db *sql.DB, cutoff time.Time, rep *report) {
	totals := aggregateFamilies(db, cutoff)
	if totals == nil {
		rep.discrepancy("aggregate token buckets: query failed")
		return
	}
	families := make([]string, 0, len(totals))
	for f := range totals {
		families = append(families, f)
	}
	sort.Strings(families)
	rep.add("  %-10s %10s %12s %12s %12s %12s %14s %8s", "family", "prompt", "completion", "cache_read", "creation_5m", "creation_1h", "upstream_quota", "rows")
	for _, f := range families {
		t := totals[f]
		rep.add("  %-10s %10d %12d %12d %12d %12d %14d %8d", f, t.prompt, t.completion, t.cacheRead, t.creation5m, t.creation1h, t.upstreamQuota, t.rows)
	}
}

// aggregateFamilies groups each reservation once by display-only provider family
// (the same low-cardinality heuristic billing uses for metrics).
func aggregateFamilies(db *sql.DB, cutoff time.Time) map[string]*familyTotals {
	rows, err := db.Query(
		"SELECT model_name, MAX(prompt_tokens), MAX(completion_tokens), MAX(cache_read_tokens), "+
			"MAX(cache_creation_5m_tokens), MAX(cache_creation_1h_tokens), MAX(upstream_cost), COUNT(*) "+
			"FROM billing_ledgers WHERE type = 'consume' AND created_at >= ? GROUP BY "+ledgerReferenceGroup+", model_name",
		cutoff,
	)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make(map[string]*familyTotals)
	for rows.Next() {
		var model string
		var p, c, cr, c5, c1, up, n int64
		if err := rows.Scan(&model, &p, &c, &cr, &c5, &c1, &up, &n); err != nil {
			return nil
		}
		f := providerFamilyForModel(model)
		t := out[f]
		if t == nil {
			t = &familyTotals{}
			out[f] = t
		}
		t.prompt += p
		t.completion += c
		t.cacheRead += cr
		t.creation5m += c5
		t.creation1h += c1
		t.upstreamQuota += up
		t.rows++
	}
	return out
}

// checkVendorBill delegates to the billing-owned P2 evidence command. The old
// model-family/DATE(created_at)/consume SUM comparison cannot establish supplier
// scope and is deliberately rejected rather than emitting a false PASS.
func checkVendorBill(_ *sql.DB, _ time.Time, opts options, rep *report) {
	if opts.vendorManifest == "" || opts.vendorOutput == "" || opts.vendorTolerance != 0 {
		rep.discrepancy("vendor comparison requires -vendor-manifest and -vendor-out; set evidenced unit/rounding tolerances in the manifest; legacy family CSV is unsupported")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// #nosec G204 -- The executable/package and flags are fixed; operator file paths are separate argv values, never shell input.
	cmd := exec.CommandContext(ctx, "go", "run", "./app/billing/cmd/vendor-reconcile", "-manifest", opts.vendorManifest, "-vendor-bill", opts.vendorBill, "-out", opts.vendorOutput)
	cmd.Env = append(os.Environ(), "VENDOR_RECONCILE_DSN="+opts.dsn)
	output, err := cmd.CombinedOutput()
	if err != nil {
		rep.discrepancy("external reconciliation did not pass: %s", strings.TrimSpace(string(output)))
		return
	}
	rep.ok("%s", strings.TrimSpace(string(output)))
}

func parseInt64(s string) (int64, error) { return strconv.ParseInt(strings.TrimSpace(s), 10, 64) }

// providerFamilyForModel mirrors the low-cardinality family heuristic used by
// billing metrics (app/billing/internal/biz/billing.go) so the reconcile
// report groups traffic the same way as Prometheus alerts.
func providerFamilyForModel(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "gpt-"), strings.HasPrefix(m, "o1"), strings.HasPrefix(m, "o3"), strings.HasPrefix(m, "o4"), strings.HasPrefix(m, "chatgpt"):
		return "openai"
	case strings.HasPrefix(m, "claude-"):
		return "anthropic"
	case strings.HasPrefix(m, "gemini-"):
		return "google"
	case strings.HasPrefix(m, "glm-"):
		return "zhipu"
	case strings.HasPrefix(m, "deepseek"):
		return "deepseek"
	case strings.HasPrefix(m, "qwen"):
		return "alibaba"
	default:
		return "other"
	}
}
