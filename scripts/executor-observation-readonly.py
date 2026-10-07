#!/usr/bin/env python3
"""Collect executor evidence over SSH without requests, writes, or restarts.

Only DEPLOY_REMOTE_SERVER is read from the local .env. Docker credentials stay
inside the remote process; SQL returns aggregates from a read-only snapshot.
This report intentionally does not issue an automatic seven-day PASS.
"""
import argparse
import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def collect():
    # Executed on the production host using only the Python standard library.
    import datetime
    import math
    import time
    import urllib.parse
    import urllib.request

    zone = datetime.timezone(datetime.timedelta(hours=8))

    def stamp(value):
        return datetime.datetime.fromtimestamp(value, zone).isoformat()

    def inspect(name):
        return json.loads(subprocess.check_output(['docker', 'inspect', name]))[0]

    def env(container):
        return dict(x.split('=', 1) for x in container['Config'].get('Env', []) if '=' in x)

    services = ['relay-gateway', 'admin-api', 'identity-service', 'channel-service',
                'billing-service', 'config-service', 'log-service', 'monitor-worker', 'notify-worker']
    containers = {name: inspect(name) for name in services}
    relay = containers['relay-gateway']
    before = {name: c['Id'] for name, c in containers.items()}
    relay_env = env(relay)
    started = datetime.datetime.fromisoformat(relay['State']['StartedAt'][:26] + '+00:00').timestamp()
    # End all queries at the same closed boundary, two minutes before collection.
    end = int(time.time()) - 120
    duration = max(1, int(end - started))
    prom = inspect('prometheus')
    prom_ip = next(iter(prom['NetworkSettings']['Networks'].values()))['IPAddress']
    base = 'http://' + prom_ip + ':9090'

    def get(path):
        with urllib.request.urlopen(base + path, timeout=45) as response:
            data = json.load(response)
        if data.get('status') != 'success':
            raise RuntimeError('Prometheus query failed')
        if data.get('warnings'):
            raise RuntimeError('Prometheus query returned warnings; evidence incomplete')
        return data['data']

    def query(expression):
        return get('/api/v1/query?' + urllib.parse.urlencode({'query': expression, 'time': end}))['result']

    target = next(t for t in get('/api/v1/targets')['activeTargets']
                  if t['labels'].get('instance') == 'relay-gateway:8080')
    interval = float(target['scrapeInterval'].removesuffix('s'))
    selector = '{instance="relay-gateway:8080"}'
    up = query('up' + selector + '[7d]')
    if len(up) != 1:
        raise RuntimeError('Expected exactly one Relay scrape series')

    def continuity(start):
        values = [(float(t), float(v)) for t, v in up[0]['values'] if float(t) >= start]
        if not values:
            raise RuntimeError('No Relay scrape samples in window')
        gaps = [(a[0], b[0], b[0] - a[0]) for a, b in zip(values, values[1:])]
        # One second tolerates timestamp jitter. Keep actual maximum as well.
        missing = [g for g in gaps if g[2] > interval + 1]
        return dict(start=stamp(start), end=stamp(end), samples=len(values),
                    first=stamp(values[0][0]), last=stamp(values[-1][0]),
                    failed_samples=sum(v != 1 for _, v in values),
                    failed_sample_times=[stamp(t) for t, v in values if v != 1],
                    maximum_gap_seconds=max((g[2] for g in gaps), default=None),
                    gaps_over_interval_plus_one_second=[dict(start=stamp(a), end=stamp(b), seconds=d)
                                                       for a, b, d in missing],
                    boundary_covered=values[0][0] - start <= interval + 1 and end - values[-1][0] <= interval + 1,
                    expected_samples_approx=math.floor((end - start) / interval))

    queries = {}
    metric = 'micro_one_api_relay_executor_'
    for label, window in [('24h', '24h'), ('7d_historical_only', '7d'), ('current_process', str(duration) + 's')]:
        expressions = {
            'requests': 'sum by(endpoint,stream,execution_path,result,status)(increase(' + metric + 'requests_total' + selector + '[' + window + ']))',
            'p95_seconds': 'histogram_quantile(0.95,sum by(le,endpoint,stream,execution_path)(rate(' + metric + 'request_duration_seconds_bucket' + selector + '[' + window + '])))',
            'quota': 'sum by(endpoint,stream,execution_path,outcome)(increase(' + metric + 'quota_outcome_total' + selector + '[' + window + ']))',
            'failover': 'sum by(endpoint,stream,execution_path,result,reason)(increase(' + metric + 'failover_total' + selector + '[' + window + ']))',
            'counter_resets': 'sum(resets(' + metric + 'requests_total' + selector + '[' + window + ']))',
        }
        queries[label] = {name: dict(promql=q, result=query(q)) for name, q in expressions.items()}
    for name in ['requests_total', 'quota_outcome_total', 'failover_total']:
        q = metric + name + selector
        queries[name + '_snapshot'] = dict(promql=q, result=query(q))
    starts = query('process_start_time_seconds' + selector + '[7d]')
    if len(starts) != 1:
        raise RuntimeError('Expected exactly one Relay process-start series')
    process_segments = []
    for ts, value in starts[0]['values']:
        if not process_segments or value != process_segments[-1]['value']:
            process_segments.append(dict(value=value, process_start=stamp(float(value)), first_scraped=stamp(float(ts))))
        process_segments[-1]['last_scraped'] = stamp(float(ts))

    mysql_env = env(inspect('mysql'))
    start_sql = datetime.datetime.fromtimestamp(end - 86400, datetime.timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
    end_sql = datetime.datetime.fromtimestamp(end, datetime.timezone.utc).strftime('%Y-%m-%d %H:%M:%S')
    sql = """SET SESSION time_zone='+00:00';
SET SESSION MAX_EXECUTION_TIME=15000;
START TRANSACTION WITH CONSISTENT SNAPSHOT, READ ONLY;
SELECT 'consume_summary',COUNT(*),COUNT(DISTINCT ledger_dedupe_key),
 COALESCE(SUM(ledger_dedupe_key=''),0),COALESCE(SUM(canonical_present<>1 OR usage_parse_status<>'verified'),0),
 COALESCE(SUM(source_kind='' OR upstream_model_id=''),0)
FROM oneapi_billing.billing_ledgers WHERE type='consume' AND created_at>='%s' AND created_at<'%s';
SELECT 'consume_group',endpoint,is_stream,source_kind,COUNT(*),COUNT(DISTINCT model_name),
 COUNT(DISTINCT CONCAT(source_kind,':',channel_id,':',subscription_account_id,':',upstream_model_id))
FROM oneapi_billing.billing_ledgers WHERE type='consume' AND created_at>='%s' AND created_at<'%s'
GROUP BY endpoint,is_stream,source_kind;
SELECT 'ledger_links',COUNT(*),COALESCE(SUM(r.id IS NULL),0),COALESCE(SUM(r.status<>'committed'),0),
 COALESCE(SUM(g.id IS NULL),0),COALESCE(SUM(g.quota<>l.quota OR g.is_stream<>l.is_stream OR g.model_name<>l.model_name
 OR g.source_kind<>l.source_kind OR g.upstream_model_id<>l.upstream_model_id
 OR g.channel_id<>l.channel_id OR g.subscription_account_id<>l.subscription_account_id),0),
 COALESCE(SUM(r.actual_cost<>-l.amount),0)
FROM oneapi_billing.billing_ledgers l
LEFT JOIN oneapi_billing.billing_reservations r ON r.reservation_id=l.reference_id
LEFT JOIN oneapi_log.logs g ON g.reservation_id=l.reference_id AND g.level='consume'
WHERE l.type='consume' AND l.created_at>='%s' AND l.created_at<'%s';
SELECT 'reservations',status,COUNT(*) FROM oneapi_billing.billing_reservations
WHERE created_at>='%s' AND created_at<'%s' GROUP BY status;
SELECT 'expired_reserved',COUNT(*) FROM oneapi_billing.billing_reservations
WHERE status='reserved' AND expired_at<'%s';
SELECT 'released_with_consume',COUNT(*) FROM oneapi_billing.billing_reservations r
JOIN oneapi_billing.billing_ledgers l ON l.reference_id=r.reservation_id AND l.type='consume'
WHERE r.status='released' AND r.created_at>='%s' AND r.created_at<'%s';
SELECT 'reconciliation',id,run_at,status,discrepancy_count FROM oneapi_billing.reconciliation_runs
WHERE run_at>=%d ORDER BY id;
COMMIT;
""" % (start_sql, end_sql, start_sql, end_sql, start_sql, end_sql, start_sql, end_sql,
           end_sql, start_sql, end_sql,
           int(datetime.datetime.fromisoformat(containers['billing-service']['State']['StartedAt'][:26] + '+00:00').timestamp()))
    sql_result = subprocess.run(['docker', 'exec', '-i', '-e', 'MYSQL_PWD=' + mysql_env['MYSQL_ROOT_PASSWORD'],
                                 'mysql', 'mysql', '-uroot', '-N', '-B'], input=sql, text=True, capture_output=True, timeout=60)
    if sql_result.returncode:
        # Driver output can contain SQL or credentials; do not export it.
        raise RuntimeError('Read-only SQL collection failed (exit %d)' % sql_result.returncode)
    health_ip = next(iter(relay['NetworkSettings']['Networks'].values()))['IPAddress']
    with urllib.request.urlopen('http://' + health_ip + ':8080/healthz', timeout=10) as response:
        health = response.status
    after = {name: inspect(name)['Id'] for name in services}
    if before != after:
        raise RuntimeError('Service instances changed during collection; recollect evidence')
    report = dict(checked_at=stamp(time.time()), query_end=stamp(end),
                  windows=dict(last_24h_start=stamp(end - 86400), historical_7d_start=stamp(end - 604800)),
                  runtime=[dict(service=name, container_id=c['Id'], image_id=c['Image'], image_ref=c['Config']['Image'],
                                source_digest=c['Config'].get('Labels', {}).get('micro-one-api.source.digest'),
                                started_at=c['State']['StartedAt'], running=c['State']['Running'],
                                restart_count=c['RestartCount'], oom_killed=c['State']['OOMKilled'])
                           for name, c in containers.items()],
                  relay=dict(flag=relay_env.get('RELAY_ORCHESTRATOR_ENABLED'),
                             allowlist_count=len([s for s in relay_env.get('RELAY_ORCHESTRATOR_TOKEN_HMAC_SHA256', '').split(',') if s.strip()]),
                             health=health, target_health=target['health'], target_last_error=target['lastError'],
                             scrape_interval_seconds=interval, current_process_age_hours=(end-started)/3600),
                  scrape_continuity={name: continuity(start) for name, start in
                                     [('24h', end-86400), ('7d_historical_only', end-604800), ('current_process', started)]},
                  process_segments=process_segments, queries=queries,
                  accounting=dict(transaction='read-only consistent snapshot; UTC',
                                  start=stamp(end-86400), end=stamp(end), aggregate_rows=sql_result.stdout.splitlines(),
                                  columns=dict(consume_summary=['count', 'distinct_dedupe', 'empty_dedupe', 'unverified_canonical', 'missing_source_or_upstream_model'],
                                               consume_group=['endpoint', 'stream', 'source_kind', 'count', 'distinct_models', 'distinct_sources'],
                                               ledger_links=['joined_rows', 'missing_reservation', 'non_committed_reservation', 'missing_consume_log', 'log_attribution_mismatch', 'actual_cost_mismatch'],
                                               reservations=['status', 'count'], expired_reserved=['count'],
                                               released_with_consume=['count'],
                                               reconciliation=['id', 'run_at_unix', 'status', 'discrepancy_count'])),
                  service_container_ids_preserved=True,
                  limitations=['Historical 7d includes deployments and is not an admission window.',
                               'Prometheus increase is extrapolated; first nonzero samples may not count as increases.',
                               'Ledger lacks durable execution_path; token_name is not a unique caller identity.',
                               'Success/settlement aggregates do not prove natural traffic or matching payload/output length.',
                               'WebSocket legacy samples are not SSE executor comparison samples.'])
    print(json.dumps(report, ensure_ascii=False, indent=2))


def main():
    import inspect

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--server', help='SSH destination; defaults to DEPLOY_REMOTE_SERVER in .env')
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    server = args.server
    if not server:
        for line in (ROOT / '.env').read_text().splitlines():
            if '=' in line and not line.lstrip().startswith('#'):
                key, value = line.split('=', 1)
                if key.strip() == 'DEPLOY_REMOTE_SERVER':
                    server = value.strip().strip('\"\'')
    if not server or server.startswith('-'):
        parser.error('valid --server or DEPLOY_REMOTE_SERVER required')
    source = 'import json, subprocess\n' + inspect.getsource(collect) + '\ncollect()\n'
    result = subprocess.run(['ssh', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=10', server, 'python3 -'],
                            input=source, text=True, capture_output=True)
    if result.returncode:
        print('Executor evidence collection failed; no output written.', file=sys.stderr)
        return 1
    report = json.loads(result.stdout)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps(dict(output=str(args.output), checked_at=report['checked_at'], relay=report['relay']), ensure_ascii=False))
    return 0


if __name__ == '__main__':
    raise SystemExit(main())
