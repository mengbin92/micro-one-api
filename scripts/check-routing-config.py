#!/usr/bin/env python3
"""Render supported Compose templates and check routing gates without starting services."""
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[1]
COMPOSE = ROOT / "deployments/docker-compose"
GATES = {
    "channel-service": {"CHANNEL_ROUTING_GROUP_DUAL_WRITE": "false"},
    "identity-service": {"IDENTITY_ROUTING_V2": "false", "IDENTITY_DEFAULT_ROUTING_GROUP": "default"},
    "billing-service": {"BILLING_REQUEST_SNAPSHOT_V2": "false", "SUBSCRIPTION_ENTITLEMENTS_V2": "false"},
    "relay-gateway": {"RELAY_ROUTING_CONTEXT_V2": "false", "RELAY_ROUTING_ORDERED": "false", "SUBSCRIPTION_ENTITLEMENTS_V2": "false"},
    "admin-api": {"ADMIN_ROUTING_FIXED_KEYS": "false", "ADMIN_ROUTING_ORDERED_KEYS": "false", "SUBSCRIPTION_ENTITLEMENTS_V2": "false"},
}


def main():
    keys = {key for gates in GATES.values() for key in gates}
    for variant, example in (("", ".env.example"), (".lite", ".env.lite.example"), (".postgres", ".env.postgres.example")):
        example_keys = {line.split("=", 1)[0] for line in (COMPOSE / example).read_text().splitlines()
                        if line and not line.startswith("#") and "=" in line}
        # Never read the deployment .env or let a caller's feature gates mask a missing default.
        base = {key: value for key, value in os.environ.items()
                if key not in keys | example_keys and not key.startswith("COMPOSE_")}
        base["CHANNEL_ENCRYPTION_KEY"] = "0123456789abcdef0123456789abcdef"
        for enabled in (False, True):
            env = dict(base)
            if enabled:
                env.update({key: "routing-config-test" if key == "IDENTITY_DEFAULT_ROUTING_GROUP" else "true" for key in keys})
            result = subprocess.run(["docker", "compose", "--env-file", str(COMPOSE / example),
                                     "-f", str(COMPOSE / f"docker-compose{variant}.yml"), "config", "--format", "json"],
                                    env=env, capture_output=True, text=True, check=True)
            services = json.loads(result.stdout)["services"]
            for service, config in services.items():
                values = config.get("environment", {})
                for key in keys:
                    if key not in GATES.get(service, {}):
                        assert key not in values, f"{variant or 'mysql'}: {service} unexpectedly receives {key}"
                    else:
                        expected = env[key] if enabled else GATES[service][key]
                        assert values.get(key) == expected, f"{variant or 'mysql'}: {service} {key}: expected {expected}, got {values.get(key)!r}"
            assert GATES.keys() <= services.keys(), "required routing service missing"
            for service in ("identity-service", "billing-service", "admin-api", "relay-gateway"):
                assert services[service]["environment"].get("CHANNEL_GRPC_ENDPOINT") == "channel-service:9002", f"{variant or 'mysql'}: {service} missing routing authority endpoint"
            relay = services["relay-gateway"]
            assert relay["environment"].get("DATABASE_DSN"), f"{variant or 'mysql'}: relay missing subscription storage"
            if variant == ".lite":
                assert any(v.get("source") == "sqlite_data" and v.get("target") == "/data" for v in relay.get("volumes", [])), "lite: relay missing shared subscription database volume"
        print(f"routing config: {variant.lstrip('.') or 'mysql'} defaults and explicit gates PASS")


if __name__ == "__main__":
    main()
