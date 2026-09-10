#!/usr/bin/env python3
"""Fresh SQLite Compose smoke; only writes to its own temporary project.

Uses the current contents of tracked files, no ignored .env, generated stubs or
existing volumes. Only the upstream is mocked; all user actions use public HTTP.
Requires Python 3, git and Docker Compose. --keep retains the isolated demo.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import shutil
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def request(url, payload=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(url, headers=headers,
                                 data=None if payload is None else json.dumps(payload).encode())
    with urllib.request.urlopen(req, timeout=15) as response:
        data = json.load(response)
    require(data.get("success", True), f"API rejected {req.get_method()} {req.full_url}")
    return data


def eventually(check, label):
    deadline = time.monotonic() + 90
    while True:
        try:
            result = check()
            require(result, label)
            return result
        except (OSError, ValueError, RuntimeError, subprocess.CalledProcessError):
            if time.monotonic() >= deadline:
                raise RuntimeError(f"Timed out: {label}") from None
            time.sleep(2)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--keep", action="store_true", help="keep this isolated project for manual UI verification")
    args = parser.parse_args()
    root = Path(__file__).resolve().parents[1]
    scratch = Path(tempfile.mkdtemp(prefix="micro-one-api-lite-"))
    project = "lite-smoke-" + secrets.token_hex(4)
    repo = scratch / "repo"
    # Copy tracked sources with local edits, without host credentials/build artifacts.
    names = subprocess.check_output(["git", "ls-files", "-z"], cwd=root).decode().split("\0")
    for name in filter(None, names):
        source, dest = root / name, repo / name
        if source.is_file():
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source, dest)
    compose_dir = repo / "deployments/docker-compose"
    env_path = compose_dir / ".env"
    settings = dict(line.split("=", 1) for line in (compose_dir / ".env.lite.example").read_text().splitlines()
                    if line and not line.startswith("#"))
    for key in ("JWT_SECRET_KEY", "SERVICE_TOKEN", "ADMIN_TOKEN", "REDIS_PASSWORD"):
        settings[key] = secrets.token_hex(32)
    settings.update(CHANNEL_ENCRYPTION_KEY=secrets.token_hex(16), LITE_ADMIN_PORT="0", LITE_RELAY_PORT="0",
                    INITIAL_ADMIN_PASSWORD="", ALIPAY_CERT_DIR=str(scratch / "cert"))
    (scratch / "cert").mkdir()
    env_path.write_text("".join(f"{key}={value}\n" for key, value in settings.items()))
    env_path.chmod(0o600)
    # Do not inherit deployment/Compose variables from the caller's shell.
    child_env = {key: value for key, value in os.environ.items()
                 if key not in settings and not key.startswith(("COMPOSE_", "BILLING_CANONICAL_", "RELAY_"))}
    child_env["COMPOSE_PARALLEL_LIMIT"] = "1"
    override = compose_dir / "smoke.json"
    override.write_text(json.dumps({"services": {
        "mock-upstream": {"image": "python:3-alpine", "command": ["python3", "/mock/mock_server.py"],
                          "volumes": ["../../test/mockupstream/mock_server.py:/mock/mock_server.py:ro"],
                          "networks": ["backend"]},
        # A Docker-local mock needs private-network access. This is test-only.
        "relay-gateway": {"environment": {"PROVIDER_DISABLE_SSRF_CHECK": "true"}},
        "channel-service": {"environment": {"PROVIDER_DISABLE_SSRF_CHECK": "true"}},
    }}))
    compose = ["docker", "compose", "--project-name", project, "--env-file", str(env_path),
               "-f", str(compose_dir / "docker-compose.lite.yml"), "-f", str(override)]
    log_path = scratch / "compose.log"

    def run(*args):
        return subprocess.check_output(compose + list(args), cwd=compose_dir, env=child_env, stderr=subprocess.STDOUT).decode().strip()

    def logged(*args):
        with log_path.open("a") as log:
            subprocess.run(compose + list(args), cwd=compose_dir, env=child_env,
                           stdout=log, stderr=subprocess.STDOUT, check=True)

    try:
        print(f"[lite] Fresh project: {project}; private build log: {log_path}", flush=True)
        run("config", "--quiet")
        print("[lite] Building Go services one at a time from clean sources (first build may take several minutes)", flush=True)
        # Serial builds keep memory within the documented 4 CPU / 8 GiB budget;
        # image-only services (redis, sqlite-init, mock-upstream) are pulled at up.
        for service in run("config", "--services").splitlines():
            if service in ("redis", "sqlite-init", "mock-upstream"):
                continue
            logged("build", service)
        print("[lite] Starting fresh SQLite/Redis volumes", flush=True)
        logged("up", "-d")
        for service in ("sqlite-init", "migrate"):
            container = run("ps", "--all", "-q", service)
            require(subprocess.check_output(["docker", "inspect", "--format", "{{.State.ExitCode}}", container]).strip() == b"0",
                    f"{service} must exit successfully")
        admin = "http://" + run("port", "admin-api", "8000")
        relay = "http://" + run("port", "relay-gateway", "8080")
        eventually(lambda: request(relay + "/healthz"), "relay health")
        with urllib.request.urlopen(admin + "/", timeout=15) as response:
            require(b'<div id="root"' in response.read(), "bundled admin frontend")
        password = eventually(lambda: run("exec", "-T", "identity-service", "cat", "/data/initial-admin-password.txt"), "generated password file")
        require(run("exec", "-T", "identity-service", "stat", "-c", "%a", "/data/initial-admin-password.txt") == "600", "password file mode")
        login = eventually(lambda: request(admin + "/api/user/login", {"username": "admin", "password": password}), "admin login")
        session = login["data"] if isinstance(login["data"], str) else login["data"]["token"]
        user = request(admin + "/api/user/self", token=session)["data"]
        print("[lite] PASS migration, frontend, private bootstrap password and login", flush=True)
        request(admin + "/api/channel", {"name": "lite-demo", "type": 1, "base_url": "http://mock-upstream:9999",
                "key": "sk-mock-key", "models": "gpt-3.5-turbo", "group": "default", "priority": 1, "weight": 1}, session)
        request(admin + "/v1/topup", {"user_id": str(user["id"]), "amount": 500000, "remark": "isolated lite smoke"}, session)
        token = request(admin + "/api/token", {"name": "lite-smoke", "models": ["gpt-3.5-turbo"], "unlimited_quota": True}, session)["data"]["key"]
        models = request(relay + "/v1/models", token=token)
        require(any(model["id"] == "gpt-3.5-turbo" for model in models["data"]), "configured model must be visible")
        chat = request(relay + "/v1/chat/completions", {"model": "gpt-3.5-turbo", "messages": [{"role": "user", "content": "Hello lite"}], "max_tokens": 16, "stream": False}, token)
        require(chat["choices"][0]["message"]["content"] == "Mock response for: Hello lite", "chat response")
        require(chat["usage"]["total_tokens"] > 0, "upstream usage")
        print("[lite] PASS create channel, wallet credit, create Token, models and chat completion", flush=True)
        # Restart without deleting volumes, re-run migrations and use the same API Token.
        logged("stop")
        logged("run", "--rm", "migrate")
        logged("up", "-d")
        admin = "http://" + run("port", "admin-api", "8000")
        relay = "http://" + run("port", "relay-gateway", "8080")
        eventually(lambda: request(relay + "/v1/models", token=token).get("data"), "persisted Token and channel after restart")
        require(run("exec", "-T", "identity-service", "cat", "/data/initial-admin-password.txt") == password, "bootstrap must not reset on restart")
        print("[lite] PASS repeat migration and persistence after restart", flush=True)
        print(f"[lite] PASS complete; admin={admin}, relay={relay}", flush=True)
    except Exception:
        # Keep diagnostics private; service logs can contain test credentials.
        with (scratch / "services.log").open("w") as log:
            subprocess.run(compose + ["logs", "--no-color", "--tail", "150"], env=child_env, stdout=log, stderr=subprocess.STDOUT)
        print(f"[lite] FAIL; private diagnostics: {scratch}", flush=True)
        raise
    finally:
        if args.keep:
            print(f"[lite] Retained for inspection. Cleanup: docker compose -p {project} --env-file {env_path} -f {compose_dir}/docker-compose.lite.yml -f {override} down -v --rmi local", flush=True)
        else:
            logged("down", "-v", "--rmi", "local")
            print("[lite] Removed only this smoke project's containers, images, network and volumes", flush=True)


if __name__ == "__main__":
    main()
