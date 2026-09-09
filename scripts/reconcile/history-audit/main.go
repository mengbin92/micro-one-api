// Command history-audit reports immutable consume evidence. It has no billing
// RPC client or database mutation path; even its transaction is read-only.
package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"

	"micro-one-api/pkg/jsonx"
)

const selectSQL = `
-- Exactly one SELECT; no temp tables, locks, billing RPC, or repair SQL.
-- The sibling count prevents a window boundary from pricing a partial request.
SELECT JSON_OBJECT(
  'id', l.id, 'created_at', DATE_FORMAT(l.created_at, '%Y-%m-%dT%H:%i:%s.%fZ'),
  'user_id', l.user_id, 'reference_id', COALESCE(l.reference_id, ''),
  'ledger_dedupe_key', l.ledger_dedupe_key,
  'request_ledger_count', CASE WHEN COALESCE(l.reference_id, '') = '' THEN 1 ELSE (
    SELECT COUNT(*) FROM billing_ledgers sibling
    WHERE sibling.type = 'consume' AND sibling.reference_id = l.reference_id
      AND sibling.user_id = l.user_id
  ) END,
  'amount', l.amount, 'cost_source', l.cost_source,
  'subscription_cost', l.subscription_cost, 'balance_cost', l.balance_cost,
  'subscription_id', COALESCE(r.subscription_id, 0),
  'upstream_cost', l.upstream_cost, 'cost_audit_status', l.cost_audit_status,
  'bucket_costs', JSON_ARRAY(l.prompt_cost, l.cache_read_cost,
    l.cache_creation_5m_cost, l.cache_creation_1h_cost, l.completion_cost),
  'evidence', JSON_OBJECT(
    'model_name', l.model_name, 'source_kind', l.source_kind,
    'channel_id', l.channel_id, 'subscription_account_id', l.subscription_account_id,
    'upstream_model_id', l.upstream_model_id, 'endpoint', l.endpoint,
    'prompt_tokens', l.prompt_tokens, 'completion_tokens', l.completion_tokens,
    'cache_read_tokens', l.cache_read_tokens,
    'cache_creation_5m_tokens', l.cache_creation_5m_tokens,
    'cache_creation_1h_tokens', l.cache_creation_1h_tokens,
    'uncached_input_tokens', l.uncached_input_tokens,
    'reported_prompt_tokens', l.reported_prompt_tokens,
    'reported_total_tokens', l.reported_total_tokens,
    'billable_total_tokens', l.billable_total_tokens,
    'usage_semantics', l.usage_semantics, 'usage_protocol', l.usage_protocol,
    'usage_field_shape', l.usage_field_shape, 'usage_parse_status', l.usage_parse_status,
    'usage_contract_version', l.usage_contract_version, 'canonical_present', l.canonical_present,
    'usage_decision_reason', l.usage_decision_reason,
    'subset_candidate_cost', l.subset_candidate_cost,
    'exclusive_candidate_cost', l.exclusive_candidate_cost,
    'pricing_config_hash', l.pricing_config_hash,
    'snapshot', IF(s.config_hash IS NULL, NULL, JSON_OBJECT(
      'v', s.snapshot_version, 'model', s.model_name,
      'in', s.input_price, 'out', s.output_price, 'cr', s.cache_read_price,
      'c5', s.cache_creation_5m_price, 'c1', s.cache_creation_1h_price,
      'grp', s.group_ratio, 'mode', s.cache_creation_mode
    ))
  )
)
FROM billing_ledgers l
LEFT JOIN billing_pricing_snapshots s ON s.config_hash = l.pricing_config_hash
LEFT JOIN billing_reservations r ON r.reservation_id = l.reference_id AND r.user_id = l.user_id
WHERE l.type = 'consume' AND l.created_at >= ? AND l.created_at < ?
ORDER BY l.created_at, l.id;
`

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "history-audit:", err)
		os.Exit(2)
	}
}

func run(args []string, out, stderr io.Writer) error {
	fs := flag.NewFlagSet("history-audit", flag.ContinueOnError)
	fs.SetOutput(stderr)
	start := fs.String("start", "", "inclusive UTC RFC3339 timestamp (required for DB reads)")
	end := fs.String("end", "", "exclusive UTC RFC3339 timestamp (required for DB reads)")
	input := fs.String("input", "", "offline JSON array of ledger evidence; no DB connection")
	format := fs.String("format", "json", "json or csv; one report record per request")
	timeout := fs.Duration("timeout", 2*time.Minute, "database read deadline")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 || (*format != "json" && *format != "csv") || *timeout <= 0 {
		return errors.New("unexpected arguments, invalid format or non-positive timeout")
	}
	var ledgers []ledger
	if *input != "" {
		if *start != "" || *end != "" {
			return errors.New("offline input is already scoped; do not combine it with start/end")
		}
		f, err := os.Open(*input)
		if err != nil {
			return err
		}
		defer f.Close()
		dec := jsonx.NewDecoder(f)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&ledgers); err != nil {
			return errors.New("invalid ledger evidence JSON")
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return errors.New("expected a single JSON array")
		}
	} else {
		a, errA := time.Parse(time.RFC3339Nano, *start)
		b, errB := time.Parse(time.RFC3339Nano, *end)
		if errA != nil || errB != nil || !b.After(a) {
			return errors.New("start/end must define an increasing RFC3339 interval")
		}
		// A dedicated variable avoids accidentally using a service's writable DSN.
		cfg, err := mysql.ParseDSN(os.Getenv("HISTORY_AUDIT_DSN"))
		if err != nil || cfg.DBName == "" {
			return errors.New("set HISTORY_AUDIT_DSN to the billing database's SELECT-only account")
		}
		cfg.MultiStatements = false
		cfg.AllowAllFiles = false
		cfg.Params = map[string]string{"time_zone": "'+00:00'"}
		db, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			return errors.New("cannot open audit database")
		}
		defer db.Close()
		db.SetMaxOpenConns(1)
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		ledgers, err = readLedgers(ctx, db, a.UTC(), b.UTC())
		if err != nil {
			// Driver errors can contain usernames, hosts or DSN fragments.
			return errors.New("database read failed; check SELECT grants, migrations through 088, connectivity and timeout")
		}
	}
	records, err := audit(ledgers)
	if err != nil {
		return err
	}
	return writeReport(out, *format, records)
}

func readLedgers(ctx context.Context, db *sql.DB, start, end time.Time) ([]ledger, error) {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, selectSQL, start.Format("2006-01-02 15:04:05.999999"), end.Format("2006-01-02 15:04:05.999999"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ledger
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var row ledger
		if err := jsonx.Unmarshal(raw, &row); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func writeReport(w io.Writer, format string, records []record) error {
	if format == "json" {
		enc := jsonx.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(records)
	}
	cw := csv.NewWriter(w)
	if err := cw.Write([]string{"classification", "reason", "evidence_source", "charged_cost", "canonical_cost", "canonical_delta", "subset_candidate_cost", "subset_candidate_delta", "exclusive_candidate_cost", "exclusive_candidate_delta", "ledgers_json"}); err != nil {
		return err
	}
	for _, r := range records {
		raw, err := jsonx.Marshal(r.Ledgers)
		if err != nil {
			return err
		}
		if err := cw.Write([]string{r.Classification, r.Reason, r.EvidenceSource,
			strconv.FormatInt(r.ChargedCost, 10), optionalInt(r.CanonicalCost), optionalInt(r.CanonicalDelta),
			optionalInt(r.SubsetCost), optionalInt(r.SubsetDelta), optionalInt(r.ExclusiveCost), optionalInt(r.ExclusiveDelta), string(raw)}); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

func optionalInt(n *int64) string {
	if n == nil {
		return ""
	}
	return strconv.FormatInt(*n, 10)
}
