#!/usr/bin/env python3
"""Create a private credential overlay for review; never modifies a live env."""
import importlib.util
import json
import os
from pathlib import Path
import secrets
import argparse

SPEC = importlib.util.spec_from_file_location('preflight', Path(__file__).with_name('rbac-cutover-preflight.py'))
PREFLIGHT = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PREFLIGHT)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, required=True, help='new private file, refuses overwrite')
    args = parser.parse_args()
    tokens = {name: secrets.token_urlsafe(48) for name in PREFLIGHT.SERVICES.values()}
    rescue = secrets.token_urlsafe(48)
    values = {}
    for name in PREFLIGHT.SERVICES.values():
        allowed = PREFLIGHT.required_callers()[name]
        values[name.upper() + '_SERVICE_IDENTITY_TOKEN'] = tokens[name]
        callers = {caller: tokens[caller] for caller in sorted(allowed)}
        values[name.upper() + '_SERVICE_CALLER_TOKENS'] = json.dumps(callers, separators=(',', ':'))
    values['IAM_RESCUE_SERVICE_TOKEN'] = rescue
    payload = '# D1 reviewed service identity overlay; apply only after approved provisioning.\n'
    for key, value in values.items():
        payload += key + '=' + value + '\n'
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as file:
        file.write(payload)
    print('Private service identity overlay created; values are not printed. Deploy and verify receiver maps together.')


if __name__ == '__main__':
    main()
