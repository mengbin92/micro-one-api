#!/usr/bin/env python3
"""Validate service credential wiring without printing credentials or contacting hosts."""
import argparse
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
NAMES = ['admin', 'identity', 'channel', 'billing', 'config', 'log', 'monitor', 'notify', 'relay']
SERVICES = dict(zip(['admin-api', 'identity-service', 'channel-service', 'billing-service', 'config-service', 'log-service', 'monitor-worker', 'notify-worker', 'relay-gateway'], NAMES))


def check_templates():
    for name in ['docker-compose.yml', 'docker-compose.lite.yml', 'docker-compose.postgres.yml']:
        text = (ROOT / 'deployments/docker-compose' / name).read_text()
        blocks = re.split(r'(?m)^  ([a-z][a-z0-9-]+):\s*$', text)
        found = set()
        for service, body in zip(blocks[1::2], blocks[2::2]):
            if service not in SERVICES:
                continue
            prefix = SERVICES[service].upper()
            found.add(service)
            for field in ['SERVICE_IDENTITY_TOKEN', 'SERVICE_CALLER_TOKENS']:
                expected = f'{field}=${{{prefix}_{field}:-}}'
                if body.count(expected) != 1:
                    raise ValueError(f'{name}: {service} must receive its own {field} configuration')
        if found != set(SERVICES):
            raise ValueError(f'{name}: service identity wiring incomplete')


def check_credentials(path):
    values = {}
    for raw in path.read_text().splitlines():
        raw = raw.strip()
        if not raw or raw.startswith('#') or '=' not in raw:
            continue
        key, value = raw.split('=', 1)
        if len(value) >= 2 and value[0] == value[-1] and value[0] in ['"', "'"]:
            value = value[1:-1]
        values[key] = value
    tokens = {name: values.get(name.upper() + '_SERVICE_IDENTITY_TOKEN', '') for name in NAMES}
    if any(not token.strip() for token in tokens.values()) or len(set(tokens.values())) != len(tokens):
        raise ValueError('each service must have a nonempty independent outbound credential')
    if values.get('SERVICE_TOKEN') in tokens.values():
        raise ValueError('dedicated credentials must differ from the legacy shared credential')
    policies = (ROOT / 'platform/security/serviceidentity/registry.go').read_text()
    policies += (ROOT / 'platform/security/serviceidentity/identity.go').read_text()
    allowed = {name: set() for name in NAMES}
    for owner, fields in re.findall(r'Owner:\s*"([a-z]+)"([^\n]+)', policies):
        if owner in allowed:
            for entries in re.findall(r'(?:UserCallers|SystemCallers):\s*\[\]string\{([^}]*)\}', fields):
                allowed[owner].update(re.findall(r'"([a-z]+)"', entries))
    for receiver in NAMES:
        raw = values.get(receiver.upper() + '_SERVICE_CALLER_TOKENS', '') or '{}'
        try:
            callers = json.loads(raw)
        except Exception:
            raise ValueError(f'{receiver}: inbound caller map must be valid JSON') from None
        if not isinstance(callers, dict) or not set(callers).issubset(allowed[receiver]):
            raise ValueError(f'{receiver}: inbound credentials contain a caller with no fixed policy')
        for caller, token in callers.items():
            if caller == 'rescue':
                if not isinstance(token, str) or not token or token in tokens.values() or token == values.get('SERVICE_TOKEN'):
                    raise ValueError('identity: rescue credential must be independent')
            elif token != tokens.get(caller):
                raise ValueError(f'{receiver}: inbound credential does not match {caller} outbound credential')
        # An omitted caller prevents its actual fixed methods from working.
        required = allowed[receiver] - {'rescue'}
        if required - set(callers):
            raise ValueError(f'{receiver}: inbound credentials omit required fixed-policy callers')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--env', type=Path, help='validate explicitly supplied provisioned credential file')
    args = parser.parse_args()
    try:
        check_templates()
        if args.env:
            check_credentials(args.env)
    except ValueError as error:
        parser.exit(1, f'service identity configuration invalid: {error}\n')
    print('service identity templates valid' + ('; credential independence and receiver trust maps valid' if args.env else '; provisioned credentials not inspected'))

if __name__ == '__main__':
    main()
