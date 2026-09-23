#!/usr/bin/env python3
"""Reproduce the dashboard index plan and same-fixture timing in SQLite."""

import argparse
import datetime as dt
import json
import math
import os
import platform
import sqlite3
import statistics
import sys
import tempfile
import time
from pathlib import Path


QUERY = """
SELECT substr(created_at, 1, 10) AS day,
       COALESCE(SUM(ABS(amount)), 0),
       COALESCE(SUM(prompt_tokens), 0),
       COALESCE(SUM(completion_tokens), 0),
       COUNT(*)
FROM billing_ledgers INDEXED BY {index_name}
WHERE type = ? AND user_id = ? AND created_at >= ? AND created_at <= ?
GROUP BY day ORDER BY day ASC
"""


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--rows", type=int, default=200_000)
    parser.add_argument("--samples", type=int, default=9)
    parser.add_argument("--minimum-improvement-percent", type=float, default=5.0)
    parser.add_argument("--output", help="Optional JSON evidence path")
    return parser.parse_args()


def percentile(values, fraction):
    ordered = sorted(values)
    return ordered[max(0, math.ceil(len(ordered) * fraction) - 1)]


def measure(connection, index_name, params, samples):
    query = QUERY.format(index_name=index_name)
    for _ in range(2):
        connection.execute(query, params).fetchall()
    timings = []
    for _ in range(samples):
        started = time.perf_counter_ns()
        rows = connection.execute(query, params).fetchall()
        timings.append((time.perf_counter_ns() - started) / 1_000_000)
    plan = [row[3] for row in connection.execute("EXPLAIN QUERY PLAN " + query, params)]
    return {"raw_ms": timings, "median_ms": statistics.median(timings), "p95_ms": percentile(timings, 0.95), "result_rows": len(rows), "plan": plan}


def main():
    args = parse_args()
    if args.rows < 10_000 or args.samples < 3:
        raise ValueError("rows must be at least 10000 and samples at least 3")
    if args.minimum_improvement_percent < 0:
        raise ValueError("minimum improvement percent must be non-negative")
    descriptor, database_path = tempfile.mkstemp(prefix="dashboard-index-", suffix=".db")
    os.close(descriptor)
    try:
        connection = sqlite3.connect(database_path)
        connection.executescript("""
            PRAGMA journal_mode=OFF;
            PRAGMA synchronous=OFF;
            PRAGMA temp_store=MEMORY;
            CREATE TABLE billing_ledgers (
              id INTEGER PRIMARY KEY,
              user_id TEXT NOT NULL,
              type TEXT NOT NULL,
              created_at TEXT NOT NULL,
              model_name TEXT NOT NULL,
              amount INTEGER NOT NULL,
              prompt_tokens INTEGER NOT NULL,
              completion_tokens INTEGER NOT NULL
            );
        """)
        start = dt.datetime(2026, 1, 1)
        kinds = ("consume", "recharge", "refund", "adjust")

        def fixture():
            for row_id in range(1, args.rows + 1):
                target_row = row_id % 2 == 0
                user_id = "target-user" if target_row else f"other-{row_id % 97}"
                kind = kinds[(row_id // 2) % len(kinds)] if target_row else kinds[row_id % len(kinds)]
                created_at = start + dt.timedelta(days=row_id % 120, seconds=row_id % 86400)
                yield (row_id, user_id, kind, created_at.isoformat(sep=" "), f"model-{row_id % 11}", -10, 7, 3)

        connection.executemany("INSERT INTO billing_ledgers VALUES (?, ?, ?, ?, ?, ?, ?, ?)", fixture())
        connection.executescript("""
            CREATE INDEX idx_billing_ledgers_user_created_model
              ON billing_ledgers (user_id, created_at, model_name);
            CREATE INDEX idx_billing_ledgers_user_type_created
              ON billing_ledgers (user_id, type, created_at);
            ANALYZE;
        """)
        params = ("consume", "target-user", "2026-02-01 00:00:00", "2026-04-30 23:59:59")
        old = measure(connection, "idx_billing_ledgers_user_created_model", params, args.samples)
        new = measure(connection, "idx_billing_ledgers_user_type_created", params, args.samples)
        improvement = (old["median_ms"] - new["median_ms"]) / old["median_ms"] * 100
        passed = improvement >= args.minimum_improvement_percent and any("idx_billing_ledgers_user_type_created" in line for line in new["plan"])
        report = {
            "schema_version": 1,
            "captured_at_utc": dt.datetime.now(dt.timezone.utc).isoformat(),
            "environment": {"architecture": platform.machine(), "python": platform.python_version(), "sqlite": sqlite3.sqlite_version},
            "fixture": {"rows": args.rows, "samples_per_index": args.samples, "same_query_and_parameters": True},
            "control_index": old,
            "candidate_index": new,
            "median_improvement_percent": improvement,
            "minimum_improvement_percent": args.minimum_improvement_percent,
            "passed": passed,
            "scope": "SQLite query-plan and deterministic fixture evidence; not a production MySQL latency claim.",
        }
        print(json.dumps(report, ensure_ascii=False, indent=2))
        if args.output:
            Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        return 0 if passed else 1
    finally:
        try:
            connection.close()
        except UnboundLocalError:
            pass
        os.unlink(database_path)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, sqlite3.Error) as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)
