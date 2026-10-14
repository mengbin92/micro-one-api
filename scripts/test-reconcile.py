#!/usr/bin/env python3
"""Run the reconciliation report gate offline through the real shell parser."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class ReconciliationReportGateTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.response = self.root / "response.json"
        fake_bin = self.root / "bin"
        fake_bin.mkdir()
        curl = fake_bin / "curl"
        curl.write_text('#!/bin/sh\ncat "$RECONCILE_TEST_RESPONSE"\n')
        curl.chmod(0o755)
        self.env = {
            **os.environ,
            "PATH": str(fake_bin) + os.pathsep + os.environ["PATH"],
            "RECONCILE_TEST_RESPONSE": str(self.response),
            "SERVICE_TOKEN": "offline-fixture-only",
            "BILLING_RECON_ENDPOINT": "https://offline-fixture.invalid/reconciliation",
        }

    def run_report(self, response):
        self.response.write_text(json.dumps(response))
        return subprocess.run(
            ["bash", str(ROOT / "scripts/reconcile/reconcile.sh"), "--skip-db-checks"],
            cwd=ROOT, env=self.env, capture_output=True, text=True, timeout=10,
        )

    def test_partial_checks_never_pass_when_discrepancy_arrays_are_empty(self):
        result = self.run_report({"status": "partial", "run_at": "2026-10-14T00:00:00Z", "error_message": "refund query unavailable"})
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("RESULT: PASS", result.stdout)

    def test_failed_or_unrecognized_response_never_passes(self):
        for response in [{"status": "failed"}, {"status": "running"}, {"error": "owner unavailable"}, {}]:
            with self.subTest(response=response):
                result = self.run_report(response)
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertNotIn("RESULT: PASS", result.stdout)

    def test_completed_owner_result_can_pass_with_no_discrepancies(self):
        result = self.run_report({"status": "completed", "run_at": "2026-10-14T00:00:00Z", "IssuesVisible": True})
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("RESULT: PASS", result.stdout)

    def test_discrepancies_still_fail(self):
        result = self.run_report({"status": "completed", "run_at": "2026-10-14T00:00:00Z", "IssuesVisible": True, "refund_inconsistencies": [{"trade_no": "fixture"}]})
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertIn("RESULT: FAIL", result.stdout)

    def test_hidden_discrepancies_never_count_as_zero(self):
        result = self.run_report({"status": "completed", "run_at": "2026-10-14T00:00:00Z", "IssuesVisible": False})
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("RESULT: PASS", result.stdout)

    def test_completed_with_error_or_malformed_arrays_never_passes(self):
        for extra in [{"error_message": "check unavailable"}, {"refund_inconsistencies": {}}, {"run_at": "unknown"}]:
            with self.subTest(extra=extra):
                result = self.run_report({"status": "completed", "run_at": "2026-10-14T00:00:00Z", "IssuesVisible": True, **extra})
                self.assertNotEqual(result.returncode, 0, result.stdout)
                self.assertNotIn("RESULT: PASS", result.stdout)

    def test_skipping_all_checks_cannot_declare_a_clean_period(self):
        result = subprocess.run(
            ["bash", str(ROOT / "scripts/reconcile/reconcile.sh"), "--skip-trigger", "--skip-db-checks"],
            cwd=ROOT, env=self.env, capture_output=True, text=True, timeout=10,
        )
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertNotIn("reconciliation finished clean", result.stdout)


if __name__ == "__main__":
    unittest.main()
