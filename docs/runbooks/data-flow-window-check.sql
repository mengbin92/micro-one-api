-- Historical observation window from the 2026-09-19 production audit (UTC).
-- Run through the MySQL client on the deployment host; all statements are read-only.
SET SESSION MAX_EXECUTION_TIME = 5000;
SET SESSION time_zone = '+00:00';
START TRANSACTION READ ONLY;

SELECT
    COUNT(*) AS committed,
    SUM(l.reference_id IS NULL) AS missing_ledger,
    SUM(g.request_id IS NULL) AS missing_log,
    SUM(r.actual_cost <> -l.amount) AS settlement_cost_mismatch,
    SUM(l.quota <> g.quota) AS ledger_log_quota_mismatch,
    SUM(l.prompt_tokens <> g.prompt_tokens OR l.completion_tokens <> g.completion_tokens) AS token_mismatch,
    SUM(r.channel_id > 0 AND r.subscription_account_id IN ('', '0') AND e.reservation_id IS NULL) AS missing_channel_event,
    SUM(e.quota <> l.quota) AS channel_event_quota_mismatch
FROM oneapi_billing.billing_reservations r
LEFT JOIN (
    SELECT reference_id, SUM(amount) AS amount, MAX(quota) AS quota,
           MAX(prompt_tokens) AS prompt_tokens, MAX(completion_tokens) AS completion_tokens
    FROM oneapi_billing.billing_ledgers
    WHERE type = 'consume' AND created_at >= '2026-09-18 05:36:37'
    GROUP BY reference_id
) l ON l.reference_id = r.reservation_id
LEFT JOIN (
    SELECT request_id, MAX(quota) AS quota,
           MAX(prompt_tokens) AS prompt_tokens, MAX(completion_tokens) AS completion_tokens
    FROM oneapi_log.logs
    WHERE level = 'consume' AND created_at >= UNIX_TIMESTAMP('2026-09-18 05:36:37')
    GROUP BY request_id
) g ON g.request_id = r.request_id
LEFT JOIN oneapi_channel.channel_usage_events e ON e.reservation_id = r.reservation_id
WHERE r.status = 'committed'
  AND r.created_at >= '2026-09-18 05:36:37'
  AND r.created_at < '2026-09-19 05:36:37';

SELECT COUNT(*) AS committed,
       SUM(subscription_id > 0) AS with_subscription,
       SUM(request_snapshot IS NOT NULL AND request_snapshot <> '') AS with_snapshot,
       SUM(request_snapshot_hash <> '') AS with_snapshot_hash
FROM oneapi_billing.billing_reservations
WHERE status = 'committed'
  AND created_at >= '2026-09-18 05:36:37'
  AND created_at < '2026-09-19 05:36:37';

COMMIT;
