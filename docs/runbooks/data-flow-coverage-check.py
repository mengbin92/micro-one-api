#!/usr/bin/env python3
"""Run on the deployment host: ssh "$DEPLOY_REMOTE_SERVER" python3 - < this-file.

Read-only coverage inventory; no provider/order GETs (which can mutate orders),
no probes, notification sends, Redis claims, or business writes. Requires the
production MySQL schema layout. Failed queries are UNKNOWN, never a zero count.
Only aggregate values and allowlisted configuration/metric labels are emitted.
"""
import datetime
import json
import subprocess
import urllib.parse
import urllib.request


def command(args, **kwargs):
    return subprocess.run(args, capture_output=True, text=True, timeout=30, **kwargs)


captured = datetime.datetime.now(datetime.timezone.utc)
end = captured.replace(microsecond=0) - datetime.timedelta(minutes=10)
start = end - datetime.timedelta(days=1)
window = "r.created_at >= @audit_start AND r.created_at < @audit_end"
queries = {
    "reservation_paths_24h": f"""
        SELECT status, CASE WHEN CAST(subscription_account_id AS UNSIGNED)>0
        THEN 'subscription_account' ELSE 'channel' END AS source,
        COUNT(*) AS requests, SUM(request_id LIKE 'grpc\\_%') AS grpc_requests
        FROM oneapi_billing.billing_reservations r WHERE {window}
        GROUP BY status, source""",
    "subscription_account_writeback_24h": f"""
        SELECT COUNT(*) AS committed, SUM(r.actual_cost>0) AS positive_cost,
        SUM(e.reservation_id IS NULL) AS missing_quota_event,
        SUM(r.actual_cost>0 AND e.reservation_id IS NULL) AS positive_cost_missing_event
        FROM oneapi_billing.billing_reservations r
        LEFT JOIN oneapi_channel.subscription_account_quota_events e
          ON e.reservation_id=r.reservation_id AND e.cost_source='billing_commit'
        WHERE {window} AND r.status='committed'
          AND CAST(r.subscription_account_id AS UNSIGNED)>0""",
    "subscription_account_history": """
        SELECT COUNT(*) AS committed, SUM(r.actual_cost>0) AS positive_cost,
        SUM(e.reservation_id IS NULL) AS missing_quota_event,
        MIN(r.created_at) AS first_request, MAX(r.created_at) AS last_request
        FROM oneapi_billing.billing_reservations r
        LEFT JOIN oneapi_channel.subscription_account_quota_events e
          ON e.reservation_id=r.reservation_id AND e.cost_source='billing_commit'
        WHERE r.status='committed' AND CAST(r.subscription_account_id AS UNSIGNED)>0""",
    "subscription_window_charges_24h": f"""
        SELECT COUNT(*) AS positive_accounting_requests,
          SUM(w.reservation_id IS NULL) AS missing_window_charge,
          SUM(w.subscription_id<>r.subscription_id) AS wrong_subscription,
          SUM(JSON_EXTRACT(r.request_snapshot,'$.Subscription.RateMultiplier') IS NULL) AS missing_frozen_rate,
          SUM(ABS(w.accounting_usd-COALESCE(l.subscription_cost,0)/10000.0 *
            JSON_EXTRACT(r.request_snapshot,'$.Subscription.RateMultiplier'))>0.000001) AS ledger_amount_mismatch
        FROM oneapi_billing.billing_reservations r
        LEFT JOIN oneapi_billing.subscription_window_charges w
          ON w.reservation_id COLLATE utf8mb4_unicode_ci=r.reservation_id COLLATE utf8mb4_unicode_ci
        LEFT JOIN (SELECT reference_id,SUM(subscription_cost) AS subscription_cost
          FROM oneapi_billing.billing_ledgers WHERE type='consume' AND cost_source='subscription'
            AND created_at>=@audit_start GROUP BY reference_id) l ON l.reference_id=r.reservation_id
        WHERE {window} AND r.status='committed' AND r.subscription_accounting_usd>0""",
    "reverse_log_ledger_24h": """
        SELECT COUNT(*) AS consume_logs,
        SUM(NOT EXISTS (SELECT 1 FROM oneapi_billing.billing_reservations r
          WHERE r.request_id=g.request_id AND r.status='committed')) AS without_committed_reservation
        FROM oneapi_log.logs g WHERE level='consume'
          AND created_at>=UNIX_TIMESTAMP(@audit_start) AND created_at<UNIX_TIMESTAMP(@audit_end)""",
    "duplicate_logs_24h": """
        SELECT COUNT(*) AS duplicate_request_groups FROM (
          SELECT user_id,request_id FROM oneapi_log.logs
          WHERE level='consume' AND request_id<>''
            AND created_at>=UNIX_TIMESTAMP(@audit_start) AND created_at<UNIX_TIMESTAMP(@audit_end)
          GROUP BY user_id,request_id HAVING COUNT(*)>1) d""",
    "payment_states": """
        SELECT asset_type,status,asset_issue_status,COUNT(*) AS orders,
          MIN(created_at) AS first_created,MAX(created_at) AS last_created
        FROM oneapi_billing.payment_orders GROUP BY asset_type,status,asset_issue_status""",
    "receivable_states": """
        SELECT status,COUNT(*) AS records,SUM(overdue_quota) AS overdue_quota,
          SUM(settled_quota) AS settled_quota FROM oneapi_billing.account_receivables GROUP BY status""",
    "subscription_states": """
        SELECT status,COUNT(*) AS subscriptions,
          SUM(status='active' AND expires_at<UNIX_TIMESTAMP()-3600) AS overdue_active_over_1h,
          SUM(status='active' AND expires_at BETWEEN UNIX_TIMESTAMP() AND UNIX_TIMESTAMP()+86400) AS expires_within_24h,
          SUM(contract_snapshot IS NOT NULL AND contract_snapshot<>'') AS with_contract
        FROM oneapi_billing.user_subscriptions GROUP BY status""",
    "token_quota_modes": """
        SELECT unlimited_quota,COUNT(*) AS tokens FROM oneapi_identity.tokens GROUP BY unlimited_quota""",
    "notification_states": """
        SELECT type,status,COUNT(*) AS notifications,MAX(created_at) AS last_created_epoch,
          SUM(retry_count>0) AS retried FROM oneapi_notify.notifications GROUP BY type,status""",
    "monitor_inventory": """
        SELECT (SELECT COUNT(*) FROM oneapi_monitor.health_checks) AS health_checks,
          (SELECT MAX(checked_at) FROM oneapi_monitor.health_checks) AS last_check_epoch,
          (SELECT COUNT(*) FROM oneapi_monitor.alert_rules) AS alert_rules,
          (SELECT COUNT(*) FROM oneapi_monitor.alert_rules WHERE enabled=1) AS enabled_rules,
          (SELECT COUNT(*) FROM oneapi_config.configs) AS configs""",
    "model_health": """
        SELECT source_kind,COUNT(*) AS states,SUM(request_count) AS requests,
          MAX(last_checked_at) AS last_check_epoch FROM oneapi_channel.model_health_states GROUP BY source_kind""",
    "model_usage": """
        SELECT MAX(date) AS last_date,COUNT(*) AS rows_count,SUM(request_count) AS requests,
          SUM(error_count) AS errors FROM oneapi_channel.model_usage_stats""",
    "account_inventory": """
        SELECT COUNT(*) AS accounts,SUM(session_window_limit_usd>0) AS session_limited,
          SUM(quota_limit_usd>0 OR quota_daily_limit_usd>0 OR quota_weekly_limit_usd>0 OR quota_5h_limit_usd>0) AS locally_limited,
          SUM(expires_at>0 AND expires_at<UNIX_TIMESTAMP()) AS expired_credentials
        FROM oneapi_channel.subscription_accounts""",
    "quota_snapshots": """
        SELECT COUNT(*) AS snapshots,MAX(updated_at) AS last_updated,
          SUM(snapshot_paused) AS paused FROM oneapi_billing.account_quota_snapshots""",
    "join_key_collations": """
        SELECT table_name,column_name,collation_name FROM information_schema.columns
        WHERE table_schema='oneapi_billing'
          AND table_name IN ('subscription_window_charges','billing_reservations')
          AND column_name='reservation_id'""",
    "auxiliary_counts": """
        SELECT (SELECT COUNT(*) FROM oneapi_billing.subscription_commerce_receipts) AS commerce_receipts,
          (SELECT COUNT(*) FROM oneapi_billing.billing_redeem_records) AS redeem_records,
          (SELECT COUNT(*) FROM oneapi_channel.subscription_account_quota_events) AS account_quota_events,
          (SELECT COUNT(*) FROM oneapi_channel.usage_semantic_source_blocks) AS usage_semantic_blocks,
          (SELECT COUNT(*) FROM oneapi_billing.subscription_routing_entitlements) AS entitlements""",
}

result = {
    "captured_at_utc": captured.isoformat(),
    "window_utc": {"start_inclusive": start.isoformat(), "end_exclusive": end.isoformat()},
    "sql": {},
}
for name, query in queries.items():
    sql = f"""SET SESSION MAX_EXECUTION_TIME=5000;
    SET SESSION time_zone='+00:00';
    SET @audit_start='{start.strftime('%Y-%m-%d %H:%M:%S')}';
    SET @audit_end='{end.strftime('%Y-%m-%d %H:%M:%S')}';
    START TRANSACTION READ ONLY;
    {query}; COMMIT;"""
    try:
        r = command(['docker', 'exec', '-i', 'mysql', 'sh', '-c',
                     'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --batch'], input=sql)
        if r.returncode:
            result['sql'][name] = {'status': 'UNKNOWN', 'exit_code': r.returncode}
            continue
        lines = r.stdout.splitlines()
        columns = lines[0].split('\t')
        result['sql'][name] = {'status': 'OBSERVED', 'rows': [dict(zip(columns, line.split('\t'))) for line in lines[1:]]}
    except (OSError, subprocess.TimeoutExpired) as error:
        result['sql'][name] = {'status': 'UNKNOWN', 'error_type': type(error).__name__}

services = ['relay-gateway', 'billing-service', 'channel-service', 'identity-service',
            'admin-api', 'log-service', 'monitor-worker', 'notify-worker', 'config-service']
flags = ['LOG_LEVEL', 'EVENT_BUS_BACKEND', 'BILLING_ASYNC_ENABLED', 'LOG_BATCH_ENABLED',
         'SUBSCRIPTION_ENTITLEMENTS_V2', 'BILLING_REQUEST_SNAPSHOT_V2', 'RECON_ALERT_ENABLED',
         'CHANNEL_HEALTH_CHECK_ENABLED', 'MONITOR_CHANNEL_HEALTH_CHECK_ENABLED',
         'CHANNEL_HEALTH_ALERT_ENABLED', 'SUBSCRIPTION_QUOTA_ALERT_ENABLED',
         'SUBSCRIPTION_QUOTA_RESET_ENABLED', 'SUBSCRIPTION_ACCOUNT_RECOVERY_ENABLED']
r = command(['docker', 'inspect', *services])
if r.returncode == 0:
    containers = json.loads(r.stdout)
    result['containers'] = {}
    for c in containers:
        env = dict(s.split('=', 1) for s in c['Config']['Env'])
        result['containers'][c['Name'].lstrip('/')] = {
            'image_id': c['Image'], 'running': c['State']['Running'],
            'started_at': c['State']['StartedAt'],
            'flags': {key: env.get(key, '<unset>') for key in flags},
        }
    environments = {c['Name'].lstrip('/'): dict(s.split('=', 1) for s in c['Config']['Env']) for c in containers}
    monitor_env, channel_env = environments['monitor-worker'], environments['channel-service']
    result['monitor_dependency_config'] = {
        'channel_endpoint': monitor_env.get('CHANNEL_GRPC_ENDPOINT', '<unset>'),
        'monitor_service_token_present': bool(monitor_env.get('SERVICE_TOKEN')),
        'channel_service_token_present': bool(channel_env.get('SERVICE_TOKEN')),
        'service_tokens_match': bool(monitor_env.get('SERVICE_TOKEN')) and
            monitor_env.get('SERVICE_TOKEN') == channel_env.get('SERVICE_TOKEN'),
    }
    config_path = monitor_env.get('CONF_PATH', '/configs/config.yaml')
    config = command(['docker', 'exec', 'monitor-worker', 'cat', config_path])
    result['monitor_dependency_config']['channel_endpoint_template'] = next(
        (line.strip() for line in config.stdout.splitlines() if 'CHANNEL_GRPC_ENDPOINT' in line),
        '<not found>' if config.returncode == 0 else '<unavailable>')
else:
    result['containers'] = {'status': 'UNKNOWN', 'exit_code': r.returncode}

messages = [
    'async settlement error', 'failed to record subscription account quota usage',
    'subscription account quota usage rejected', 'consume token quota failed; token temporarily blocked',
    'failed to record channel usage after retries', 'failed to record model usage',
    'failed to ingest usage log after retries', 'batch log writer: flush failed, entries dropped',
    'model health sample dropped: rpc failed', 'model health sample rejected',
    'reconciliation completed', 'reconciliation alert sent',
]
result['log_samples'] = {}
for name in ['relay-gateway', 'billing-service', 'log-service']:
    r = command(['docker', 'logs', '--since', '3h', '--tail', '3000', name])
    counts = dict.fromkeys(messages, 0)
    lines = (r.stdout + r.stderr).splitlines()
    for line in lines:
        pos = line.find('{')
        if pos < 0:
            continue
        try:
            msg = json.loads(line[pos:]).get('msg')
        except ValueError:
            continue
        if msg in counts:
            counts[msg] += 1
    result['log_samples'][name] = {'status': 'OBSERVED' if r.returncode == 0 else 'UNKNOWN',
                                  'sample_lines': len(lines), 'message_counts': counts}

# Prometheus query is read-only; exclude high-cardinality/user-related labels.
metric_query = '{__name__=~"micro_one_api_(log_usage_ingest_total|billing_async_(queue_size|dropped_flushes_total|missing_reservation_id_total|settlement_duration_seconds_count)|monitor_channel_health_.*_total|grpc_requests_total|dependency_grpc_(errors_total|latency_seconds_count))"}'
try:
    # Production does not publish 9090 on the host; use its backend-network IP.
    inspect = command(['docker', 'inspect', 'prometheus'])
    prometheus = json.loads(inspect.stdout)[0]
    networks = prometheus['NetworkSettings']['Networks']
    address = next(n['IPAddress'] for n in networks.values() if n.get('IPAddress'))
    url = 'http://' + address + ':9090/api/v1/query?' + urllib.parse.urlencode({'query': metric_query})
    with urllib.request.urlopen(url, timeout=15) as response:
        payload = json.load(response)
    labels = {'__name__', 'job', 'instance', 'service', 'status', 'mode', 'reason', 'method', 'code'}
    result['metrics'] = {'status': payload.get('status'), 'samples': [
        {'labels': {k: v for k, v in sample['metric'].items() if k in labels}, 'value': sample['value'][1]}
        for sample in payload.get('data', {}).get('result', [])
        if not any(key in sample['metric'].get('__name__', '') for key in ['grpc_requests_total', 'dependency_grpc_'])
        or any(method in sample['metric'].get('method', '') for method in
               ['ChatCompletion', 'RecordModelUsage', 'RecordChannelHealth', 'ListChannels',
                'ConsumeTokenQuota', 'RecordSubscriptionAccountQuotaUsage'])]}
    recent_query = 'increase(micro_one_api_monitor_channel_health_check_runs_total[30m])'
    url = 'http://' + address + ':9090/api/v1/query?' + urllib.parse.urlencode({'query': recent_query})
    with urllib.request.urlopen(url, timeout=15) as response:
        recent = json.load(response)
    result['monitor_runs_30m'] = {'status': recent.get('status'), 'samples': [
        {'labels': {k: v for k, v in sample['metric'].items() if k in labels}, 'value': sample['value'][1]}
        for sample in recent.get('data', {}).get('result', [])]}
except (OSError, ValueError, KeyError, IndexError, StopIteration, subprocess.TimeoutExpired) as error:
    result['metrics'] = {'status': 'UNKNOWN', 'error_type': type(error).__name__}

print(json.dumps(result, ensure_ascii=False, indent=2))
