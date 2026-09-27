#!/usr/bin/env python3
"""Create the same isolated relay fixture for old and current Compose stacks."""

import json
import os
import sys
import time
import urllib.error
import urllib.request
from pathlib import Path


ADMIN = "http://127.0.0.1:3000"
RELAY = "http://127.0.0.1:8080"


def request(base, path, payload=None, token=None):
    data = json.dumps(payload).encode() if payload is not None else None
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    req = urllib.request.Request(base + path, data=data, headers=headers)
    with urllib.request.urlopen(req, timeout=15) as response:
        body = response.read()
        if response.status != 200:
            raise RuntimeError(f"{path}: HTTP {response.status}")
        return json.loads(body)


def success(path, payload, token=None):
    last_error = None
    for _ in range(15):
        try:
            result = request(ADMIN, path, payload, token)
            if result.get("success") is True:
                return result
            last_error = result.get("message", "unknown error")
        except (OSError, ValueError) as exc:
            last_error = str(exc)
        time.sleep(2)
    raise RuntimeError(f"{path} failed after readiness retries: {last_error}")


def wait_ready():
    for _ in range(90):
        try:
            request(RELAY, "/healthz")
            request(ADMIN, "/healthz")
            return
        except (OSError, ValueError):
            time.sleep(2)
    raise RuntimeError("admin or relay did not become ready in 180 seconds")


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: q3-fixture.py OUTPUT_ENV_FILE")
    wait_ready()
    password = os.environ["Q3_USER_PASSWORD"]
    registration = success("/api/user/register", {
        "username": "q3bench", "password": password,
        "email": "q3bench@example.invalid", "group": "default",
    })
    user_id = registration["data"]["user_id"]
    login = success("/api/user/login", {"username": "q3bench", "password": password})
    session = login["data"]["token"]
    admin_token = os.environ["Q3_ADMIN_TOKEN"]
    success("/v1/channels", {
        "name": "q3-mock", "type": 1,
        "base_url": "http://mock-upstream:18099",
        "key": "sk-q3-mock", "models": "gpt-3.5-turbo",
        "group": "default", "priority": 1, "weight": 1,
    }, admin_token)
    success("/v1/topup", {
        "user_id": str(user_id), "amount": 50000000,
        "remark": "isolated-q3-benchmark",
    }, admin_token)
    api_token = success("/api/token", {
        "name": "q3-benchmark", "models": ["gpt-3.5-turbo"],
        "unlimited_quota": True,
    }, session)["data"]["key"]
    # Keep credentials out of action logs and artifact uploads.
    output = Path(sys.argv[1])
    output.write_text(f"API_KEY={api_token}\n")
    output.chmod(0o600)
    probe = request(RELAY, "/v1/models", token=api_token)
    if not probe.get("data"):
        raise RuntimeError("relay models preflight returned an empty list")
    chat = request(RELAY, "/v1/chat/completions", {
        "model": "gpt-3.5-turbo", "messages": [{"role": "user", "content": "Hello!"}],
        "max_tokens": 10, "stream": False,
    }, api_token)
    if chat.get("object") != "chat.completion":
        raise RuntimeError("relay chat preflight did not return a completion")
    print(f"Fixture ready: user_id={user_id}, model=gpt-3.5-turbo, mock_delay=2ms")


if __name__ == "__main__":
    try:
        main()
    except (KeyError, OSError, ValueError, RuntimeError) as exc:
        print(f"fixture error: {exc}", file=sys.stderr)
        raise SystemExit(1)
