#!/usr/bin/env python3
"""Require the reviewed IAM matrix's actual passes, not only summary counts."""
import json
import sys
from pathlib import Path

TEST = "TestIAMCRealBrowserMatrix"
PACKAGE = "micro-one-api/internal/integration"


def validate(events, report, contract):
    actions = [event.get("Action") for event in events
               if event.get("Package") == PACKAGE and event.get("Test") == TEST]
    package_passed = any(event.get("Package") == PACKAGE and not event.get("Test")
                         and event.get("Action") == "pass" for event in events)
    if "run" not in actions or "pass" not in actions or "skip" in actions or "fail" in actions or not package_passed:
        raise ValueError("IAM Go test must execute and pass; missing, skipped or failed results are rejected")
    titles = contract["titles"]
    if not titles or len(set(titles)) != len(titles):
        raise ValueError("reviewed IAM scenario contract must contain unique titles")
    stats = report["stats"]
    if any(type(stats[key]) is not int for key in ("expected", "skipped", "unexpected", "flaky")):
        raise ValueError("Playwright result counters must be integers")
    if stats["expected"] != len(titles) or any(stats[key] != 0 for key in ("skipped", "unexpected", "flaky")):
        raise ValueError("the complete reviewed IAM matrix must pass without skips or flaky retries")

    def specs(suites):
        for suite in suites:
            yield from suite.get("specs", [])
            yield from specs(suite.get("suites", []))

    seen = []
    for spec in specs(report["suites"]):
        if spec["file"] != contract["file"]:
            raise ValueError("unexpected IAM spec file")
        for test in spec["tests"]:
            seen.append(spec["title"])
            if test["projectName"] != contract["project"] or test["expectedStatus"] != "passed" or test["status"] != "expected":
                raise ValueError("IAM scenarios must expect a pass in the reviewed browser project")
            results = test["results"]
            if not results or any(result["status"] != "passed" for result in results):
                raise ValueError("every IAM scenario must have an actual passed execution result")
    if sorted(seen) != sorted(titles):
        raise ValueError("missing, duplicate or unreviewed IAM scenarios")
    return len(seen)


def main():
    if len(sys.argv) != 3:
        raise SystemExit("usage: check-iam-browser-result.py <go-test.json> <playwright.json>")
    try:
        events = [json.loads(line) for line in Path(sys.argv[1]).read_text().splitlines() if line.strip()]
        report = json.loads(Path(sys.argv[2]).read_text())
        contract = json.loads(Path(__file__).with_name("iam-browser-contract.json").read_text())
        count = validate(events, report, contract)
    except (OSError, ValueError, KeyError, TypeError) as error:
        raise SystemExit(f"FAIL: {error}")
    print(f"PASS: complete IAM matrix executed; {count} actual Playwright passes, skipped=0")


if __name__ == "__main__":
    main()
