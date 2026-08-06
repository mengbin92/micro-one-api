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
//	    [-vendor-bill vendor_bill.csv] \
//	    [-vendor-tolerance 0.05] \
//	    [-unpriced-max 0] \
//	    [-cache-hit-min 0.5]
//
// DSN resolution: -dsn flag, then RECONCILE_DSN, then DATABASE_DSN. Only the
// MySQL driver (go-sql-driver) is supported; Postgres/SQLite are out of scope
// for the production billing reconciliation period check.
package main

import (
	"database/sql"
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
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
	flag.Float64Var(&opts.vendorTolerance, "vendor-tolerance", 0.05, "relative tolerance for vendor bill comparison (0.05 = 5%)")
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
	if strings.HasSuffix(s, "d") {
		days := strings.TrimSuffix(s, "d")
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

	rep.add("== cache-creation counted-but-unbilled (since %s) ==", opts.since.String())
	checkUnbilledCacheCreation(db, cutoff, opts.unpricedMax, rep)

	rep.add("== cache hit rate (口径: cache_read / (cache_read + cache_creation)) ==")
	checkCacheHitRate(db, cutoff, opts.cacheHitMin, rep)

	rep.add("== gross margin (since %s) ==", opts.since.String())
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

func checkGrossMargin(db *sql.DB, cutoff time.Time, rep *report) {
	var gross int64
	var rows int64
	err := db.QueryRow(
		"SELECT COALESCE(SUM(ABS(amount) - upstream_cost), 0), COUNT(*) "+
			"FROM billing_ledgers WHERE type = 'consume' AND created_at >= ?",
		cutoff,
	).Scan(&gross, &rows)
	if err != nil {
		rep.discrepancy("compute gross margin: %v", err)
		return
	}
	if gross < 0 {
		rep.discrepancy("gross margin negative: %d quota over %d ledger rows (售价低于上游成本)", gross, rows)
		return
	}
	rep.ok("gross margin: %d quota over %d ledger rows", gross, rows)
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

// aggregateFamilies groups consume-ledger token buckets by provider family
// (the same low-cardinality heuristic billing uses for metrics).
func aggregateFamilies(db *sql.DB, cutoff time.Time) map[string]*familyTotals {
	rows, err := db.Query(
		"SELECT model_name, "+
			"COALESCE(SUM(prompt_tokens), 0), COALESCE(SUM(completion_tokens), 0), "+
			"COALESCE(SUM(cache_read_tokens), 0), "+
			"COALESCE(SUM(cache_creation_5m_tokens), 0), COALESCE(SUM(cache_creation_1h_tokens), 0), "+
			"COALESCE(SUM(upstream_cost), 0), COUNT(*) "+
			"FROM billing_ledgers WHERE type = 'consume' AND created_at >= ? "+
			"GROUP BY model_name",
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
		t.rows += n
	}
	return out
}

type vendorRow struct {
	family string
	date   string
	fields [6]int64 // creation5m, creation1h, cacheRead, prompt, completion, upstreamQuota
}

func checkVendorBill(db *sql.DB, cutoff time.Time, opts options, rep *report) {
	file, err := os.Open(opts.vendorBill)
	if err != nil {
		rep.discrepancy("open vendor bill %s: %v", opts.vendorBill, err)
		return
	}
	defer file.Close()
	r := csv.NewReader(file)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		rep.discrepancy("read vendor bill header: %v", err)
		return
	}
	col := headerIndex(header, "provider_family", "date", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "cache_read_tokens", "prompt_tokens", "completion_tokens", "upstream_quota")
	if len(col) != 8 {
		rep.discrepancy("vendor bill header must contain provider_family,date,cache_creation_5m_tokens,cache_creation_1h_tokens,cache_read_tokens,prompt_tokens,completion_tokens,upstream_quota — got %v", header)
		return
	}

	vendor := make(map[string]*vendorRow)
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			rep.discrepancy("read vendor bill row: %v", err)
			return
		}
		row := &vendorRow{family: strings.ToLower(strings.TrimSpace(rec[col[0]])), date: rec[col[1]]}
		for i := 0; i < 6; i++ {
			n, perr := parseInt64(rec[col[i+2]])
			if perr != nil {
				rep.discrepancy("vendor bill row %s/%s field %q: %v", row.family, row.date, header[col[i+2]], perr)
				return
			}
			row.fields[i] = n
		}
		key := row.family + "|" + row.date
		if _, dup := vendor[key]; dup {
			rep.discrepancy("duplicate vendor bill row %s/%s", row.family, row.date)
		}
		vendor[key] = row
	}
	if len(vendor) == 0 {
		rep.discrepancy("vendor bill %s contains no data rows", opts.vendorBill)
		return
	}

	// DB side: per model+day totals, then aggregate by family+day in Go.
	rows, err := db.Query(
		"SELECT model_name, DATE(created_at), "+
			"COALESCE(SUM(cache_creation_5m_tokens), 0), COALESCE(SUM(cache_creation_1h_tokens), 0), "+
			"COALESCE(SUM(cache_read_tokens), 0), COALESCE(SUM(prompt_tokens), 0), "+
			"COALESCE(SUM(completion_tokens), 0), COALESCE(SUM(upstream_cost), 0) "+
			"FROM billing_ledgers WHERE type = 'consume' AND created_at >= ? GROUP BY model_name, DATE(created_at)",
		cutoff,
	)
	if err != nil {
		rep.discrepancy("query ledger buckets for vendor comparison: %v", err)
		return
	}
	defer rows.Close()
	actual := make(map[string]*vendorRow)
	for rows.Next() {
		var model, date string
		row := &vendorRow{}
		if err := rows.Scan(&model, &date, &row.fields[0], &row.fields[1], &row.fields[2], &row.fields[3], &row.fields[4], &row.fields[5]); err != nil {
			rep.discrepancy("scan ledger buckets: %v", err)
			return
		}
		key := providerFamilyForModel(model) + "|" + date
		t := actual[key]
		if t == nil {
			t = &vendorRow{family: providerFamilyForModel(model), date: date}
			actual[key] = t
		}
		for i := 0; i < 6; i++ {
			t.fields[i] += row.fields[i]
		}
	}

	keys := make([]string, 0, len(vendor))
	for k := range vendor {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	names := []string{"creation_5m", "creation_1h", "cache_read", "prompt", "completion", "upstream_quota"}
	for _, k := range keys {
		v := vendor[k]
		a := actual[k]
		if a == nil {
			rep.discrepancy("vendor row %s/%s has no matching ledger traffic in window", v.family, v.date)
			continue
		}
		for i := 0; i < 6; i++ {
			diff := float64(a.fields[i] - v.fields[i])
			rel := 0.0
			if v.fields[i] != 0 {
				rel = diff / float64(v.fields[i])
				if rel < 0 {
					rel = -rel
				}
			}
			if v.fields[i] == 0 && a.fields[i] == 0 {
				continue
			}
			if rel > opts.vendorTolerance {
				rep.discrepancy("%s/%s %s: ledger=%d vendor=%d (|diff| %.2f%% > tolerance %.0f%%)",
					v.family, v.date, names[i], a.fields[i], v.fields[i], rel*100, opts.vendorTolerance*100)
			}
		}
		rep.ok("%s/%s buckets within tolerance", v.family, v.date)
	}
}

func headerIndex(header []string, names ...string) []int {
	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.ToLower(strings.TrimSpace(h))] = i
	}
	out := make([]int, len(names))
	for i, n := range names {
		v, ok := idx[strings.ToLower(n)]
		if !ok {
			return nil
		}
		out[i] = v
	}
	return out
}

func parseInt64(s string) (int64, error) {
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil {
		return 0, fmt.Errorf("expected integer, got %q", s)
	}
	return n, nil
}

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
