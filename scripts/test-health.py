#!/usr/bin/env python3
"""Prove selected existing tests reject deliberate production-code regressions."""

import argparse
from dataclasses import asdict, dataclass
import json
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]


@dataclass(frozen=True)
class Mutation:
    name: str
    source: str
    before: str
    after: str
    package: str
    test: str
    test_file: str
    assertion: str


# shortcut: only four critical regressions, add a case when a real bug escapes its tests.
MUTATIONS = (
    Mutation("duplicate-purchase", "app/billing/internal/biz/idempotency.go",
             'if requestID == "" {', 'if requestID != "" {',
             "./app/billing/internal/data", "TestPurchaseSubscription_ConcurrentDuplicateRequestChargesOnce",
             "idempotency_test.go",
             "exactly one of 8 concurrent purchases must succeed"),
    Mutation("subscription-expiry", "domain/subscription/biz/expiry_checker.go",
             "if sub.ExpiresAt <= now {", "if sub.ExpiresAt > now {",
             "./domain/subscription/biz", "TestSubscriptionExpiryChecker_MarksExpiredAndWarnsSoon",
             "expiry_checker_test.go",
             "notifications = 1, want 2"),
    Mutation("cache-read-cost", "app/billing/internal/biz/billing.go",
             "cacheReadCost := roundScaled(cacheRead, cacheReadPrice, multiplier)",
             "cacheReadCost := roundScaled(cacheRead, cacheReadPrice, multiplier) * 0",
             "./app/billing/internal/biz", "TestCalculateCanonicalCost_PerBucketRounding",
             "usage_semantics_cost_test.go",
             "expected: 45056"),
    Mutation("random-auto-key", "app/billing/internal/biz/idempotency.go",
             'requestID = "auto:" + randomHex(16)', 'requestID = "auto:" + strings.Repeat("0", 32)',
             "./app/billing/internal/biz", "TestLedgerDedupeKeyFor_EmptyRequestIDGetsDistinctAutoKeys",
             "idempotency_test.go",
             "auto keys must be distinct per call"),
)


def mutate(source, case):
    if source.count(case.before) != 1:
        raise ValueError(f"{case.name}: source anchor must match exactly once; review the case after code changes")
    return source.replace(case.before, case.after, 1)


def check_result(result, case, mutated):
    events = [json.loads(line) for line in result.stdout.splitlines() if line.strip()]
    if any(not isinstance(event, dict) for event in events):
        raise ValueError("invalid go test event stream")
    package = "micro-one-api/" + case.package.removeprefix("./")
    target = [e for e in events if e.get("Package") == package and e.get("Test") == case.test]
    actions = [event.get("Action") for event in target]
    if "run" not in actions or "skip" in actions:
        raise ValueError("designated test did not run or was skipped")
    terminals = [a for a in actions if a in ("pass", "fail")]
    package_terminals = [e.get("Action") for e in events if e.get("Package") == package
                         and not e.get("Test") and e.get("Action") in ("pass", "fail")]
    if not mutated:
        if (result.returncode != 0 or terminals != ["pass"] or package_terminals != ["pass"]
                or any(e.get("Action") in ("fail", "build-fail") for e in events)):
            raise ValueError("baseline must pass before a mutation can be credited")
        return
    output = "".join(event.get("Output", "") for event in target)
    all_output = result.stdout + result.stderr
    if (any(e.get("Action") == "build-fail" for e in events)
            or re.search(r"panic:|fatal error:|test timed out|\[build failed\]", all_output)):
        raise ValueError("build failure, panic or timeout is not an assertion failure")
    failures = {(e.get("Package"), e.get("Test")) for e in events if e.get("Action") == "fail"}
    if (result.returncode != 1 or terminals != ["fail"] or package_terminals != ["fail"]
            or failures != {(package, case.test), (package, None)} or case.assertion not in output
            or not re.search(re.escape(case.test_file) + r":\d+", output)):
        raise ValueError("mutation survived or failed outside the designated business assertion")


def run_test(repo, case, mutated, output_dir):
    phase = "mutant" if mutated else "baseline"
    command = ["go", "test", "-mod=readonly", "-json", "-count=1", "-timeout=60s",
               "-run", f"^{case.test}$", case.package]
    log = output_dir / f"{case.name}-{phase}.log"
    try:
        result = subprocess.run(command, cwd=repo, capture_output=True, text=True, timeout=240)
    except subprocess.TimeoutExpired as exc:
        log.write_bytes((exc.stdout or b"") + (exc.stderr or b""))
        raise ValueError(f"{phase} command timed out; see {log}") from exc
    log.write_text("$ " + " ".join(command) + "\n" + result.stdout + "\n" + result.stderr)
    check_result(result, case, mutated)


def copy_checkout(repo):
    # Include local edits/new files; exclude ignored secrets, dependencies and build outputs.
    names = subprocess.check_output(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=ROOT,
    )
    # Generated Go stubs are ignored in this repo but are required build inputs.
    names += subprocess.check_output(
        ["git", "ls-files", "--others", "--ignored", "--exclude-standard", "-z", "--", "*.pb.go", ":(glob)**/wire_gen.go"], cwd=ROOT,
    )
    for name in set(filter(None, names.decode().split("\0"))):
        source, target = ROOT / name, repo / name
        if source.is_file():
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, target)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output-dir", type=Path, help="retain JSON report and baseline/mutant logs here")
    args = parser.parse_args()
    output_dir = (args.output_dir or Path(tempfile.mkdtemp(prefix="test-health-report-"))).resolve()
    output_dir.mkdir(parents=True, exist_ok=True)
    report = {"status": "failed", "cases": []}
    print(f"Test-health evidence: {output_dir}", flush=True)
    try:
        if not MUTATIONS:
            raise ValueError("no mutation cases configured")
        with tempfile.TemporaryDirectory(prefix="test-health-checkout-") as scratch:
            repo = Path(scratch)
            copy_checkout(repo)
            for case in MUTATIONS:
                entry = {**asdict(case), "status": "failed"}
                report["cases"].append(entry)
                source = repo / case.source
                original = source.read_text()
                try:
                    broken = mutate(original, case)
                    print(f"{case.name}: baseline", flush=True)
                    run_test(repo, case, False, output_dir)
                    source.write_text(broken)
                    print(f"{case.name}: deliberate regression", flush=True)
                    run_test(repo, case, True, output_dir)
                    entry["status"] = "caught"
                    print(f"{case.name}: caught by {case.test}", flush=True)
                except (ValueError, OSError) as exc:
                    entry["error"] = str(exc)
                    print(f"{case.name}: FAIL: {exc}", flush=True)
                finally:
                    source.write_text(original)
        if len(report["cases"]) == len(MUTATIONS) and all(c["status"] == "caught" for c in report["cases"]):
            report["status"] = "passed"
    except (ValueError, OSError, subprocess.SubprocessError) as exc:
        report["error"] = str(exc)
    finally:
        (output_dir / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    print(f"Test-health {report['status']}: {output_dir / 'report.json'}", flush=True)
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
