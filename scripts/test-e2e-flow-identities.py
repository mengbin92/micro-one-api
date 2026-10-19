#!/usr/bin/env python3
"""Check real E2E/Lite startup wiring without building or starting containers."""
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parent.parent


class E2EIdentityTest(unittest.TestCase):
    def test_q3_environment_provisions_receiver_trust(self):
        script = (ROOT / "scripts/benchmark/run-q3-ci.sh").read_text()
        writer = script[script.index("write_environment() {"):script.index("\nreset_samples() {")]
        with tempfile.TemporaryDirectory() as scratch:
            checkout = Path(scratch)
            (checkout / "deployments/docker-compose").mkdir(parents=True)
            env = dict(os.environ, repo_root=str(ROOT), root_password="q3-test-root",
                       redis_password="q3-test-redis", admin_token="q3-test-admin",
                       service_token="q3-test-shared", jwt_secret="q3-test-jwt",
                       channel_key=os.urandom(16).hex(), user_password="q3-test-user")
            subprocess.run(["bash", "-euc", writer + '\nwrite_environment "$1"', "q3-test", str(checkout)],
                           env=env, check=True, capture_output=True, text=True)
            env_file = checkout / "deployments/docker-compose/.env"
            result = subprocess.run(["python3", str(ROOT / "scripts/check-service-identities.py"),
                                     "--env", str(env_file)], capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            config = json.loads(subprocess.check_output([
                "docker", "compose", "--env-file", str(env_file),
                "-f", str(ROOT / "deployments/docker-compose/docker-compose.yml"),
                "-f", str(ROOT / "scripts/benchmark/q3-compose.yml"), "config", "--format", "json"]))
            services = config["services"]
            relay = services["relay-gateway"]["environment"]["SERVICE_IDENTITY_TOKEN"]
            self.assertTrue(relay, "Q3 relay has no identity; execution claims return 503")
            for owner in ("channel-service", "identity-service", "billing-service", "log-service", "config-service"):
                callers = json.loads(services[owner]["environment"]["SERVICE_CALLER_TOKENS"])
                self.assertEqual(callers.get("relay"), relay, owner + " does not trust the Q3 relay")
            self.assertEqual(env_file.stat().st_mode & 0o777, 0o600)

    def test_lite_smoke_provisions_service_identities_before_building(self):
        with tempfile.TemporaryDirectory() as scratch:
            scratch = Path(scratch)
            docker = scratch / "docker"
            docker.write_text('''#!/usr/bin/env python3
import json, os, subprocess, sys
from pathlib import Path
args = sys.argv[1:]
if "config" in args:
    config = json.loads(subprocess.check_output(
        [os.environ["TEST_DOCKER"]] + args[:args.index("config")] + ["config", "--format", "json"]))
    services = config["services"]
    relay = services["relay-gateway"]["environment"]["SERVICE_IDENTITY_TOKEN"]
    assert relay and relay != services["relay-gateway"]["environment"]["SERVICE_TOKEN"]
    for owner in ("channel-service", "identity-service", "billing-service", "log-service", "config-service"):
        callers = json.loads(services[owner]["environment"]["SERVICE_CALLER_TOKENS"])
        assert callers.get("relay") == relay, owner + " does not trust the Lite relay"
    Path(os.environ["TEST_RESULT"]).write_text("PASS")
    sys.exit(1)  # Stop before any build, container or HTTP operation.
assert "build" not in args and "up" not in args
''')
            docker.chmod(0o700)
            result_file = scratch / "result"
            env = dict(os.environ, PATH=str(scratch) + os.pathsep + os.environ["PATH"],
                       TEST_DOCKER=shutil.which("docker"), TEST_RESULT=str(result_file), TMPDIR=str(scratch))
            result = subprocess.run(["python3", str(ROOT / "scripts/test-lite-smoke.py")],
                                    env=env, capture_output=True, text=True, timeout=30)
            self.assertEqual(result.returncode, 1, result.stderr)
            self.assertTrue(result_file.exists(), result.stdout + result.stderr)
            self.assertEqual(result_file.read_text(), "PASS")

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
    [os.environ["TEST_DOCKER"], "compose", "--env-file", os.environ["TEST_COMPOSE_ENV"]]
    + args[:args.index("up")] + ["config", "--format", "json"]))
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
                       TEST_COMPOSE_ENV=str(ROOT / "deployments/docker-compose/.env.example"),
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
