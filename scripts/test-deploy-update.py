#!/usr/bin/env python3
"""Exercise deploy argument handling with local stubs; never contact Docker/SSH."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().with_name("deploy-update.sh")


class DeployArgumentsTest(unittest.TestCase):
    def test_invalid_service_rejected_before_any_deployment(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            calls = root / "calls"
            # Stubs also consume SSH heredocs. No external application is used.
            for name in ("docker", "ssh", "scp"):
                stub = root / name
                stub.write_text('#!/bin/bash\necho "$0 $*" >> "$DEPLOY_TEST_CALLS"\nif [ "${0##*/}" = ssh ]; then cat >/dev/null; fi\n')
                stub.chmod(0o755)
            env = {**os.environ, "PATH": f"{root}:{os.environ['PATH']}", "DEPLOY_TEST_CALLS": str(calls)}
            result = subprocess.run(["bash", str(SCRIPT), "billing-service", "typo-service"], env=env, capture_output=True, text=True, timeout=10)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("Unknown service", result.stdout + result.stderr)
            self.assertFalse(calls.exists(), "invalid trailing service must not partially deploy preceding services")


if __name__ == "__main__":
    unittest.main()
