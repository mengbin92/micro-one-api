#!/usr/bin/env python3
"""Reject false-green mutation checks without invoking Go or touching sources."""

from dataclasses import replace
import importlib.util
import io
import json
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SPEC = importlib.util.spec_from_file_location("test_health", Path(__file__).with_name("test-health.py"))
HEALTH = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = HEALTH
SPEC.loader.exec_module(HEALTH)
CASE = HEALTH.MUTATIONS[0]
PACKAGE = "micro-one-api/" + CASE.package.removeprefix("./")
ASSERTION = "    " + CASE.test_file + ":123: " + CASE.assertion + "\n"


def result(actions=("run", "fail"), code=1, output=ASSERTION, extra=(), stderr=""):
    events = [{"Action": "run", "Package": PACKAGE}]
    for action in actions:
        if action == "fail" and output:
            events.append({"Action": "output", "Package": PACKAGE, "Test": CASE.test, "Output": output})
        events.append({"Action": action, "Package": PACKAGE, "Test": CASE.test})
    events.extend({"Package": PACKAGE, **event} for event in extra)
    events.append({"Action": "fail" if code else "pass", "Package": PACKAGE})
    return subprocess.CompletedProcess([], code, "".join(json.dumps(e) + "\n" for e in events), stderr)


class ResultTest(unittest.TestCase):
    def test_baseline_requires_successful_designated_test(self):
        HEALTH.check_result(result(("run", "pass"), 0, ""), CASE, False)
        for sample in (result(), result(("run", "skip"), 0), result((), 0),
                       result(("run", "pass"), 0, extra=({"Action": "fail", "Test": "TestOther"},))):
            with self.subTest(sample=sample.stdout), self.assertRaises(ValueError):
                HEALTH.check_result(sample, CASE, False)

    def test_mutation_requires_designated_business_assertion(self):
        HEALTH.check_result(result(), CASE, True)
        bad_results = {
            "survived": result(("run", "pass"), 0),
            "skipped": result(("run", "skip")),
            "no selected test": result(()),
            "different assertion": result(output="idempotency_test.go:123: database unavailable\n"),
            "no assertion source": result(output=CASE.assertion),
            "wrong assertion source": result(output="other_test.go:123: " + CASE.assertion),
            "wrong exit code": result(code=2),
            "panic": result(stderr="panic: nil pointer dereference"),
            "timeout": result(stderr="panic: test timed out after 60s"),
            "build error": result(stderr="FAIL package [build failed]"),
            "build failure event": result(extra=({"Action": "build-fail"},)),
            "another failing test": result(extra=({"Action": "fail", "Test": "TestOther"},)),
            "another failing package": result(extra=({"Action": "fail", "Package": "other/package"},)),
            "pass and fail": result(("run", "pass", "fail")),
        }
        for name, sample in bad_results.items():
            with self.subTest(name=name), self.assertRaises(ValueError):
                HEALTH.check_result(sample, CASE, True)

    def test_truncated_or_invalid_go_output_is_not_a_kill(self):
        incomplete = result()
        incomplete.stdout = "\n".join(incomplete.stdout.splitlines()[:-1])
        invalid = subprocess.CompletedProcess([], 1, "not JSON\n", "")
        for sample in (incomplete, invalid):
            with self.subTest(sample=sample.stdout), self.assertRaises(ValueError):
                HEALTH.check_result(sample, CASE, True)


class MutationTest(unittest.TestCase):
    def test_anchor_must_exist_once(self):
        self.assertEqual("before " + CASE.after + " after", HEALTH.mutate("before " + CASE.before + " after", CASE))
        for source in ("unrelated source", CASE.before + CASE.before):
            with self.subTest(source=source), self.assertRaises(ValueError):
                HEALTH.mutate(source, CASE)

    def test_main_isolates_mutation_and_restores_after_failure(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            original = root / "user-source.go"
            original.write_text(CASE.before + "\n")
            report_dir = (root / "evidence").resolve()
            second = replace(CASE, name="second-case")
            calls = []

            def copy_checkout(repo):
                target = repo / CASE.source
                target.parent.mkdir(parents=True)
                shutil.copy2(original, target)

            def run_test(repo, case, mutated, output_dir):
                self.assertNotEqual(repo, root)
                self.assertEqual(report_dir, output_dir)
                self.assertEqual((case.after if mutated else case.before) + "\n", (repo / case.source).read_text())
                self.assertEqual(CASE.before + "\n", original.read_text())
                calls.append((case.name, mutated))
                if case == CASE and mutated:
                    raise ValueError("simulated survived mutation")

            with patch.object(HEALTH, "MUTATIONS", (CASE, second)), \
                    patch.object(HEALTH, "copy_checkout", copy_checkout), \
                    patch.object(HEALTH, "run_test", run_test), \
                    patch.object(sys, "argv", ["test-health.py", "--output-dir", str(report_dir)]), \
                    patch("sys.stdout", io.StringIO()):
                self.assertEqual(1, HEALTH.main())
            report = json.loads((report_dir / "report.json").read_text())
            self.assertEqual("failed", report["status"])
            self.assertEqual(["failed", "caught"], [entry["status"] for entry in report["cases"]])
            self.assertEqual([(CASE.name, False), (CASE.name, True), (second.name, False), (second.name, True)], calls)
            self.assertEqual(CASE.before + "\n", original.read_text())

    def test_empty_case_list_cannot_report_success(self):
        with tempfile.TemporaryDirectory() as directory, \
                patch.object(HEALTH, "MUTATIONS", ()), \
                patch.object(HEALTH, "copy_checkout") as copy, \
                patch.object(sys, "argv", ["test-health.py", "--output-dir", directory]), \
                patch("sys.stdout", io.StringIO()):
            self.assertEqual(1, HEALTH.main())
            copy.assert_not_called()
            report = json.loads((Path(directory) / "report.json").read_text())
            self.assertEqual("failed", report["status"])
            self.assertIn("no mutation", report["error"])


class SnapshotTest(unittest.TestCase):
    def test_snapshot_preserves_working_inputs_and_excludes_secrets(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory) / "source"
            root.mkdir()
            files = {
                ".gitignore": ".env\nnode_modules/\n*.pb.go\nwire_gen.go\n",
                "biz/source.go": "original source\n",
                "testdata/fixture.json": '{"expected": 42}\n',
                "api/v1/resource.pb.go": "generated proto input\n",
                "cmd/service/wire_gen.go": "generated wire input\n",
                ".env": "SECRET=local-only\n",
                "node_modules/dependency/index.js": "ignored dependency\n",
            }
            for name, content in files.items():
                source = root / name
                source.parent.mkdir(parents=True, exist_ok=True)
                source.write_text(content)
            subprocess.run(["git", "init", "-q"], cwd=root, check=True, capture_output=True)
            subprocess.run(["git", "add", ".gitignore", "biz/source.go"], cwd=root, check=True, capture_output=True)
            files["biz/source.go"] = "locally edited source\n"
            (root / "biz/source.go").write_text(files["biz/source.go"])
            snapshot = Path(directory) / "snapshot"
            with patch.object(HEALTH, "ROOT", root):
                HEALTH.copy_checkout(snapshot)
            for name in ("biz/source.go", "testdata/fixture.json", "api/v1/resource.pb.go", "cmd/service/wire_gen.go"):
                self.assertEqual(files[name], (snapshot / name).read_text(), name)
            for name in (".git", ".env", "node_modules"):
                self.assertFalse((snapshot / name).exists(), name)
            for name, content in files.items():
                self.assertEqual(content, (root / name).read_text(), name)


if __name__ == "__main__":
    unittest.main()
