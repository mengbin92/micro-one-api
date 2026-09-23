#!/usr/bin/env python3
"""Compare median k6 summaries and enforce the Q3 engineering gate."""

import argparse
import json
import statistics
import sys
from pathlib import Path


METRICS = {
    "throughput_req_per_second": ("http_reqs", "rate", "higher"),
    "http_error_rate": ("http_req_failed", "rate", "lower"),
    "aggregate_p95_ms": ("http_req_duration", "p(95)", "lower"),
    "chat_p95_ms": ("chat_duration_ms", "p(95)", "lower"),
    "models_p95_ms": ("models_duration_ms", "p(95)", "lower"),
    "healthz_p95_ms": ("healthz_duration_ms", "p(95)", "lower"),
    "dropped_iterations": ("dropped_iterations", "count", "zero"),
}


def parse_args():
    parser = argparse.ArgumentParser()
    parser.add_argument("--baseline", action="append", required=True, help="Baseline k6 summary JSON; repeat at least three times")
    parser.add_argument("--candidate", action="append", required=True, help="Candidate k6 summary JSON; repeat at least three times")
    parser.add_argument("--max-regression-percent", type=float, default=20.0)
    parser.add_argument("--max-error-rate", type=float, default=0.01)
    parser.add_argument("--output", help="Optional JSON report path")
    return parser.parse_args()


def samples(paths):
    if len(paths) < 3:
        raise ValueError("each version needs at least three summary files")
    result = {name: [] for name in METRICS}
    for name in paths:
        payload = json.loads(Path(name).read_text())
        metrics = payload.get("metrics", {})
        for output_name, (metric_name, value_name, _) in METRICS.items():
            try:
                value = metrics[metric_name]["values"][value_name]
            except KeyError as exc:
                raise ValueError(f"{name} is missing metrics.{metric_name}.values.{value_name}") from exc
            result[output_name].append(float(value))
    return result


def medians(values):
    return {name: statistics.median(items) for name, items in values.items()}


def main():
    args = parse_args()
    if args.max_regression_percent < 0:
        raise ValueError("max regression percent must be non-negative")
    if not 0 <= args.max_error_rate <= 1:
        raise ValueError("max error rate must be between 0 and 1")
    baseline_samples = samples(args.baseline)
    candidate_samples = samples(args.candidate)
    baseline = medians(baseline_samples)
    candidate = medians(candidate_samples)
    tolerance = args.max_regression_percent / 100
    checks = {}
    failures = []

    for name, (_, _, direction) in METRICS.items():
        old, new = baseline[name], candidate[name]
        if direction == "higher":
            limit = old * (1 - tolerance)
            passed = new >= limit
            detail = f">= {limit:.6g}"
        elif direction == "zero":
            limit = 0.0
            passed = new == 0
            detail = "== 0"
        elif name == "http_error_rate":
            relative_limit = old * (1 + tolerance) if old > 0 else args.max_error_rate
            limit = min(args.max_error_rate, relative_limit)
            passed = new <= limit
            detail = f"<= {limit:.6g}"
        else:
            limit = old * (1 + tolerance)
            passed = new <= limit
            detail = f"<= {limit:.6g}"
        checks[name] = {"baseline_median": old, "candidate_median": new, "required": detail, "passed": passed}
        print(f"{'PASS' if passed else 'FAIL'} {name}: baseline={old:.6g} candidate={new:.6g} required {detail}")
        if not passed:
            failures.append(name)

    report = {
        "schema_version": 1,
        "gate": {
            "minimum_runs_per_version": 3,
            "max_regression_percent": args.max_regression_percent,
            "max_error_rate": args.max_error_rate,
            "description": "Engineering admission gate; it is not a statistical significance claim.",
        },
        "baseline_files": args.baseline,
        "candidate_files": args.candidate,
        "baseline_samples": baseline_samples,
        "candidate_samples": candidate_samples,
        "checks": checks,
        "passed": not failures,
    }
    if args.output:
        Path(args.output).write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    if failures:
        print("Regression gate failed: " + ", ".join(failures), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"error: {exc}", file=sys.stderr)
        raise SystemExit(2)
