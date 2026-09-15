#!/usr/bin/env python3
"""Run real service binaries in a private Compose project; only the upstream is mocked."""
import argparse
import copy
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
SERVICES = {"relay-gateway": "relay-gateway", "admin-api": "admin", "identity-service": "identity",
            "channel-service": "channel", "billing-service": "billing", "config-service": "config",
            "log-service": "log", "monitor-worker": "monitor", "notify-worker": "notify"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--driver", choices=("mysql", "sqlite3"), default="mysql")
    parser.add_argument("--image", default="micro-one-api-routing-e2e:local")
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument("--build-only", action="store_true")
    parser.add_argument("--keep", action="store_true", help="retain only this test project for diagnosis")
    args = parser.parse_args()
    scratch = Path(tempfile.mkdtemp(prefix="routing-e2e-"))
    scratch.chmod(0o700)
    project = "routing-e2e-" + secrets.token_hex(4)
    print(f"[routing-e2e] project={project} driver={args.driver} private diagnostics={scratch}", flush=True)
    env = {key: value for key, value in os.environ.items() if not key.startswith("COMPOSE_")}
    repo = scratch / "repo"
    if not args.skip_build:
        # Include new task files and local edits, excluding ignored credentials and host artifacts.
        names = subprocess.check_output(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=ROOT).decode().split("\0")
        for name in set(filter(None, names)):
            src, dst = ROOT / name, repo / name
            if src.is_file():
                dst.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(src, dst)
        with (scratch / "build.log").open("w") as log:
            print("[routing-e2e] building isolated acceptance image", flush=True)
            result = subprocess.run(["docker", "build", "-t", args.image, "-f", str(repo / "test/e2e/routing/Dockerfile"), str(repo)],
                                    env=env, stdout=log, stderr=subprocess.STDOUT)
        if result.returncode:
            raise RuntimeError(f"acceptance image build failed; see {scratch}/build.log")
        print("[routing-e2e] build PASS", flush=True)
    if args.build_only:
        return
    suffix = ".lite" if args.driver == "sqlite3" else ""
    example = ROOT / "deployments/docker-compose" / (".env.lite.example" if suffix else ".env.example")
    settings = dict(line.split("=", 1) for line in example.read_text().splitlines() if line and not line.startswith("#") and "=" in line)
    for key in ("JWT_SECRET_KEY", "SERVICE_TOKEN", "ADMIN_TOKEN", "REDIS_PASSWORD", "MYSQL_ROOT_PASSWORD"):
        settings[key] = secrets.token_hex(20)
    settings.update(CHANNEL_ENCRYPTION_KEY=secrets.token_hex(16), INITIAL_ADMIN_PASSWORD="routing-e2e-password-123456",
                    INITIAL_ADMIN_USERNAME="admin", INITIAL_ADMIN_EMAIL="admin@routing.test", DATABASE_DRIVER=args.driver)
    dsn = f"root:{settings['MYSQL_ROOT_PASSWORD']}@tcp(mysql:3306)/oneapi?charset=utf8mb4&parseTime=True&loc=UTC"
    if args.driver == "sqlite3":
        dsn = "file:/data/oneapi.db?_busy_timeout=10000&_journal_mode=WAL"
    settings.update(DATABASE_DSN=dsn, ALIPAY_CERT_DIR=str(scratch / "cert"))
    (scratch / "cert").mkdir()
    env_file = scratch / "test.env"
    env_file.write_text("".join(f"{k}={v}\n" for k, v in settings.items()))
    env_file.chmod(0o600)
    # Render only test credentials. The calling shell cannot override examples or routing gates.
    child_env = {k: v for k, v in env.items() if k not in settings and not k.startswith(("RELAY_", "IDENTITY_", "BILLING_", "ADMIN_", "CHANNEL_", "SUBSCRIPTION_"))}
    base = json.loads(subprocess.check_output(["docker", "compose", "--env-file", str(env_file), "-f", str(ROOT / f"deployments/docker-compose/docker-compose{suffix}.yml"), "config", "--format", "json"], env=child_env))
    keep = set(SERVICES) | {"mysql", "redis", "migrate", "sqlite-init"}
    base["services"] = {k: v for k, v in base["services"].items() if k in keep}
    base.pop("name", None)
    relay_config = scratch / "relay.yaml"
    relay_config.write_text((ROOT / "configs/config.yaml").read_text() + "\nidempotency:\n  enabled: true\n  ttl: 24h\n")
    for section in ("networks", "volumes"):
        for spec in base.get(section, {}).values():
            spec.pop("name", None)
            spec.pop("external", None)
    for service, config in base["services"].items():
        config.pop("container_name", None)
        config.pop("ports", None)
        config.pop("build", None)
        config["restart"] = "no"
        if service in SERVICES:
            binary = SERVICES[service]
            config.update(image=args.image, entrypoint=[f"/out/{binary}"])
            config["environment"]["CONF_PATH"] = "/app/configs/config.yaml" if service == "relay-gateway" else f"/app/app/{binary}/configs/config.yaml"
            config["environment"].update(PROVIDER_DISABLE_SSRF_CHECK="true", ADMIN_WEB_ROOT="", INITIAL_ADMIN_PASSWORD=settings["INITIAL_ADMIN_PASSWORD"])
            # No production bind mounts; SQLite's project-private named volume is retained.
            config["volumes"] = [v for v in config.get("volumes", []) if v.get("type") == "volume"]
            if service == "relay-gateway":
                config["environment"].update(CONF_PATH="/fixture/relay.yaml", MODELS_PATH="/app/configs/models.yaml")
                config["volumes"].append({"type": "bind", "source": str(relay_config), "target": "/fixture/relay.yaml", "read_only": True})
            for key in list(config["environment"]):
                if key.endswith("_SCHEMA"):
                    config["environment"][key] = ""
        if service == "migrate":
            migration_dir = "/app/migrations/sqlite" if suffix else "/app/migrations"
            config.update(image=args.image, entrypoint=["/out/migrate", "-dir", migration_dir])
    base["services"]["mock-upstream"] = {"image": args.image, "entrypoint": ["/out/routingmock"], "networks": ["backend"]}
    base["services"]["relay-peer"] = copy.deepcopy(base["services"]["relay-gateway"])
    runner_env = {"ROUTING_E2E": "1", "DATABASE_DRIVER": args.driver, "DATABASE_DSN": dsn,
                  "SERVICE_TOKEN": settings["SERVICE_TOKEN"], "ADMIN_TOKEN": settings["ADMIN_TOKEN"],
                  "INITIAL_ADMIN_PASSWORD": settings["INITIAL_ADMIN_PASSWORD"], "ADMIN_HTTP_BASE": "http://admin-api:8000",
                  "RELAY_HTTP_BASE": "http://relay-gateway:8080", "IDENTITY_GRPC_ENDPOINT": "identity-service:9001",
                  "RELAY_PEER_HTTP_BASE": "http://relay-peer:8080",
                  "CHANNEL_GRPC_ENDPOINT": "channel-service:9002", "BILLING_GRPC_ENDPOINT": "billing-service:9004",
                  "REDIS_ADDR": "redis:6379", "REDIS_PASSWORD": settings["REDIS_PASSWORD"],
                  "GROUP_AUDIT_DSN": dsn, "GROUP_BACKFILL_DSN": dsn, "IDENTITY_SQL_DSN": dsn,
                  # Actual static billing base in app/billing/configs/config.yaml.
                  "ROUTING_E2E_BASE_RATIOS": '{"default":1,"vip":1,"svip":1}',
                  "PROMETHEUS_HTTP_BASE": "http://prometheus:9090",
                  "ROUTING_STATE": "/state/state.json"}
    if suffix:
        runner_env["GROUP_AUDIT_DSN"] = "/data/oneapi.db"
    for owner in ("IDENTITY", "CHANNEL", "BILLING"):
        runner_env[f"ROUTING_{owner}_DSN"] = "/data/oneapi.db" if suffix else dsn
    base.setdefault("volumes", {})["test_state"] = {}
    base["services"]["test-runner"] = {"image": args.image, "user": "0:0", "profiles": ["test"],
        "entrypoint": ["/out/routing-e2e"], "environment": runner_env, "networks": ["backend"],
        "volumes": ["test_state:/state"] + (["sqlite_data:/data"] if suffix else [])}
    # Run the shipped rule expressions and timings in the private network.
    # Mount copies so Docker's unprivileged Prometheus user can read them.
    monitoring = scratch / "monitoring"
    monitoring.mkdir(mode=0o755)
    for source, target in ((ROOT / "deploy/prometheus/prometheus.yml", "prometheus.yml"),
                           (ROOT / "deploy/prometheus/alerts/alerts.yml", "alerts.yml"),
                           (ROOT / "deploy/prometheus/alerts/routing.test.yml", "routing.test.yml")):
        shutil.copyfile(source, monitoring / target)
        (monitoring / target).chmod(0o644)
    base["services"]["prometheus"] = {
        "image": "prom/prometheus:v3.6.0", "networks": ["backend"], "restart": "no",
        "volumes": [{"type": "bind", "source": str(monitoring / filename),
                     "target": "/etc/prometheus/" + filename, "read_only": True}
                    for filename in ("prometheus.yml", "alerts.yml", "routing.test.yml")]}
    config_file = scratch / "compose.json"
    def save():
        config_file.write_text(json.dumps(base))
        config_file.chmod(0o600)
    save()
    compose = ["docker", "compose", "-p", project, "-f", str(config_file)]
    def run(*cmd, input=None, check=True):
        result = subprocess.run(compose + list(cmd), env=child_env, input=input, capture_output=True, text=True)
        with (scratch / "compose.log").open("a") as log:
            log.write(result.stdout + result.stderr)
        if check and result.returncode:
            raise RuntimeError(f"Compose command {cmd[:2]} failed; see {scratch}/compose.log")
        return result
    def gate(service, **values):
        base["services"][service]["environment"].update(values)
        if service == "relay-gateway":
            base["services"]["relay-peer"]["environment"].update(values)
        save()
    def helper(binary, *cmd, check=True, input=None):
        return run("run", "--rm", "-T", "--no-deps", "--entrypoint", "/out/"+binary, "test-runner", *cmd, input=input, check=check)
    def test(phase):
        result = run("run", "--rm", "-T", "--no-deps", "-e", "ROUTING_PHASE="+phase, "test-runner", "-test.v", "-test.timeout=5m", check=False)
        with (scratch / "acceptance.log").open("a") as log:
            log.write(result.stdout)
        # Assertions never print authentication values; retain full fixture logs privately.
        for line in result.stdout.splitlines():
            if line.startswith(("=== RUN", "--- PASS", "--- FAIL", "PASS", "FAIL", "    observability_test.go:")):
                print(line, flush=True)
        if result.returncode:
            raise RuntimeError(f"{phase} acceptance failed; see {scratch}/compose.log")
    try:
        run("run", "--rm", "-T", "--no-deps", "--workdir", "/etc/prometheus",
            "--entrypoint", "/bin/promtool", "prometheus", "test", "rules", "routing.test.yml")
        print("[routing-e2e] production alert rule tests PASS", flush=True)
        run("up", "-d")
        test("legacy")
        print("[routing-e2e] legacy compatibility PASS; applying fixture backfills", flush=True)
        audit = helper("group-audit", "--driver="+args.driver, "--base-ratios-env=ROUTING_E2E_BASE_RATIOS", "--output=/state/groups.json", check=False)
        if audit.returncode not in (0, 1):
            raise RuntimeError("group audit could not complete; inspect private compose.log")
        helper("group-backfill", "--driver="+args.driver, "--report=/state/groups.json", "--apply")
        gate("channel-service", CHANNEL_ROUTING_GROUP_DUAL_WRITE="true")
        run("up", "-d", "--no-deps", "channel-service")
        run("stop", "identity-service", "admin-api")
        helper("routing-backfill", "--driver="+args.driver, "--apply")
        gate("identity-service", IDENTITY_ROUTING_V2="true")
        gate("billing-service", BILLING_REQUEST_SNAPSHOT_V2="true", SUBSCRIPTION_ENTITLEMENTS_V2="true")
        gate("relay-gateway", RELAY_ROUTING_CONTEXT_V2="true", RELAY_ROUTING_ORDERED="true", SUBSCRIPTION_ENTITLEMENTS_V2="true")
        gate("admin-api", ADMIN_ROUTING_FIXED_KEYS="true", ADMIN_ROUTING_ORDERED_KEYS="true", SUBSCRIPTION_ENTITLEMENTS_V2="true")
        run("up", "-d")
        test("v2")
        preflight = helper("routing-preflight", "--driver="+args.driver, "--stage=f", input=json.dumps(base))
        (scratch / "preflight.json").write_text(preflight.stdout)
        print("[routing-e2e] stage F preflight PASS", flush=True)
        run("stop", "redis")
        test("redis-down")
        run("start", "redis")
        test("redis-recovered")
        gate("billing-service", BILLING_REQUEST_SNAPSHOT_V2="false")
        run("up", "-d", "--no-deps", "billing-service")
        test("missing-capability")
        gate("billing-service", BILLING_REQUEST_SNAPSHOT_V2="true")
        run("up", "-d", "--no-deps", "billing-service")
        gate("admin-api", ADMIN_ROUTING_FIXED_KEYS="false", ADMIN_ROUTING_ORDERED_KEYS="false")
        run("up", "-d", "--no-deps", "admin-api")
        test("creation-rollback")
        print("[routing-e2e] PASS all phases", flush=True)
    finally:
        logs = run("logs", "--no-color", "--tail=100", check=False)
        (scratch / "services.log").write_text(logs.stdout + logs.stderr)
        if args.keep:
            print(f"[routing-e2e] retained; cleanup: docker compose -p {project} -f {config_file} --profile test down -v", flush=True)
        else:
            # Include the runner profile so its private state volume is removed too.
            run("--profile", "test", "down", "-v", "--remove-orphans")
            print("[routing-e2e] removed this project's containers, network and volumes", flush=True)


if __name__ == "__main__":
    main()
