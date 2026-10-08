#!/usr/bin/env python3
"""Exercise deploy argument handling with local stubs; never contact Docker/SSH."""

import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import shutil

SCRIPT = Path(__file__).resolve().with_name("deploy-update.sh")


class DeployArgumentsTest(unittest.TestCase):
    def run_deploy(self, services=("channel-service",), failure="", configured=True):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            (root / "scripts").mkdir()
            script = root / "scripts/deploy-update.sh"
            shutil.copyfile(SCRIPT, script)
            (root / "scripts/rbac-source-digest.py").write_text("print('a' * 64)\n")
            compose = root / "target/docker-compose"
            compose.mkdir(parents=True)
            config = compose / "docker-compose.yml"
            original = 'services:\n  channel-service:\n    image: registry/channel:fixed-old\n    environment:\n      KEEP: untouched\n  billing-service:\n    image: registry/billing:fixed-old\n'
            config.write_text(original)
            calls = root / "calls"
            stubs = {
                "git": '#!/bin/bash\necho deadbeef\n',
                "scp": '#!/bin/bash\necho "scp $*" >> "$DEPLOY_TEST_CALLS"\n[[ "$DEPLOY_TEST_FAILURE" != upload ]]\n',
                "ssh": '''#!/bin/bash
while [[ "$1" = -o ]]; do shift 2; done
echo "ssh $*" >> "$DEPLOY_TEST_CALLS"
shift
DEPLOY_TEST_REMOTE=1 "$@"
''',
                "docker": '''#!/bin/bash
echo "docker remote=${DEPLOY_TEST_REMOTE:-0} $*" >> "$DEPLOY_TEST_CALLS"
case "$1 $2" in
  'inspect '*)
    case "$*" in
      *'{{.State.Running}}'*) echo true ;;
      *'{{.State.Status}}'*)
        if [[ "$DEPLOY_TEST_FAILURE" = health ]]; then echo 'running unhealthy'; else echo 'running healthy'; fi ;;
      *'{{.Image}}'*)
        if [[ -f "$DEPLOY_TEST_UPDATED.${2#test-container-}" ]]; then printf 'sha256:%064d\n' 2; else printf 'sha256:%064d\n' 1; fi ;;
      *'{{.Id}}'*) printf 'sha256:%064d\n' 2 ;;
    esac ;;
  'compose ps') echo "test-container-$4" ;;
  'compose config')
    if [[ "$3" = --format && "$4" = json ]]; then
      python3 - <<'PY'
import json, os, re
images = dict(re.findall(r'^  ([a-z-]+):\\n    image: (.+)$', open('docker-compose.yml').read(), re.M))
images['redis'] = 'redis:8.4'
failure = os.environ['DEPLOY_TEST_FAILURE']
if failure == 'override': images['channel-service'] = 'registry/shadow:old'
if failure == 'second-override': images['billing-service'] = 'registry/shadow:old'
print(json.dumps({'services': {name: {'image': image} for name, image in images.items()}}))
PY
    fi ;;
  'compose up') touch "$DEPLOY_TEST_UPDATED.${!#}" ;;
  'tag '*) [[ "$DEPLOY_TEST_FAILURE" != backup ]] ;;
  'load '*) [[ "$DEPLOY_TEST_FAILURE" != load ]] ;;
  'save '*) [[ "$DEPLOY_TEST_FAILURE" != save ]] ;;
esac
''',
            }
            for name, body in stubs.items():
                path = root / name
                path.write_text(body)
                path.chmod(0o755)
            env = {k: v for k, v in os.environ.items() if not k.startswith("DEPLOY_")}
            env.update(PATH=f"{root}:{env['PATH']}", DEPLOY_TEST_CALLS=str(calls), DEPLOY_TEST_FAILURE=failure, DEPLOY_TEST_UPDATED=str(root / "updated"))
            if configured:
                # Exercise the same .env coordinate reader as a normal run.
                (root / ".env").write_text(f'DEPLOY_REMOTE_SERVER="deploy@test.invalid"\nDEPLOY_REMOTE_DIR="{root}/target"\n')
            result = subprocess.run(["bash", str(script), *services], env=env, capture_output=True, text=True, timeout=10)
            backups = list(compose.glob("docker-compose.yml.rollback-*"))
            if backups:
                self.assertEqual(len(backups), 1)
                self.assertEqual(backups[0].read_text(), original, "multi-service run must preserve the initial Compose rollback")
            return result, calls.read_text() if calls.exists() else "", config.read_text(), original

    def test_multiple_services_keep_one_original_config_backup(self):
        result, calls, config, _ = self.run_deploy(services=("channel-service", "billing-service"))
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(config.count("image: docker-compose-"), 2)
        self.assertEqual(calls.count("docker remote=1 tag sha256:" + f"{1:064d}"), 2)

    def test_fixed_image_and_actual_running_rollback(self):
        result, calls, config, original = self.run_deploy()
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("deploy@test.invalid", calls)
        self.assertIn("--platform linux/amd64", calls)
        self.assertIn("--label micro-one-api.source.digest=" + "a" * 64, calls)
        self.assertIn("docker remote=1 tag sha256:" + f"{1:064d}" + " docker-compose-channel-service:rollback-", calls)
        self.assertIn("image: docker-compose-channel-service:deploy-", config)
        self.assertIn("image: registry/billing:fixed-old", config)
        self.assertIn("KEEP: untouched", config)
        self.assertIn("up -d --no-deps --no-build --pull never channel-service", calls)
        self.assertIn("compose config --format json", calls)
        self.assertNotIn("compose config --images", calls)
        self.assertNotIn("docker remote=1 build", calls)
        self.assertNotIn("docker remote=1 pull", calls)
        self.assertIn("channel-service image=sha256:", result.stdout)

    def test_second_service_failure_preserves_first_service_pin(self):
        result, calls, config, _ = self.run_deploy(services=("channel-service", "billing-service"), failure="second-override")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("image: docker-compose-channel-service:deploy-", config)
        self.assertIn("image: registry/billing:fixed-old", config)
        self.assertEqual(calls.count("compose up"), 1)
        self.assertNotIn("Deployment completed!", result.stdout)

    def test_missing_coordinates_fail_before_external_calls(self):
        result, calls, _, _ = self.run_deploy(configured=False)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual(calls, "")

    def test_failures_stop_deployment(self):
        for failure in ("save", "upload", "backup", "load", "override", "health"):
            with self.subTest(failure=failure):
                result, calls, config, original = self.run_deploy(failure=failure)
                self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
                if failure != "health":
                    self.assertNotIn("compose up", calls)
                    self.assertEqual(config, original)
                self.assertNotIn("Deployment completed!", result.stdout)

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
