#!/usr/bin/env python3
"""Check the real E2E startup wiring without building or starting containers."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class E2EIdentityTest(unittest.TestCase):
    def test_compose_receivers_trust_dedicated_relay(self):
        with tempfile.TemporaryDirectory() as scratch:
            scratch = Path(scratch)
            compose = scratch / "docker-compose"
            compose.write_text('''#!/usr/bin/env python3
import json, os, subprocess, sys
from pathlib import Path
args = sys.argv[1:]
if "up" not in args:
    sys.exit(0)
config = json.loads(subprocess.check_output(
    [os.environ["TEST_DOCKER"], "compose"] + args[:args.index("up")] + ["config", "--format", "json"]))
services = config["services"]
relay = services["relay-gateway"]["environment"]["SERVICE_IDENTITY_TOKEN"]
assert relay, "E2E relay has no dedicated identity; execution claims will be denied"
assert relay != services["relay-gateway"]["environment"]["SERVICE_TOKEN"]
for owner in ("channel-service", "identity-service", "billing-service", "log-service", "config-service"):
    callers = json.loads(services[owner]["environment"]["SERVICE_CALLER_TOKENS"])
    assert callers.get("relay") == relay, owner + " does not trust the E2E relay"
Path(os.environ["TEST_RESULT"]).write_text("PASS")
sys.exit(1)  # Stop before any container or HTTP operation.
''')
            compose.chmod(0o700)
            sleep = scratch / "sleep"
            sleep.write_text("#!/bin/sh\nexit 0\n")
            sleep.chmod(0o700)
            result_file = scratch / "result"
            env = dict(os.environ, PATH=str(scratch) + os.pathsep + os.environ["PATH"],
                       TEST_DOCKER=shutil.which("docker"), TEST_RESULT=str(result_file),
                       MYSQL_ROOT_PASSWORD="e2e-test-password", ADMIN_TOKEN="e2e-test-admin",
                       SERVICE_TOKEN="e2e-test-shared", TMPDIR=str(scratch))
            result = subprocess.run(["bash", str(ROOT / "scripts/test-e2e-flow.sh"), "--suite"],
                                    env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertTrue(result_file.exists(), result.stdout + result.stderr)
            self.assertEqual(result_file.read_text(), "PASS")
            self.assertEqual(list(scratch.glob("e2e-identities.*")), [])


if __name__ == "__main__":
    unittest.main()
