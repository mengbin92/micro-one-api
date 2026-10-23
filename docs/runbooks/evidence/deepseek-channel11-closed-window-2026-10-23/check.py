#!/usr/bin/env python3
"""Replay the saved closed-window evidence without network or production writes."""
import csv
import hashlib
import json
from collections import defaultdict
from datetime import datetime
from decimal import Decimal
from pathlib import Path

ROOT = Path(__file__).resolve().parent


def read_json(name):
    return json.loads((ROOT / name).read_text())


def read_csv(name):
    with (ROOT / name).open(encoding="utf-8-sig", newline="") as source:
        return list(csv.DictReader(source))


index = read_json("index.json")
for name, digest in index["files"].items():
    assert hashlib.sha256((ROOT / name).read_bytes()).hexdigest() == digest, name

local = read_json("production-readonly.json")
provider = read_json("provider-export.json")
assert local["window"] == provider["window"] == index["window"]
window = local["window"]
start, end = (datetime.fromisoformat(window[k]) for k in ("start", "end_exclusive"))
assert start.isoformat() == "2026-10-21T00:00:00+08:00"
assert end.isoformat() == "2026-10-23T00:00:00+08:00"
assert (end - start).total_seconds() == 48 * 3600
source, = local["counts"]["source"]
assert source["id"] == 11 and source["authorization_revision"] == 4
assert source["base_url"] == "https://api.deepseek.com/"
assert source["status"] == 1 and source["health_status"] == "healthy"
assert provider["selected_key_name"] == "micro-one-api"

cost_rows = read_csv("cost-2026-10-21_2026-10-22.csv")
token_rows = read_csv("amount-2026-10-21_2026-10-22.csv")
assert len(cost_rows) == 3 and len(token_rows) == 11
for row in cost_rows + token_rows:
    assert row["user_id"] == "sha256:" + provider["account_sha256"]
    assert row["model"] == "deepseek-flash"
    assert start <= datetime.fromisoformat(row["start_time_iso"])
    assert datetime.fromisoformat(row["start_time_iso"]) < datetime.fromisoformat(row["end_time_iso"]) <= end
for row in token_rows:
    assert row["api_key"] == "[REDACTED]" and row["api_key_name"] == "micro-one-api"
assert all(row["currency"] == "CNY" for row in cost_rows)

amounts = defaultdict(int)
daily_usage = defaultdict(lambda: defaultdict(int))
for row in token_rows:
    count = Decimal(row["amount"])
    assert count == int(count) and count >= 0
    amounts[row["type"]] += int(count)
    daily_usage[row["start_time_iso"][:10]][row["type"]] += int(count)
assert set(amounts) == {"request_count", "input_cache_miss_tokens", "input_cache_hit_tokens", "output_tokens"}

old_root = ROOT.parent / "deepseek-channel11-two-day-2026-10-21"
old_index = json.loads((old_root / "index.json").read_text())
assert hashlib.sha256((old_root / "live-test.json").read_bytes()).hexdigest() == old_index["files"]["live-test.json"]["sha256"]
test = json.loads((old_root / "live-test.json").read_text())
assert test["purpose"] == "internal_test" and len(test["calls"]) == 2
external = defaultdict(int)
for call in test["calls"]:
    assert call["http_status"] == 200 and call["automatic_retries"] == 0
    usage = call["native_usage"]
    external["request_count"] += 1
    external["input_cache_miss_tokens"] += usage["prompt_cache_miss_tokens"]
    external["input_cache_hit_tokens"] += usage["prompt_cache_hit_tokens"]
    external["output_tokens"] += usage["completion_tokens"]

daily = local["counts"]["daily_ledger"]
assert {row["date_cst"] for row in daily} == {"2026-10-21", "2026-10-22"}
for day in ("2026-10-21", "2026-10-22"):
    rows = [row for row in daily if row["date_cst"] == day]
    by_type = {
        "request_count": sum(row["rows"] for row in rows),
        "input_cache_miss_tokens": sum(row["uncached_input_tokens"] for row in rows),
        "input_cache_hit_tokens": sum(row["cache_read_tokens"] for row in rows),
        "output_tokens": sum(row["completion_tokens"] for row in rows),
    }
    if day == "2026-10-21":
        by_type = {key: value + external[key] for key, value in by_type.items()}
    assert by_type == daily_usage[day], (day, by_type, daily_usage[day])
ledger, = local["counts"]["ledger"]
assert ledger["rows"] == 201
assert amounts["request_count"] == ledger["rows"] + external["request_count"] == 203
total_tokens = sum(amounts[key] for key in amounts if key != "request_count")
assert total_tokens == provider["ui"]["tokens"] == 30832706
assert total_tokens == ledger["prompt_tokens"] + ledger["completion_tokens"] + 18

reported = sum((Decimal(row["cost"]) for row in cost_rows), Decimal(0))
extended = sum((Decimal(row["price"]) * Decimal(row["amount"]) for row in token_rows if row["price"]), Decimal(0))
assert reported == extended == Decimal(provider["provider_reported_cost_cny"]) == Decimal("2.04700896")
assert provider["report_period_has_ended"]
assert provider["supplier_explicit_final_watermark"] is None
assert not provider["individual_request_ids_in_csv"]
assert local["counts"]["send_window_events"] == []
assert local["counts"]["owner_report_counts"] == [{"runs": 0, "reports": 0}]
assert all(row["purpose"] == "health_probe" and row["missing_send_time"] == row["rows"] for row in local["counts"]["received_window_events"])
assert not local["production_changes"] and local["new_provider_inference_requests"] == 0
assert not local["other_computer_automation_modified"]
assert index["n3_status"] == "NOT_ACCEPTED"

owner = ROOT / "owner-execution"
if owner.exists():
    price = json.loads((owner / "pricing-context.json").read_text(), parse_float=Decimal)
    snapshots = price["sections"]["historical_customer_price_snapshots"]
    assert sum(row["rows"] for row in snapshots) == 201
    for row in snapshots:
        assert Decimal(row["input_price_per_token"]) == Decimal("0.0000003")
        assert Decimal(row["cache_read_price_per_token"]) == Decimal("0.00000006")
        assert Decimal(row["output_price_per_token"]) == Decimal("0.0000012")
    provenance = json.loads((owner / "input-provenance.json").read_text())
    groups = provenance["official_csv_price_groups"]
    assert Decimal(groups["off_peak"]["cost_cny"]) == Decimal("0.55012848")
    assert Decimal(groups["peak"]["cost_cny"]) == Decimal("1.49688048")
    assert sum((Decimal(value["cost_cny"]) for value in groups.values()), Decimal(0)) == reported
    manifest = json.loads((owner / "manifest.json").read_text())
    assert manifest["scope"]["currency"] == "CNY" and manifest["scope"]["granularity"] == "account_period"
    assert manifest["cost_basis"] == "provider_reported" and not manifest["complete"]
    assert manifest["vendor_watermark"].startswith("0001-") and not manifest["coverage_evidence_id"]
    preview = json.loads((owner / "owner-preview-summary.json").read_text())
    request = json.loads((owner / "owner-import-request.json").read_text())
    assert preview["report_id"] == request["expected_report_id"]
    assert preview["status"] == "pending" and preview["snapshot_database_unchanged"]
    assert preview["user_reservations"] == 0 and preview["unknown_time_attempts"] == 2442
    assert preview["comparisons"][0]["amount_status"] == "unknown"
    with (owner / "vendor-normalized.csv").open(newline="") as source_file:
        normalized, = csv.DictReader(source_file)
    assert normalized["request_id"] == "" and Decimal(normalized["amount"]) == reported
    for key in ("uncached_input_tokens", "cache_read_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens", "output_tokens"):
        assert normalized[key] == ""
    if (owner / "permission-publication-result.json").exists():
        publication = json.loads((owner / "permission-publication-result.json").read_text())
        assert publication["migration"] == "140_publish_vendor_report_import"
        assert publication["no_other_permission_grant_role_assignment_or_session_changes"]
        assert publication["repeat_result"] == "nothing to apply"
        assert publication["after"]["root_import_grants"] == 1
        assert publication["after"]["permission_status"] == "enabled"
        for key in ("policy_revision", "catalog_revision", "root_revision"):
            assert publication["after"][key] == publication["before"][key] + 1
        for key in ("other_permission_digest", "other_grant_digest", "nonroot_role_digest", "assignment_digest", "session_digest"):
            assert publication["after"][key] == publication["before"][key]
        migration = ROOT.parents[3] / "migrations/140_publish_vendor_report_import.sql"
        assert hashlib.sha256(migration.read_bytes()).hexdigest() == publication["migration_sha256"]
    if index.get("production_import_executed"):
        persisted = json.loads((owner / "owner-persistence-verification.json").read_text())
        report, = persisted["sections"]["report"]
        runs = persisted["sections"]["runs"]
        assert report["id"] == preview["report_id"] and report["status"] == "pending"
        assert report["actor"].startswith("iam-user:")
        assert report["cost_basis"] == "provider_reported" and report["amount_status"] == "unknown"
        assert report["verification_scope"] == "none" and Decimal(report["vendor_amount_cny"]) == reported
        assert len(runs) == 1 and runs[0]["sequence"] == 1
        assert runs[0]["payload_sha256"] == report["payload_sha256"]
        assert persisted["sections"]["owner_counts"] == [{"runs": 1, "reports": 1}]
        audit, = persisted["sections"]["write_audit"]
        assert audit["operation"] == "billing.vendor_report.import" and audit["actor_user_id"] == 1
        assert audit["result"] == "success" and audit["reason_mentions_fixed_window"]
        result = json.loads((owner / "owner-import-result.json").read_text())
        assert result["production_import_executed"] and result["listed_after_import"]
        assert result["report_id"] == report["id"] and result["status"] == "pending"
        assert not result["session_token_archived"] and result["new_provider_inference_requests"] == 0
        assert not index.get("price_ledger_or_gate_changes", False)
    if index.get("owner_query_fix_deployed"):
        replay = json.loads((owner / "owner-batch-replay.json").read_text())
        assert replay["full_report_byte_equal"] and replay["full_report_structurally_equal"]
        assert replay["original_report_sha256"] == replay["batched_report_sha256"]
        assert replay["source_snapshot_unchanged"] and replay["unknown_time_attempts"] == 2442
        fixed = json.loads((owner / "owner-timeout-fix-verification.json").read_text())
        assert fixed["same_original_owner_request_replayed"] and not fixed["deadline_widened"]
        assert fixed["before_query_count_for_64_intents"] == 65 and fixed["after_query_count_for_64_intents"] == 2
        assert fixed["after_query_count_for_513_intents"] == 4
        deployed = json.loads((owner / "billing-deploy-result.json").read_text())
        assert deployed["running"] and deployed["platform"] == "linux/amd64"
        assert deployed["health"]["status"] == 200 and deployed["existing_gate_flags_unchanged"]
        assert deployed["existing_timeout_environment_unchanged"]
        indexed = json.loads((owner / "execution-index-publication.json").read_text())
        assert indexed["after"]["index_columns"] == "intent_id,id" and indexed["after"]["migration_rows"] == 1
        assert indexed["repeat_result"] == "nothing to apply"
        migration = ROOT.parents[3] / "migrations/141_add_execution_intent_event_index.sql"
        assert hashlib.sha256(migration.read_bytes()).hexdigest() == indexed["sql_sha256"]
imported = "real owner 1 report / 1 run pending; " if index.get("production_import_executed") else ""
print(f"PASS: 201 ledger + 2 archived tests = 203 requests / {total_tokens} tokens; provider CNY {reported} equals CSV price extension; {imported}N3 owner coverage, comparable currency and finality remain pending")
