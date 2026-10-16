#!/usr/bin/env python3
"""Replay the fixed official-day comparison without network or production writes."""
import csv
import hashlib
import json
from datetime import datetime
from decimal import Decimal
from pathlib import Path

here = Path(__file__).resolve().parent
evidence = here.parent
report = json.loads((here / "index.json").read_text())
for name, digest in report["local_evidence_sha256"].items():
    assert hashlib.sha256((here.parents[3] / name).read_bytes()).hexdigest() == digest


def rows(name):
    path = here / name
    assert hashlib.sha256(path.read_bytes()).hexdigest() == report["files"][name]["redacted_sha256"]
    with path.open(encoding="utf-8-sig", newline="") as source:
        result = list(csv.DictReader(source))
    assert len(result) == report["files"][name]["rows"]
    return result


cost = rows("cost-2026-10-14_2026-10-14.csv")
amount = rows("amount-2026-10-14_2026-10-14.csv")
first = json.loads((evidence / "deepseek-official-pilot-2026-10-14/live-evidence.json").read_text())
loads = json.loads((evidence / "deepseek-balance-and-channel1-2026-10-14/balance-window.json").read_text())
cases = first["cases"] + loads["calls"]
assert len(cases) == 6
start, end = map(datetime.fromisoformat, (report["period_start"], report["period_end_exclusive"]))
for row in cost + amount:
    assert start <= datetime.fromisoformat(row["start_time_iso"]) < datetime.fromisoformat(row["end_time_iso"]) <= end
    assert row["model"] == "deepseek-flash"
assert {r["currency"] for r in cost} == {"CNY"}
assert {r["api_key_name"] for r in amount} == {"micro-one-api"}
assert {r["api_key"] for r in amount} == {"[REDACTED]"}
with (evidence / "deepseek-balance-and-channel1-2026-10-14/amount-2026-09-15_2026-10-14.csv").open() as source:
    assert {r["user_id"] for r in csv.DictReader(source)} == {r["user_id"] for r in cost + amount}
local = {"request_count": len(cases), "input_cache_miss_tokens": 0, "input_cache_hit_tokens": 0, "output_tokens": 0}
for case in cases:
    usage = case["native_usage"]
    local["input_cache_miss_tokens"] += usage["prompt_cache_miss_tokens"]
    local["input_cache_hit_tokens"] += usage["prompt_cache_hit_tokens"]
    local["output_tokens"] += usage["completion_tokens"]
    assert usage["prompt_tokens"] == usage["prompt_cache_miss_tokens"] + usage["prompt_cache_hit_tokens"]
    assert usage["total_tokens"] == usage["prompt_tokens"] + usage["completion_tokens"]
    assert start <= datetime.fromisoformat(case.get("started_at_utc") or case["meta"]["started_at"]) < end
assert {r["type"] for r in amount} == set(local)
official = {kind: sum(Decimal(r["amount"]) for r in amount if r["type"] == kind) for kind in local}
assert local == official == report["native"] == report["official"]
reported_cost = sum(Decimal(r["cost"]) for r in cost)
native_estimate = sum(Decimal(case["tariff_estimate_cny"]) for case in cases)
extension = sum(Decimal(r["amount"]) * Decimal(r["price"]) for r in amount if r["type"] != "request_count")
assert reported_cost == native_estimate == extension == Decimal("0.14036956")
assert reported_cost - native_estimate == Decimal(report["difference"]) == 0
assert not rows("cost-2026-10-15_2026-10-15.csv")
assert not rows("amount-2026-10-15_2026-10-15.csv")
assert report["zero_activity_day"]["exact_csv_amount"] is None
assert not report["production_ledger_reconciled"]
assert not report["request_level_actual_cost_verified"]
assert not report["charging_profile_or_rollout_approved"]
print("PASS: six recorded calls, 140333 tokens, CNY 0.14036956; exact difference 0; scope remains manual-trial day.")
