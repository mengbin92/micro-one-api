#!/usr/bin/env python3
"""Exercise the pre-push gate without network access or a real push."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class PrePushTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.bin = self.root / "bin"
        self.tools = self.root / "go tools"
        self.bin.mkdir()
        self.tools.mkdir()
        for command in ["dirname", "grep", "mktemp", "cat", "mkdir", "mv", "rm"]:
            (self.bin / command).symlink_to(shutil.which(command))
        self.calls = self.root / "calls"
        self.env = {
            **os.environ,
            "PATH": str(self.bin),
            "HOOK_TEST_ROOT": str(ROOT),
            "HOOK_TEST_TOOLS": str(self.tools),
            "HOOK_TEST_CALLS": str(self.calls),
            "HOOK_TEST_GOPATH": str(self.root / "first") + ":/unused",
            "HOOK_TEST_GOBIN": str(self.tools),
            "HOOK_TEST_SCAN_EXIT": "0",
            "HOOK_TEST_INSTALL_EXIT": "0",
        }
        versions = dict(line.split("=", 1) for line in
                        (ROOT / "scripts/tool-versions.env").read_text().splitlines()
                        if line and not line.startswith("#"))
        self.env["HOOK_TEST_GOSEC_VERSION"] = versions["GOSEC_VERSION"]
        self.env["HOOK_TEST_TOOLS_VERSION"] = versions["GOSEC_X_TOOLS_VERSION"]
        self.stub(self.bin / "git", 'printf "%s\\n" "$HOOK_TEST_ROOT"\n')
        self.stub(self.bin / "go", '''case "$*" in
  "env GOBIN") printf '%s\\n' "$HOOK_TEST_GOBIN" ;;
  "env GOPATH") printf '%s\\n' "$HOOK_TEST_GOPATH" ;;
  version\ -m\ *)
    [ -x "$3" ] || exit 1
    printf '\\tdep\\tgithub.com/securego/gosec/v2\\t%s\\thash\\n' "$HOOK_TEST_GOSEC_VERSION"
    printf '\\tdep\\tgolang.org/x/tools\\t%s\\thash\\n' "$HOOK_TEST_TOOLS_VERSION"
    ;;
  build\ *)
    printf '%s\\n' "$*" >> "$HOOK_TEST_CALLS"
    /bin/cat go.mod >> "$HOOK_TEST_CALLS"
    [ "$HOOK_TEST_INSTALL_EXIT" -eq 0 ] || exit "$HOOK_TEST_INSTALL_EXIT"
    while [ "$1" != "-o" ]; do shift; done
    /bin/cp "$HOOK_TEST_TOOLS/gosec.fixture" "$2"
    ;;
  *) exit 2 ;;
esac
''')
        self.stub(self.tools / "gosec.fixture", '''printf '%s\\n' "$PWD" "$*" >> "$HOOK_TEST_CALLS"
exit "$HOOK_TEST_SCAN_EXIT"
''')

    def stub(self, path, body):
        path.write_text("#!/bin/sh\n" + body)
        path.chmod(0o755)

    def installed(self):
        (self.tools / "gosec").symlink_to(self.tools / "gosec.fixture")

    def run_hook(self):
        return subprocess.run(
            ["/bin/sh", str(ROOT / ".githooks/pre-push")],
            cwd=ROOT / "web", env=self.env, capture_output=True,
            text=True, timeout=10,
        )

    def test_finds_gobin_outside_path_and_scans_repo_root(self):
        self.installed()
        result = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls.read_text().splitlines(), [
            str(ROOT), "-quiet -exclude-generated -exclude=G104 ./...",
        ])

    def test_uses_first_gopath_when_gobin_is_empty(self):
        self.installed()
        target = self.root / "first"
        target.mkdir()
        target_bin = target / "bin"
        target_bin.mkdir()
        shutil.copy2(self.tools / "gosec.fixture", target_bin / "gosec")
        self.env["HOOK_TEST_GOBIN"] = ""
        result = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertNotIn("install", self.calls.read_text())

    def test_installs_pinned_version_when_missing(self):
        result = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        version = next(line.split("=", 1)[1] for line in
                       (ROOT / "scripts/tool-versions.env").read_text().splitlines()
                       if line.startswith("GOSEC_VERSION="))
        self.assertIn(f"github.com/securego/gosec/v2 {version}", self.calls.read_text())
        self.assertIn("golang.org/x/tools " + self.env["HOOK_TEST_TOOLS_VERSION"],
                      self.calls.read_text())

    def test_rebuilds_incompatible_installed_reader(self):
        self.installed()
        self.env["HOOK_TEST_TOOLS_VERSION"] = "v0.48.0"
        result = self.run_hook()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("build -mod=mod", self.calls.read_text())

    def test_install_failure_blocks_push(self):
        self.env["HOOK_TEST_INSTALL_EXIT"] = "1"
        result = self.run_hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("-quiet", self.calls.read_text())

    def test_scan_failure_blocks_push(self):
        self.installed()
        self.env["HOOK_TEST_SCAN_EXIT"] = "1"
        result = self.run_hook()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("gosec reported issues", result.stderr)


if __name__ == "__main__":
    unittest.main()
