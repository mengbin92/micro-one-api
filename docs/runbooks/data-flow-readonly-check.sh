#!/usr/bin/env bash
# Run on the deployment host via ssh ... bash -s < this-file.
set -euo pipefail

python3 - <<'PY'
import datetime
import json
import subprocess
import sys
import urllib.error
import urllib.request


def command(args, **kwargs):
    return subprocess.run(args, text=True, capture_output=True, timeout=30, check=True, **kwargs)


sql = """
SET SESSION MAX_EXECUTION_TIME=5000;
START TRANSACTION READ ONLY;
SELECT COUNT(*) FROM oneapi_billing.reconciliation_runs;
SELECT COUNT(*) FROM oneapi_notify.notifications WHERE created_at>=UNIX_TIMESTAMP()-10800;
SELECT COUNT(*) FROM oneapi_billing.billing_reservations
WHERE status IN ('reserved','committing','releasing') AND updated_at<UTC_TIMESTAMP()-INTERVAL 10 MINUTE;
COMMIT;
"""
db = command(['docker', 'exec', '-i', 'mysql', 'sh', '-c',
              'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --batch --skip-column-names'], input=sql)
history, notifications, stale = map(int, db.stdout.split())

logs = command(['docker', 'logs', '--since', '3h', '--tail', '3000', 'billing-service'])
completed = 0
sent = 0
for line in (logs.stdout + logs.stderr).splitlines():
    start = line.find('{')
    if start < 0:
        continue
    try:
        record = json.loads(line[start:])
    except ValueError:
        continue
    completed += record.get('msg') == 'reconciliation completed'
    sent += record.get('msg') == 'reconciliation alert sent'

containers = json.loads(command(['docker', 'inspect', 'admin-api', 'billing-service', 'relay-gateway']).stdout)
containers = {c['Name'].lstrip('/'): c for c in containers}
billing_env = dict(item.split('=', 1) for item in containers['billing-service']['Config']['Env'])
admin_env = dict(item.split('=', 1) for item in containers['admin-api']['Config']['Env'])
result = {
    'captured_at_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'completed_logs_3h': completed,
    'reconciliation_history_rows': history,
    'alert_sent_logs_3h': sent,
    'notification_rows_3h': notifications,
    'recon_alert_enabled_env': billing_env.get('RECON_ALERT_ENABLED', '<unset>'),
    'stale_reservations_over_10m': stale,
    'image_ids': {name: c['Image'] for name, c in containers.items()},
}

token = admin_env.get('ADMIN_TOKEN')
if token:
    request = urllib.request.Request('http://127.0.0.1:3000/api/reconciliation?page=1&page_size=5',
                                     headers={'Authorization': 'Bearer ' + token})
    try:
        with urllib.request.urlopen(request, timeout=15) as response:
            payload = json.load(response)
            data = payload.get('data') or {}
            result['admin_history_read'] = {
                'http_status': response.status,
                'success': payload.get('success'),
                'total': data.get('total'),
                'returned_rows': len(data.get('runs') or []),
            }
    except urllib.error.HTTPError as error:
        result['admin_history_read'] = {'http_status': error.code}
    except (OSError, ValueError) as error:
        result['admin_history_read'] = {'error_type': type(error).__name__}
else:
    result['admin_history_read'] = 'unavailable: no ADMIN_TOKEN configured'

# This detects the observed complete-but-never-persisted failure. Nonzero
# history alone does not prove that every later run was persisted.
missing_history = completed > 0 and history == 0
result['history_check'] = 'FAIL' if missing_history else ('NO_SAMPLE' if not completed else 'NO_EMPTY_HISTORY_FAILURE')
result['alert_log_conflicts_with_disabled_setting'] = (
    billing_env.get('RECON_ALERT_ENABLED', '').lower() == 'false' and sent > 0
)
print(json.dumps(result, ensure_ascii=False, indent=2))
sys.exit(1 if missing_history else 0)
PY
