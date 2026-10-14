package data

import (
	"context"
	"database/sql"
	"fmt"
	_ "github.com/mattn/go-sqlite3"
	"micro-one-api/domain/operations/biz"
	"micro-one-api/pkg/jsonx"
	"net/url"
	"path/filepath"
	"time"
)

type Data struct{ db *sql.DB }
type taskRepo struct{ data *Data }

func NewData(path string) (*Data, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("absolute durable operator task database required")
	}
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("_busy_timeout", "10000")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", "FULL")
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite3", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS operator_tasks(task_key TEXT PRIMARY KEY,plan_id TEXT NOT NULL,plan_hash TEXT NOT NULL,kind TEXT NOT NULL,status TEXT NOT NULL,started_at INTEGER NOT NULL,lease_until INTEGER NOT NULL,receipt TEXT NOT NULL DEFAULT '{}'); CREATE INDEX IF NOT EXISTS operator_tasks_budget ON operator_tasks(kind,started_at);`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Data{db: db}, nil
}
func (d *Data) Close() error           { return d.db.Close() }
func NewTaskRepo(d *Data) biz.TaskRepo { return &taskRepo{data: d} }
func (r *taskRepo) Claim(ctx context.Context, p biz.Plan, now time.Time, maxDaily int) (*biz.Claim, error) {
	if err := p.Validate(now); err != nil {
		return nil, err
	}
	if maxDaily < 1 || maxDaily > 48 {
		return nil, fmt.Errorf("invalid daily invocation budget")
	}
	tx, err := r.data.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var hashes int
	err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM operator_tasks WHERE plan_id=? AND plan_hash<>?", p.ID, p.Hash()).Scan(&hashes)
	if err != nil {
		return nil, err
	}
	if hashes > 0 {
		return nil, fmt.Errorf("immutable task plan changed; explicit new plan identity required")
	}
	var exists, runs, daily int
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM operator_tasks WHERE task_key=?", p.Key(now)).Scan(&exists); err != nil {
		return nil, err
	}
	if exists > 0 {
		return nil, nil
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM operator_tasks WHERE plan_id=?", p.ID).Scan(&runs); err != nil {
		return nil, err
	}
	day := now.UTC().Truncate(24 * time.Hour).Unix()
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM operator_tasks WHERE kind=? AND started_at>=?", p.Kind, day).Scan(&daily); err != nil {
		return nil, err
	}
	if runs >= p.MaxRuns || daily >= maxDaily {
		return nil, nil
	}
	c := &biz.Claim{Key: p.Key(now), PlanID: p.ID, Kind: p.Kind, StartedAt: now.Unix()}
	_, err = tx.ExecContext(ctx, "INSERT INTO operator_tasks(task_key,plan_id,plan_hash,kind,status,started_at,lease_until) VALUES(?,?,?,?,?,?,?)", c.Key, p.ID, p.Hash(), p.Kind, "running", now.Unix(), now.Add(2*time.Minute).Unix())
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return c, nil
}
func (r *taskRepo) Finish(ctx context.Context, c biz.Claim, receipt biz.Receipt, now time.Time) error {
	raw, err := jsonx.Marshal(receipt)
	if err != nil {
		return err
	}
	result, err := r.data.db.ExecContext(ctx, "UPDATE operator_tasks SET status=?,receipt=? WHERE task_key=? AND plan_id=? AND status='running' AND lease_until>=?", receipt.Status, string(raw), c.Key, c.PlanID, now.Unix())
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("task lease no longer owned")
	}
	return nil
}
func (r *taskRepo) Expire(ctx context.Context, now time.Time) error {
	// A crash after a paid generation send is execution-unknown. Never retry
	// that same task or release its already consumed call budget.
	_, err := r.data.db.ExecContext(ctx, "UPDATE operator_tasks SET status='uncertain' WHERE status='running' AND lease_until<?", now.Unix())
	return err
}

func (r *taskRepo) Summary(ctx context.Context, kind string) (map[string]int, error) {
	if kind != "cost" && kind != "repair" && kind != "weekly" {
		return nil, fmt.Errorf("fixed worker role required")
	}
	rows, err := r.data.db.QueryContext(ctx, "SELECT status,count(*) FROM operator_tasks WHERE kind=? GROUP BY status", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, err
		}
		out[status] = count
	}
	return out, rows.Err()
}
