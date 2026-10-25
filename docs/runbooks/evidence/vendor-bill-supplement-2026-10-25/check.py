#!/usr/bin/env python3
"""Check private legacy-store bindings and emit only a sanitized audit result.

This is read-only evidence replay. It neither creates execution events nor
certifies the authenticity, completeness or finality of a supplier account.
"""
import argparse
import csv
import hashlib
import io
import json
import os
import re
import sys
from collections import Counter, defaultdict
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from pathlib import Path
from zoneinfo import ZoneInfo

UTC = timezone.utc
USAGE = ("uncached_input_tokens", "cache_read_tokens", "completion_tokens",
         "prompt_tokens", "cache_creation_5m_tokens", "cache_creation_1h_tokens")
OUTCOMES = {"success", "rejected", "failed", "cancelled", "incomplete", "unknown"}


class AuditFailure(Exception):
    pass


def require(condition, message):
    if not condition:
        raise AuditFailure(message)


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON field")
        result[key] = value
    return result


def decode(raw):
    return json.loads(raw, object_pairs_hook=unique_object)


def integer(value):
    require(not isinstance(value, bool) and
            (isinstance(value, int) or isinstance(value, str) and
             re.fullmatch(r"-?(0|[1-9][0-9]*)", value)), "invalid integer")
    return int(value)


def identity(value):
    require(isinstance(value, (str, int)) and not isinstance(value, bool) and
            str(value) != "", "missing identity")
    return str(value)


def instant(value, assume_utc=False):
    require(isinstance(value, str), "invalid timestamp")
    try:
        point = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        raise AuditFailure("invalid timestamp") from None
    require(point.tzinfo is not None or assume_utc, "timestamp timezone missing")
    return point.replace(tzinfo=UTC) if point.tzinfo is None else point.astimezone(UTC)


def milliseconds(value):
    value = integer(value)
    require(value > 0, "invalid timing timestamp")
    return datetime.fromtimestamp(value // 1000, UTC) + timedelta(milliseconds=value % 1000)


def usage(row):
    require(integer(row["canonical_present"]) == 1 and
            row["usage_parse_status"] == "verified", "local usage is not verified canonical data")
    require(isinstance(row["usage_semantics"], str) and
            row["usage_semantics"] not in ("", "unknown"), "local usage semantics missing")
    values = tuple(integer(row[key]) for key in USAGE)
    require(all(value >= 0 for value in values), "negative local usage")
    return values


def grouped(rows, field):
    result = defaultdict(list)
    for row in rows:
        result[identity(row[field])].append(row)
    return result


def audit(snapshot_raw, archive_raw):
    require(len(snapshot_raw) <= 64 << 20 and len(archive_raw) <= 2 << 20,
            "evidence input exceeds audit bounds")
    snapshot = decode(snapshot_raw)
    require(snapshot["revision"] == "legacy-bill-readonly-v1", "unsupported snapshot revision")
    require(isinstance(snapshot["rows"], list) and len(snapshot["rows"]) <= 100000,
            "snapshot rows exceed audit bounds")
    start, end = (instant(snapshot["window"][key]) for key in ("start", "end"))
    location = ZoneInfo(snapshot["window"]["timezone"])
    require(start < end, "invalid audit window")
    source = (snapshot["source_kind"], integer(snapshot["source_id"]), snapshot["upstream_model_id"])
    require(source[0] == "channel" and source[1] > 0 and source[2], "invalid source binding")
    sections = defaultdict(list)
    for row in snapshot["rows"]:
        sections[row["section"]].append(row)
    for section in ("consume", "timing", "ledger", "reservation"):
        require(sections[section], "required store section is empty")
        ids = [identity(row["id"]) for row in sections[section]]
        require(len(ids) == len(set(ids)), "duplicate store primary key")

    archived = list(csv.DictReader(io.StringIO(archive_raw.decode("utf-8-sig"))))
    numbers = [identity(row["record_no"]) for row in archived]
    require(len(numbers) == len(set(numbers)), "duplicate archive record")
    require(all(row["purpose"] in ("user", "internal_test") for row in archived),
            "unsupported archive purpose")
    for row in archived:
        begin, finish, stop = (instant(row[key]) for key in
                               ("started_at_cst", "finished_at_cst", "comparison_end_exclusive_cst"))
        cost = Decimal(row["cost_cny"])
        require(start <= begin <= finish < stop <= end and cost.is_finite() and cost >= 0 and
                all(integer(row[key]) >= 0 for key in
                    ("uncached_input_tokens", "cache_read_tokens", "output_tokens")),
                "archive interval, usage or recomputation is invalid")
    users = [row for row in archived if row["purpose"] == "user"]
    external = [row for row in archived if row["purpose"] == "internal_test"]
    # shortcut: this bill has two external tests; extend before auditing another evidence shape.
    require(users and len(external) == 2 and len(sections["consume"]) == len(users),
            "archive and local user record counts differ")
    archive_keys = defaultdict(list)
    for row in users:
        key = tuple(instant(row[name]) for name in
                    ("started_at_cst", "finished_at_cst", "comparison_end_exclusive_cst"))
        key += tuple(integer(row[name]) for name in
                     ("uncached_input_tokens", "cache_read_tokens", "output_tokens"))
        archive_keys[key].append(row)

    timings = defaultdict(list)
    parsed_timings = []
    for row in sections["timing"]:
        stored = row["request_timing_json"]
        summary = decode(stored) if isinstance(stored, str) else stored
        require(isinstance(summary, dict), "timing summary is not an object")
        require(identity(row["user_id"]) == identity(summary["user_id"]) and
                row["root_request_id"] == summary.get("root_request_id", ""),
                "timing owner or root differs from its stored summary")
        require(isinstance(summary.get("attempts"), list), "timing attempts missing")
        require(summary.get("outcome") in OUTCOMES and
                all(attempt.get("outcome") in OUTCOMES for attempt in summary["attempts"]),
                "unsupported timing outcome")
        timings[(row["root_request_id"], identity(row["user_id"]))].append(summary)
        parsed_timings.append(summary)
    reservations = grouped(sections["reservation"], "reservation_id")
    ledgers = grouped(sections["ledger"], "reference_id")
    dedupes = [identity(row["ledger_dedupe_key"]) for row in sections["ledger"]]
    require(len(dedupes) == len(set(dedupes)), "duplicate ledger dedupe key")
    roots, reserved, used_ledger_ids = set(), set(), set()
    used_archive, paths, split_dimensions = [], Counter(), Counter()
    totals = Counter()
    for row in sections["consume"]:
        root, res, user = (identity(row[key]) for key in
                           ("root_request_id", "reservation_id", "user_id"))
        require(root not in roots and res not in reserved, "duplicate successful root or reservation")
        roots.add(root)
        reserved.add(res)
        require((row["source_kind"], integer(row["channel_id"]), row["upstream_model_id"]) == source,
                "consume source binding differs")
        local_usage = usage(row)
        summaries = timings[(root, user)]
        require(len(summaries) == 1, "successful consume lacks an unambiguous timing join")
        summary, = summaries
        successful = [a for a in summary["attempts"] if a["outcome"] == "success"]
        require(summary["outcome"] == "success" and len(successful) == 1,
                "timing does not have exactly one successful attempt")
        attempt, = successful
        require(identity(attempt["request_id"]) == identity(row["request_id"]) and
                identity(attempt["reservation_id"]) == res and
                integer(attempt["number"]) == integer(row["attempt_number"]) and
                (attempt["source_kind"], integer(attempt["source_id"]), attempt["upstream_model_id"]) == source,
                "timing attempt binding differs")
        matching_reservations = reservations[res]
        require(len(matching_reservations) == 1, "reservation join is missing or ambiguous")
        reservation, = matching_reservations
        require(identity(reservation["user_id"]) == user and
                identity(reservation["request_id"]) == identity(row["request_id"]) and
                identity(reservation["root_request_id"]) == root and
                integer(reservation["attempt_number"]) == integer(row["attempt_number"]) and
                (reservation["source_kind"], integer(reservation["channel_id"]), reservation["upstream_model_id"]) == source,
                "reservation execution binding differs")
        require(reservation["status"] == "committed" and row["model_name"] == reservation["model"],
                "reservation is not committed or billing model differs")
        dimension_rows = ledgers[res]
        require(1 <= len(dimension_rows) <= 2, "ledger join has invalid dimension count")
        dimensions = [entry["cost_source"] for entry in dimension_rows]
        require(len(dimensions) == len(set(dimensions)) and
                set(dimensions) <= {"balance", "subscription"}, "duplicate or unsupported ledger dimensions")
        amount = 0
        ledger_times = []
        for entry in dimension_rows:
            require(identity(entry["user_id"]) == user and entry["type"] == "consume" and
                    (entry["source_kind"], integer(entry["channel_id"]), entry["upstream_model_id"]) == source and
                    entry["model_name"] == reservation["model"], "ledger owner/source/model binding differs")
            require(entry["ledger_dedupe_key"] == res + ":consume:" + entry["cost_source"],
                    "ledger idempotency binding differs")
            require(usage(entry) == local_usage and entry["usage_semantics"] == row["usage_semantics"],
                    "ledger and consume usage differ")
            debit = integer(entry["amount"])
            require(debit < 0 and integer(entry[entry["cost_source"] + "_cost"]) == -debit and
                    integer(entry["balance_cost" if entry["cost_source"] == "subscription" else "subscription_cost"]) == 0,
                    "ledger dimension amounts are inconsistent")
            amount += debit
            ledger_times.append(instant(entry["created_at"], assume_utc=True))
            used_ledger_ids.add(identity(entry["id"]))
        require(integer(reservation["actual_cost"]) == -amount, "committed cost differs from ledger debits")
        begin = milliseconds(summary["started_at_ms"])
        finish = milliseconds(summary["handler_finished_at_ms"])
        stop = max([finish + timedelta(milliseconds=1),
                    datetime.fromtimestamp(integer(row["created_at"]), UTC) + timedelta(seconds=1)] +
                   [point + timedelta(milliseconds=1) for point in ledger_times])
        require(start <= begin <= finish < stop <= end, "joined interval escapes the fixed window")
        key = (begin, finish, stop, *local_usage[:3])
        matching_archive = archive_keys.get(key, [])
        require(len(matching_archive) == 1, "fresh private binding does not uniquely match archived interval/usage")
        used_archive.append(matching_archive.pop())
        paths["legacy" if summary["execution_path"] == "legacy" else "other"] += 1
        split_dimensions[len(dimension_rows)] += 1
        totals.update(dict(zip(USAGE[:3], local_usage[:3])))
    require(len(used_archive) == len(users), "not all archived user records were joined")
    user_cost = sum((Decimal(row["cost_cny"]) for row in used_archive), Decimal(0))
    external_cost = sum((Decimal(row["cost_cny"]) for row in external), Decimal(0))
    require(user_cost.is_finite() and external_cost.is_finite() and
            user_cost >= 0 and external_cost >= 0, "invalid archive recomputation amount")
    source_attempts = Counter()
    for summary in parsed_timings:
        for attempt in summary["attempts"]:
            if (attempt.get("source_kind"), integer(attempt.get("source_id", 0)), attempt.get("upstream_model_id")) == source:
                source_attempts[attempt["outcome"]] += 1
    return {
        "revision": "legacy-store-binding-audit-v1",
        "private_snapshot_sha256": hashlib.sha256(snapshot_raw).hexdigest(),
        "archive_records_sha256": hashlib.sha256(archive_raw).hexdigest(),
        "window": {"start": start.astimezone(location).isoformat(),
                   "end": end.astimezone(location).isoformat(), "timezone": location.key},
        "joined_user_records": len(used_archive),
        "unique_roots": len(roots), "unique_reservations": len(reserved),
        "joined_ledger_dimension_rows": len(used_ledger_ids),
        "ledger_dimension_counts": dict(split_dimensions),
        "execution_paths": dict(paths), "local_three_bucket_tokens": dict(totals),
        "timing_records_scanned": len(parsed_timings),
        "extra_timing_records": len(parsed_timings) - len(used_archive),
        "timing_outcomes": dict(Counter(s["outcome"] for s in parsed_timings)),
        "source_matching_attempt_outcomes": dict(source_attempts),
        "extra_reservations": len(sections["reservation"]) - len(reserved),
        "unmatched_ledger_rows": len(sections["ledger"]) - len(used_ledger_ids),
        "archive_user_recomputation_cny": str(user_cost),
        "external_archive_records": len(external),
        "external_archive_recomputation_cny": str(external_cost),
        "combined_archive_records": len(used_archive) + len(external),
        "combined_archive_recomputation_cny": str(user_cost + external_cost),
        "external_evidence_source": "existing public archive; not local user requests",
        "legacy_store_join_verified": True,
        "owner_execution_coverage_verified": False,
        "all_account_execution_coverage_verified": False,
        "supplier_authenticity_or_finality_certified": False,
        "cache_creation_supplier_observation": "not provided; local values are not supplier-observed zeroes",
        "financial_boundary": "Private store bindings and archived arithmetic only; no execution events, frozen estimates, customer ledgers or supplier verification states are changed.",
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--snapshot", type=Path, required=True)
    parser.add_argument("--archive-records", type=Path, required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    result = audit(args.snapshot.read_bytes(), args.archive_records.read_bytes())
    args.out.parent.mkdir(parents=True, exist_ok=True)
    fd = os.open(args.out, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as target:
        json.dump(result, target, ensure_ascii=False, indent=2)
        target.write("\n")
    print("PASS: private legacy bindings verified for %d user records; owner execution coverage remains unverified" % result["joined_user_records"])


if __name__ == "__main__":
    try:
        main()
    except AuditFailure as error:
        print("FAIL: " + str(error), file=sys.stderr)
        raise SystemExit(1)
    except Exception as error:
        # Raw IDs, source models and private values must not leak on failure.
        print("FAIL: malformed or unavailable evidence (" + type(error).__name__ + ")", file=sys.stderr)
        raise SystemExit(1)
