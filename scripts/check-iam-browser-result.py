#!/usr/bin/env python3
"""Reject a skipped or missing IAM matrix in a go test -json report."""
import json
import sys
from pathlib import Path

TEST = "TestIAMCRealBrowserMatrix"
PACKAGE = "micro-one-api/internal/integration"


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: check-iam-browser-result.py <go-test.json> <playwright.json>")
    events = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines() if line.strip()]
    actions = [event.get("Action") for event in events
               if event.get("Package") == PACKAGE and event.get("Test") == TEST]
    package_passed = any(event.get("Package") == PACKAGE and not event.get("Test")
                         and event.get("Action") == "pass" for event in events)
    if "run" not in actions or "pass" not in actions or "skip" in actions or "fail" in actions or not package_passed:
        raise SystemExit("FAIL: IAM browser matrix must execute and pass; missing, skipped or failed results are rejected")
    stats = json.loads(Path(sys.argv[2]).read_text())["stats"]
    if stats["expected"] <= 0 or any(stats[key] != 0 for key in ("skipped", "unexpected", "flaky")):
        raise SystemExit("FAIL: every IAM Playwright scenario must execute and pass without skips or flaky retries")
    print(f"PASS: IAM browser matrix executed; {stats['expected']} Playwright scenarios passed, skipped=0")


if __name__ == "__main__":
    main()
