#!/usr/bin/env python3
"""Read-only follow-up for Desktop/data claims: SSH this script to the host.

Only allowlisted flags, aggregate SQL and notification GET response summaries
are emitted. No bodies, recipients, authentication values or business writes.
"""
import datetime
import json
import subprocess
import urllib.error
import urllib.request


def command(args, **kwargs):
    return subprocess.run(args, text=True, capture_output=True, timeout=30, **kwargs)


captured = datetime.datetime.now(datetime.timezone.utc)
end = captured.replace(microsecond=0) - datetime.timedelta(minutes=10)
start = end - datetime.timedelta(days=1)
result = {
    "captured_at_utc": captured.isoformat(),
    "window_start_utc": start.isoformat(),
    "window_end_utc": end.isoformat(),
}
queries = {
    "consume_source_24h": """
        SELECT COUNT(*) AS ledger_rows,COUNT(DISTINCT reference_id) AS reservations,
          SUM(COALESCE(source_kind,'')='') AS missing_source_kind,
          SUM(COALESCE(upstream_model_id,'')='') AS missing_upstream_model,
          SUM(usage_contract_version>=1) AS contract_v1,
          SUM(canonical_present=1) AS canonical_present,
          SUM(COALESCE(pricing_config_hash,'')='') AS missing_pricing_hash
        FROM oneapi_billing.billing_ledgers WHERE type='consume'
          AND created_at>=@audit_start AND created_at<@audit_end""",
    "kimi_channel_1_history": """
        SELECT COUNT(*) AS ledger_rows,MIN(created_at) AS first_at,MAX(created_at) AS last_at,
          SUM(COALESCE(source_kind,'')='') AS missing_source_kind,
          SUM(COALESCE(upstream_model_id,'')='') AS missing_upstream_model,
          SUM(created_at>=@audit_start AND created_at<@audit_end) AS in_window,
          SUM(created_at>=@audit_start AND created_at<@audit_end AND
            COALESCE(source_kind,'')='') AS missing_source_in_window,
          SUM(created_at>=@audit_start AND created_at<@audit_end AND
            COALESCE(upstream_model_id,'')='') AS missing_model_in_window,
          MAX(CASE WHEN COALESCE(upstream_model_id,'')='' THEN created_at END) AS last_missing_model
        FROM oneapi_billing.billing_ledgers
        WHERE type='consume' AND channel_id=1 AND LOWER(model_name) LIKE '%kimi%'""",
    "consume_source_breakdown_24h": """
        SELECT channel_id,source_kind,model_name,COUNT(*) AS ledger_rows,
          SUM(COALESCE(upstream_model_id,'')='') AS missing_upstream_model
        FROM oneapi_billing.billing_ledgers WHERE type='consume'
          AND created_at>=@audit_start AND created_at<@audit_end
        GROUP BY channel_id,source_kind,model_name ORDER BY channel_id,model_name""",
}
result["sql"] = {}
for name, query in queries.items():
    sql = (
        "SET SESSION MAX_EXECUTION_TIME=5000; SET SESSION time_zone='+00:00';\n"
        f"SET @audit_start='{start:%Y-%m-%d %H:%M:%S}';\n"
        f"SET @audit_end='{end:%Y-%m-%d %H:%M:%S}';\n"
        "START TRANSACTION READ ONLY;\n" + query + ";\nCOMMIT;"
    )
    try:
        db = command(["docker", "exec", "-i", "mysql", "sh", "-c",
                      'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot --batch'], input=sql)
        result["sql"][name] = {"status": "OBSERVED" if db.returncode == 0 else "UNKNOWN"}
        if db.returncode == 0:
            result["sql"][name]["tsv"] = db.stdout.strip()
        else:
            result["sql"][name]["exit_code"] = db.returncode
    except (OSError, subprocess.TimeoutExpired) as error:
        result["sql"][name] = {"status": "UNKNOWN", "error_type": type(error).__name__}

try:
    inspect = command(["docker", "inspect", "admin-api", "billing-service", "relay-gateway"])
    if inspect.returncode != 0:
        raise ValueError("inspect failed")
    containers = {c["Name"].lstrip("/"): c for c in json.loads(inspect.stdout)}
    envs = {name: dict(item.split("=", 1) for item in c["Config"]["Env"])
            for name, c in containers.items()}
    flags = ("BILLING_CANONICAL_USAGE_MODE", "BILLING_CACHE_CREATION_MODE",
             "BILLING_CANONICAL_USAGE_CHARGE_ALLOWLIST")
    result["flags"] = {name: {key: envs[name].get(key, "<unset>") for key in flags}
                       for name in ("billing-service", "relay-gateway")}
    result["image_ids"] = {name: c["Image"] for name, c in containers.items()}
    token = envs["admin-api"].get("ADMIN_TOKEN")
    if token:
        request = urllib.request.Request(
            "http://127.0.0.1:3000/api/admin/notifications?page=1&page_size=1",
            headers={"Authorization": "Bearer " + token})
        try:
            with urllib.request.urlopen(request, timeout=15) as response:
                payload = json.load(response)
                data = payload.get("data", payload)
                rows = data.get("items") or []
                result["notification_admin_read"] = {
                    "http_status": response.status, "total": data.get("total"),
                    "returned_rows": len(rows),
                    "statuses": sorted({row.get("status", "") for row in rows}),
                }
        except urllib.error.HTTPError as error:
            result["notification_admin_read"] = {"http_status": error.code}
        except (OSError, ValueError, TypeError, AttributeError) as error:
            result["notification_admin_read"] = {"status": "UNKNOWN", "error_type": type(error).__name__}
    else:
        result["notification_admin_read"] = {"status": "UNKNOWN", "reason": "no admin credential"}
except (OSError, subprocess.TimeoutExpired, ValueError, KeyError, TypeError) as error:
    result["container_read"] = {"status": "UNKNOWN", "error_type": type(error).__name__}

print(json.dumps(result, ensure_ascii=False, indent=2))
