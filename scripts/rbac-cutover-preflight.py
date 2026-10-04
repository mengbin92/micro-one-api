#!/usr/bin/env python3
"""Read live Docker identity wiring without emitting any credentials.

This is a preflight, not evidence that an external write barrier exists. It
never stops services, writes configuration, changes DB grants, or flips mode.
"""
import argparse
import json
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
SERVICES = dict(zip(
    ['admin-api', 'identity-service', 'channel-service', 'billing-service',
     'config-service', 'log-service', 'monitor-worker', 'notify-worker', 'relay-gateway'],
    ['admin', 'identity', 'channel', 'billing', 'config', 'log', 'monitor', 'notify', 'relay']))


def inspect_command():
    # The raw inspect output contains credentials. It stays in the remote
    # Python process; only the derived readiness facts leave that process.
    return '''import subprocess,json,hashlib
names = %r
items=json.loads(subprocess.check_output(['docker','inspect']+names))
envs={c['Name'].lstrip('/'):dict(x.split('=',1) for x in c['Config'].get('Env',[]) if '=' in x) for c in items}
tokens={name:envs[svc].get('SERVICE_IDENTITY_TOKEN','') for svc,name in %r.items()}
result=[]
for c in items:
 svc=c['Name'].lstrip('/'); env=envs[svc]; raw=env.get('SERVICE_CALLER_TOKENS','')
 try: callers=json.loads(raw or '{}'); valid=isinstance(callers,dict)
 except Exception: callers={}; valid=False
 correct=[]; incorrect=[]
 if valid:
  for caller,token in callers.items():
   (correct if caller in tokens and tokens[caller] and token==tokens[caller] else incorrect).append(caller)
 token=env.get('SERVICE_IDENTITY_TOKEN',''); dsn=env.get('SQL_DSN','')
 result.append({'service':svc,'image':c['Image'],'state':c['State']['Status'],'dedicated_outbound':bool(token),'distinct_from_shared':bool(token) and token!=env.get('SERVICE_TOKEN'), 'outbound_fingerprint':hashlib.sha256(token.encode()).hexdigest() if token else None,'caller_map_valid':valid,'correct_callers':correct,'incorrect_callers':incorrect,'sql_username':dsn.split(':',1)[0] if '@tcp(' in dsn else None,'source_digest':(c['Config'].get('Labels') or {}).get('micro-one-api.source.digest','')})
print(json.dumps(result))
''' % (list(SERVICES), SERVICES)


def required_callers():
    raw = (ROOT / 'platform/security/serviceidentity/registry.go').read_text()
    raw += (ROOT / 'platform/security/serviceidentity/identity.go').read_text()
    allowed = {owner: set() for owner in SERVICES.values()}
    for owner, fields in re.findall(r'Owner:\s*"([a-z]+)"([^\n]+)', raw):
        if owner not in allowed:
            continue
        for entries in re.findall(r'(?:UserCallers|SystemCallers):\s*\[\]string\{([^}]*)\}', fields):
            allowed[owner].update(re.findall(r'"([a-z]+)"', entries))
    for owners in allowed.values():
        owners.discard('rescue')  # separate credential, not a system caller
    return allowed


def assess(snapshot, source_digest):
    blockers = []
    found = {row['service']: row for row in snapshot}
    callers = required_callers()
    fingerprints = []
    for service, owner in SERVICES.items():
        row = found.get(service)
        if row is None:
            blockers.append(service + ': missing instance')
            continue
        if row.get('state') != 'running':
            blockers.append(service + ': instance not running')
        if not row.get('dedicated_outbound') or not row.get('distinct_from_shared'):
            blockers.append(service + ': dedicated service identity not provisioned')
        if row.get('outbound_fingerprint'):
            fingerprints.append(row['outbound_fingerprint'])
        if not row.get('caller_map_valid') or row.get('incorrect_callers') or not callers[owner].issubset(row.get('correct_callers', [])):
            blockers.append(service + ': fixed-method receiver trust incomplete')
        if row.get('sql_username') in (None, '', 'root'):
            blockers.append(service + ': least-privilege database channel not verified')
        if row.get('source_digest') != source_digest:
            blockers.append(service + ': IAM capability build digest not verified')
    if len(set(fingerprints)) != len(fingerprints):
        blockers.append('service credentials are shared between callers')
    return {'ready': not blockers, 'source_digest': source_digest, 'instances': snapshot, 'blockers': blockers}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument('--remote', help='explicit SSH host from DEPLOY_REMOTE_SERVER')
    source.add_argument('--snapshot', type=Path, help='previous sanitized snapshot')
    parser.add_argument('--source-digest', required=True, help='reviewed local release source SHA256')
    args = parser.parse_args()
    if not re.fullmatch('[a-f0-9]{64}', args.source_digest):
        parser.error('source digest must be SHA256')
    if args.remote:
        raw = subprocess.check_output(['ssh', '-o', 'ConnectTimeout=10', args.remote, 'python3', '-'], input=inspect_command().encode())
    else:
        raw = args.snapshot.read_bytes()
    report = assess(json.loads(raw), args.source_digest)
    print(json.dumps(report, ensure_ascii=False, indent=2))
    raise SystemExit(0 if report['ready'] else 1)


if __name__ == '__main__':
    main()
