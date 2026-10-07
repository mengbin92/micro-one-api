#!/usr/bin/env python3
"""Regression tests for incomplete or misleading browser acceptance reports."""
import copy
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("iam_result", ROOT / "check-iam-browser-result.py")
checker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(checker)
CONTRACT = json.loads((ROOT / "iam-browser-contract.json").read_text())


class AcceptanceResultTests(unittest.TestCase):
    def setUp(self):
        self.events = [
            {"Package": checker.PACKAGE, "Test": checker.TEST, "Action": "run"},
            {"Package": checker.PACKAGE, "Test": checker.TEST, "Action": "pass"},
            {"Package": checker.PACKAGE, "Action": "pass"},
        ]
        self.report = {
            "stats": {"expected": len(CONTRACT["titles"]), "skipped": 0, "unexpected": 0, "flaky": 0},
            "suites": [{"suites": [{"specs": [
                {"file": CONTRACT["file"], "title": title, "tests": [{
                    "projectName": CONTRACT["project"], "expectedStatus": "passed",
                    "status": "expected", "results": [{"status": "passed"}],
                }]} for title in CONTRACT["titles"]
            ]}]}],
        }

    def validate(self):
        return checker.validate(self.events, self.report, CONTRACT)

    def first_test(self):
        return self.report["suites"][0]["suites"][0]["specs"][0]["tests"][0]

    def test_complete_nested_report_passes(self):
        self.assertEqual(self.validate(), len(CONTRACT["titles"]))

    def test_reduced_matrix_cannot_pass_even_with_consistent_stats(self):
        self.report["suites"][0]["suites"][0]["specs"] = self.report["suites"][0]["suites"][0]["specs"][:1]
        self.report["stats"]["expected"] = 1
        with self.assertRaises(ValueError):
            self.validate()

    def test_expected_failure_is_not_an_actual_pass(self):
        test = self.first_test()
        test["expectedStatus"] = "failed"
        test["results"][0]["status"] = "failed"
        with self.assertRaises(ValueError):
            self.validate()

    def test_summary_cannot_hide_failed_or_missing_execution(self):
        for results in ([], [{"status": "failed"}], [{"status": "skipped"}], [{"status": "timedOut"}]):
            with self.subTest(results=results):
                self.first_test()["results"] = results
                with self.assertRaises(ValueError):
                    self.validate()

    def test_equal_count_cannot_hide_missing_or_unreviewed_cases(self):
        specs = self.report["suites"][0]["suites"][0]["specs"]
        for title in (specs[1]["title"], "unreviewed replacement scenario"):
            with self.subTest(title=title):
                specs[0]["title"] = title
                with self.assertRaises(ValueError):
                    self.validate()

    def test_wrong_file_or_project_rejected(self):
        for field in ("file", "project"):
            with self.subTest(field=field):
                bad = copy.deepcopy(self.report)
                if field == "file":
                    bad["suites"][0]["suites"][0]["specs"][0]["file"] = "admin-smoke.spec.ts"
                else:
                    bad["suites"][0]["suites"][0]["specs"][0]["tests"][0]["projectName"] = "other-browser"
                with self.assertRaises(ValueError):
                    checker.validate(self.events, bad, CONTRACT)

    def test_skip_failure_or_missing_go_test_rejected(self):
        for action in ("skip", "fail", "missing"):
            with self.subTest(action=action):
                events = copy.deepcopy(self.events)
                if action == "missing":
                    events = events[2:]
                else:
                    events[1]["Action"] = action
                with self.assertRaises(ValueError):
                    checker.validate(events, self.report, CONTRACT)

    def test_failed_package_rejected_after_test_pass(self):
        self.events[2]["Action"] = "fail"
        with self.assertRaises(ValueError):
            self.validate()

    def test_skipped_flaky_unexpected_or_zero_summary_rejected(self):
        for key in ("skipped", "flaky", "unexpected", "expected"):
            with self.subTest(key=key):
                bad = copy.deepcopy(self.report)
                bad["stats"][key] = 0 if key == "expected" else 1
                with self.assertRaises(ValueError):
                    checker.validate(self.events, bad, CONTRACT)

    def test_unknown_summary_values_are_not_zero(self):
        for value in (False, None, "0"):
            with self.subTest(value=value):
                self.report["stats"]["skipped"] = value
                with self.assertRaises(ValueError):
                    self.validate()


class WorkflowSelectionTests(unittest.TestCase):
    def select(self, path, fail=False):
        workflow = (ROOT.parent / ".github/workflows/pull-request-e2e.yml").read_text()
        script = textwrap.dedent(workflow.split("name: Classify changed paths", 1)[1].split("run: |", 1)[1].split("\n  affected-e2e:", 1)[0])
        with tempfile.TemporaryDirectory(prefix="iam-workflow-filter-") as directory:
            git = Path(directory) / "git"
            git.write_text('#!/usr/bin/env python3\nimport os,sys\nif os.environ["FIXTURE_DIFF_FAIL"] == "1": sys.exit(128)\nprint(os.environ["FIXTURE_CHANGED"])\n')
            git.chmod(0o700)
            output = Path(directory) / "output"
            output.write_text("")
            env = {**os.environ, "PATH": directory + os.pathsep + os.environ["PATH"],
                   "FIXTURE_CHANGED": path, "FIXTURE_DIFF_FAIL": str(int(fail)),
                   "BASE_SHA": "fixture-base", "HEAD_SHA": "fixture-head", "GITHUB_OUTPUT": str(output)}
            result = subprocess.run(["bash", "-e", "-o", "pipefail"], input=script, text=True, env=env, capture_output=True)
            return result.returncode, dict(line.split("=", 1) for line in output.read_text().splitlines())

    def test_diff_error_is_a_failed_gate(self):
        code, output = self.select("", fail=True)
        self.assertNotEqual(code, 0, "a missing base/head diff must not silently skip IAM acceptance")
        self.assertEqual(output, {})

    def test_iam_dependencies_selected_and_docs_only_skipped(self):
        for path in ("app/billing/internal/biz/reconciliation.go", "internal/integration/iam_c_browser_test.go",
                     "platform/authz/client.go", "domain/authorization/enforcer.go", "api/identity/v1/iam.proto",
                     "web/playwright.rbac.config.ts", "Makefile", "scripts/tool-versions.env",
                     "scripts/iam-browser-contract.json", "scripts/check-iam-browser-result.py",
                     "scripts/test-iam-browser.sh", "scripts/test-iam-browser-result.py",
                     ".github/workflows/ci.yml", ".github/workflows/e2e.yml", ".github/workflows/pull-request-e2e.yml", "docs/TODO.md"):
            with self.subTest(path=path):
                code, output = self.select(path)
                self.assertEqual(code, 0)
                expected = "false" if path == "docs/TODO.md" else "true"
                self.assertEqual(output["iam"], expected)
                self.assertEqual(output["any"], expected)


if __name__ == "__main__":
    unittest.main()
