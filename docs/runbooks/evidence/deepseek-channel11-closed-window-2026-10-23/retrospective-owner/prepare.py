#!/usr/bin/env python3
"""Prepare public, reviewable archive input. No DB/network/provider calls.

Hashes are artifact references. This transformation does not authenticate the
archives or reverify their omitted root/reservation joins. Owner arithmetic is
replayed separately by Go tests / the existing owner import.
"""
import argparse
import base64
import csv
import hashlib
import json
import os
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
BASE = HERE.parent


def digest(path):
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def utc_text(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00")).astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def report_id(manifest):
    # This is only fixture preparation. Go's VendorReportID remains the owner
    # authority; the storage-boundary test checks both generated identities.
    scope = dict(manifest["scope"])
    scope["start"], scope["end"] = utc_text(scope["start"]), utc_text(scope["end"])
    with (BASE / "owner-execution/vendor-normalized.csv").open(newline="") as source:
        bill_rows = [{"amount": row["amount"], "buckets": None} for row in csv.DictReader(source)]
    projection = {
        "Scope": scope, "Version": manifest["version"], "EvidenceID": manifest["evidence_id"],
        "Basis": manifest["cost_basis"], "Mode": manifest["mode"], "Rows": bill_rows,
        "Complete": manifest["complete"], "PagesComplete": manifest["pages_complete"],
        "VendorWatermark": utc_text(manifest["vendor_watermark"]),
        "Supersedes": manifest.get("supersedes", ""),
        "ContractEvidenceID": manifest.get("contract_evidence_id", ""),
        "OverageEvidenceID": manifest.get("overage_evidence_id", ""),
        "RequestIDKind": manifest.get("request_id_kind", ""),
        "RequestIDContractEvidenceID": manifest.get("request_id_contract_evidence_id", ""),
    }
    if manifest.get("finality_attestation"):
        attestation = dict(manifest["finality_attestation"])
        attestation["confirmed_at"] = utc_text(attestation["confirmed_at"])
        projection["FinalityAttestation"] = attestation
    return hashlib.sha256(json.dumps(projection, ensure_ascii=False, separators=(",", ":")).encode()).hexdigest()


def prepare():
    manifest = json.loads((BASE / "owner-execution/manifest.json").read_text())
    request = json.loads((BASE / "owner-execution/owner-import-request.json").read_text())
    assert report_id(manifest) == request["expected_report_id"]
    analysis = json.loads((BASE / "time-pricing-analysis.json").read_text())
    mapping = manifest["mappings"][0]
    rates = analysis["pricing_rule"]["cny_per_million_tokens"]
    # Explicit historical windows for these two ordinary weekdays only. This
    # does not install a holiday calendar or an automatic pricing engine.
    stops = [
        ("2026-10-21T09:00:00+08:00", "off_peak"),
        ("2026-10-21T12:00:00+08:00", "peak"),
        ("2026-10-21T14:00:00+08:00", "off_peak"),
        ("2026-10-21T18:00:00+08:00", "peak"),
        ("2026-10-22T09:00:00+08:00", "off_peak"),
        ("2026-10-22T12:00:00+08:00", "peak"),
        ("2026-10-22T14:00:00+08:00", "off_peak"),
        ("2026-10-22T18:00:00+08:00", "peak"),
        ("2026-10-23T00:00:00+08:00", "off_peak"),
    ]
    windows = []
    start = manifest["scope"]["start"]
    for end, band in stops:
        windows.append({
            "start": start, "end": end, "band": band,
            "uncached_input_rate_per_million": rates[band]["uncached_input_tokens"],
            "cache_read_rate_per_million": rates[band]["cache_read_tokens"],
            "output_rate_per_million": rates[band]["output_tokens"],
        })
        start = end
    records = []
    with (BASE / "time-pricing-records.csv").open(newline="") as source:
        for row in csv.DictReader(source):
            begin = datetime.fromisoformat(row["started_at_cst"])
            stop = datetime.fromisoformat(row["comparison_end_exclusive_cst"])
            matching = [w for w in windows if datetime.fromisoformat(w["start"]) <= begin and stop <= datetime.fromisoformat(w["end"])]
            assert len(matching) == 1 and matching[0]["band"] == row["band"]
            records.append({
                "id": "archive-row-" + row["record_no"],
                "purpose": row["purpose"], "time_basis": row["time_basis"],
                "started_at": row["started_at_cst"],
                "finished_at": row["finished_at_cst"],
                "end_exclusive": row["comparison_end_exclusive_cst"],
                **{key: int(row[key]) for key in ("uncached_input_tokens", "cache_read_tokens", "output_tokens")},
            })
    assert len(records) == 203
    external = BASE.parent / "deepseek-channel11-two-day-2026-10-21/live-test.json"
    assert digest(external) == "sha256:" + analysis["external_test_source"]["sha256"]
    manifest["retrospective_evidence"] = {
        "report_id": request["expected_report_id"],
        "supplier_evidence_id": manifest["evidence_id"], "scope": manifest["scope"],
        "source_kind": mapping["source_kind"], "source_id": mapping["source_id"],
        "upstream_model_id": manifest["scope"]["upstream_model_id"],
        "mapping_revision": mapping["revision"], "mapping_evidence_id": mapping["evidence_id"],
        "snapshot_evidence_id": "sha256:" + analysis["private_source_sha256"],
        "records_evidence_id": digest(BASE / "time-pricing-records.csv"),
        "tariff_evidence_id": digest(BASE / "amount-2026-10-21_2026-10-22.csv"),
        "analysis_evidence_id": digest(BASE / "time-pricing-analysis.json"),
        "external_evidence_id": digest(external),
        "tariff_windows": windows, "records": records,
    }
    manifest["reason"] = (
        "Append retrospective archive arithmetic for the fixed DeepSeek channel 11 "
        "2026-10-21/22 report. Input contains 201 archived user intervals and 2 external "
        "internal_test intervals, with original artifact references and historical CNY "
        "rates. Owner verifies only input binding and arithmetic; private execution "
        "joins and CSV-to-record transformation remain operator-submitted. Preserve "
        "authoritative attempts, frozen USD, missing_local/unknown, pending, coverage "
        "and finality. No customer balance, pricing, ledger or admission changes."
    )
    request["manifest_json"] = base64.b64encode(encode(manifest)).decode()
    request["reason"] = manifest["reason"]
    return manifest, request


def successor(manifest, request):
    attestation_path = HERE / "finality-attestation.json"
    attestation = json.loads(attestation_path.read_text())
    updated = json.loads(json.dumps(manifest))
    updated["version"] += "-final-v2"
    updated["supersedes"] = request["expected_report_id"]
    updated["complete"] = True
    updated["vendor_watermark"] = updated["scope"]["end"]
    updated["finality_attestation"] = {
        "kind": attestation["kind"], "evidence_id": digest(attestation_path),
        "confirmed_at": attestation["recorded_at"], "statement": attestation["statement"],
    }
    updated["reason"] = (
        "Supersede the original DeepSeek fixed-period report after the maintainer "
        "confirmed no further corrections. Finality basis is maintainer_confirmation, "
        "not a provider-published watermark or invoice. Attestation: " + digest(attestation_path) + ". "
        "Retain the 201 archived user intervals and 2 external internal_test intervals "
        "and replay only their historical CNY arithmetic. Omitted private execution "
        "joins remain operator-submitted, local execution coverage remains incomplete, "
        "and frozen USD estimates are unchanged. Preserve original reports/runs and "
        "pending/missing_local/unknown. No customer balance, pricing, ledger or admission changes."
    )
    identity = report_id(updated)
    updated["retrospective_evidence"]["report_id"] = identity
    revised_request = dict(request)
    revised_request["manifest_json"] = base64.b64encode(encode(updated)).decode()
    revised_request["reason"] = updated["reason"]
    revised_request["expected_report_id"] = identity
    return updated, revised_request


def encode(value):
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--check", action="store_true", help="verify the checked-in manifest without writing")
    parser.add_argument("--request-out", type=Path, help="create a new owner HTTP request file for review; never submit it")
    args = parser.parse_args()
    manifest, original_request = prepare()
    updated, request = successor(manifest, original_request)
    for name, document in (("manifest.json", manifest), ("final-manifest.json", updated)):
        path = HERE / name
        if args.check:
            assert path.read_bytes() == encode(document), name + " differs from archived inputs"
        else:
            path.write_bytes(encode(document))
    if args.check:
        print("PASS: 203 archive rows and maintainer-attested successor reproduce; no production writes")
    if args.request_out:
        if not args.request_out.is_absolute():
            parser.error("--request-out must be an absolute new file")
        with os.fdopen(os.open(args.request_out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "wb") as output:
            output.write(encode(request))
