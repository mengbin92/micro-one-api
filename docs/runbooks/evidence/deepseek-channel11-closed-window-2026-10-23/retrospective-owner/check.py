#!/usr/bin/env python3
"""Replay the public supplement, later export, and maintainer attestation."""
import csv
import hashlib
import json
import sys
from collections import Counter
from datetime import datetime
from decimal import Decimal
from pathlib import Path

sys.dont_write_bytecode = True
import prepare

HERE = Path(__file__).resolve().parent
BASE = HERE.parent


def read_csv(path):
    with path.open(newline="") as source:
        return list(csv.DictReader(source))


def canonical(rows):
    return sorted(json.dumps(row, sort_keys=True, separators=(",", ":")) for row in rows)


manifest, original_request = prepare.prepare()
final, request = prepare.successor(manifest, original_request)
assert (HERE / "manifest.json").read_bytes() == prepare.encode(manifest)
assert (HERE / "final-manifest.json").read_bytes() == prepare.encode(final)
assert prepare.report_id(manifest) == original_request["expected_report_id"]
assert prepare.report_id(final) == final["retrospective_evidence"]["report_id"] == request["expected_report_id"]
assert final["supersedes"] == original_request["expected_report_id"]
assert final["complete"] is True and final["pages_complete"] is True
assert final["vendor_watermark"] == final["scope"]["end"]
assert final["local_watermark"].startswith("0001-") and final["coverage_evidence_id"] == ""
attestation = json.loads((HERE / "finality-attestation.json").read_text())
assert attestation["kind"] == "maintainer_confirmation"
assert attestation["confirmation_text"] == "不会再更正了"
assert attestation["provider_published_watermark"] is False
assert final["finality_attestation"]["evidence_id"] == prepare.digest(HERE / "finality-attestation.json")
assert final["finality_attestation"]["statement"] == "no_further_corrections"

evidence = final["retrospective_evidence"]
purposes = Counter(record["purpose"] for record in evidence["records"])
assert purposes == {"user": 201, "internal_test": 2}
counts = Counter()
amount = Decimal(0)
for record in evidence["records"]:
    windows = [w for w in evidence["tariff_windows"] if datetime.fromisoformat(w["start"]) <= datetime.fromisoformat(record["started_at"]) and datetime.fromisoformat(record["end_exclusive"]) <= datetime.fromisoformat(w["end"])]
    assert len(windows) == 1
    window = windows[0]
    for count_key, rate_key in (("uncached_input_tokens", "uncached_input_rate_per_million"), ("cache_read_tokens", "cache_read_rate_per_million"), ("output_tokens", "output_rate_per_million")):
        counts[count_key] += record[count_key]
        amount += Decimal(record[count_key]) * Decimal(window[rate_key]) / Decimal(1000000)
assert counts == {"uncached_input_tokens": 319242, "cache_read_tokens": 30420736, "output_tokens": 92728}
assert amount == Decimal("2.04700896")
recheck = json.loads((HERE / "finality-recheck.json").read_text())
assert recheck["window"] == attestation["window"]
assert recheck["supplier_final_watermark"] is None  # UI observation, before maintainer attestation.
for name, info in recheck["csv_files"].items():
    exported = HERE / info["sanitized_file"]
    assert hashlib.sha256(exported.read_bytes()).hexdigest() == info["sanitized_sha256"]
    rows = read_csv(exported)
    assert canonical(rows) == canonical(read_csv(BASE / name))
    assert hashlib.sha256("\n".join(canonical(rows)).encode()).hexdigest() == info["canonical_rows_sha256"]
    assert info["canonical_rows_equal_initial_export"] is True
assert Decimal(recheck["provider_reported_cost_cny"]) == amount
retained = ""
if (HERE / "owner-persistence-verification.json").exists():
    proof = json.loads((HERE / "owner-persistence-verification.json").read_text())["sections"]
    counts = next(row for row in proof if row["section"] == "counts")
    assert counts["reports"] == counts["runs"] == 2
    saved = next(row for row in proof if row["section"] == "report" and row["id"] == request["expected_report_id"])
    assert saved["supersedes"] == final["supersedes"] and saved["actor"] == "iam-user:1"
    assert saved["status"] == "pending" and saved["comparison_status"] == "missing_local" and saved["amount_status"] == "unknown"
    assert saved["retrospective_result"]["amount"] == "2.04700896" and saved["retrospective_result"]["difference"] == "0"
    assert saved["retrospective_result"]["owner_arithmetic_replayed"] is True
    assert saved["retrospective_result"]["execution_join_verified"] is False
    audits = [row for row in proof if row["section"] == "audit"]
    assert len(audits) == 1 and audits[0]["result"] == "success" and audits[0]["actor_user_id"] == 1
    retained = "real owner 2 reports / 2 runs and success audit retained; "
print("PASS: 203 archive records / CNY 2.04700896; re-export rows agree; maintainer-attested successor bound; " + retained + "local owner coverage remains unknown; offline check performs no writes")
