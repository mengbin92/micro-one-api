#!/usr/bin/env python3
"""Replay saved evidence only; never send inference or write production data."""
import csv
import hashlib
import json
from decimal import Decimal
from pathlib import Path

root = Path(__file__).resolve().parent
index = json.loads((root / "index.json").read_text())
for name, expected in index["files"].items():
    assert hashlib.sha256((root / name).read_bytes()).hexdigest() == expected["sha256"], name
    if expected.get("data_rows") == 0:
        with (root / name).open(encoding="utf-8-sig", newline="") as source:
            assert list(csv.DictReader(source)) == [], name

assert index["status"] == "NO_INFERENCE_ACTIVITY_OBSERVED"
assert index["window"]["start"] == "2026-10-19T00:00:00+08:00"
assert index["window"]["end_exclusive"] == "2026-10-21T00:00:00+08:00"
assert index["provider_evidence"]["csv_amount"] is None
assert index["local_counts"]["consumption_ledger_rows"] == 0
binding = json.loads((root / "source-binding.json").read_text())
assert binding["current_credential_mask"] == index["provider_evidence"]["selected_key_mask"]
assert not binding["credential_left_production"]
assert all(row[1] == "health_probe" for row in binding["received_window_event_groups"])

test = json.loads((root / "live-test.json").read_text())
assert test["status"] == "completed" and test["runner_exit_code"] == 0
assert test["source_id"] == 11 and test["purpose"] == "internal_test"
assert len(test["calls"]) == test["maximum_calls"] == 2
total = Decimal(0)
for call in test["calls"]:
    assert call["state"] == "completed" and call["http_status"] == 200
    assert call["automatic_retries"] == 0
    usage = call["native_usage"]
    assert usage["prompt_tokens"] == 8 and usage["completion_tokens"] == 1
    assert usage["total_tokens"] == usage["prompt_tokens"] + usage["completion_tokens"]
    cost = (Decimal(usage["prompt_cache_hit_tokens"]) * Decimal("0.04")
            + Decimal(usage["prompt_cache_miss_tokens"]) * Decimal("2")
            + Decimal(usage["completion_tokens"]) * Decimal("8")) / Decimal(1_000_000)
    assert cost == Decimal(call["tariff_estimate_cny"])
    total += cost
assert total == Decimal("0.000048") < Decimal(test["budget_cny"])
snapshot = json.loads((root / "provider-live-snapshot.json").read_text())
assert snapshot["status"] == "CONTROLLED_SOURCE_COST_SNAPSHOT_MATCHED"
assert not snapshot["complete_two_day_period"] and snapshot["supplier_final_watermark"] is None
with (root / "cost-2026-10-21_2026-10-21.csv").open(newline="") as source:
    cost_rows = list(csv.DictReader(source))
with (root / "amount-2026-10-21_2026-10-21.csv").open(newline="") as source:
    amount_rows = list(csv.DictReader(source))
assert len(cost_rows) == 1 and all(row["currency"] == "CNY" for row in cost_rows)
assert all(row["user_id"] == "sha256:" + snapshot["account_sha256"] for row in cost_rows + amount_rows)
assert all(row["api_key"] == "[REDACTED]" and row["api_key_name"] == "micro-one-api" for row in amount_rows)
assert {row["type"]: Decimal(row["amount"]) for row in amount_rows} == {
    "request_count": Decimal(2), "input_cache_miss_tokens": Decimal(16), "output_tokens": Decimal(2)}
reported = sum((Decimal(row["cost"]) for row in cost_rows), Decimal(0))
extended = sum((Decimal(row["amount"]) * Decimal(row["price"]) for row in amount_rows if row["price"]), Decimal(0))
assert reported == extended == total == Decimal(snapshot["provider_reported_amount_cny"])
print(f"PASS: closed baseline has no inference rows; two separate internal tests, 18 tokens, official CNY {reported} matches exactly; final period closure pending")
