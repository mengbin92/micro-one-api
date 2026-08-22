-- One-time MySQL repair for the historical channel-counter and usage-log
-- drift. Run only after migrations 080-082 have completed. The billing ledger
-- is authoritative; duplicate dual-track ledger rows are collapsed by
-- reservation_id before either repair is calculated.
--
-- The transaction is intentionally left uncommitted. Review the two preview
-- result sets and replace ROLLBACK with COMMIT for the production execution.

START TRANSACTION;

DROP TEMPORARY TABLE IF EXISTS expected_channel_usage;
CREATE TEMPORARY TABLE expected_channel_usage AS
SELECT request_usage.channel_id, SUM(request_usage.quota) AS expected_used_quota
FROM (
  SELECT reference_id, MAX(channel_id) AS channel_id, MAX(quota) AS quota
  FROM oneapi_billing.billing_ledgers
  WHERE type = 'consume' AND source_kind = 'channel' AND channel_id > 0
  GROUP BY reference_id
) AS request_usage
GROUP BY request_usage.channel_id;

SELECT c.id AS channel_id,
       c.used_quota AS before_used_quota,
       e.expected_used_quota,
       e.expected_used_quota - c.used_quota AS correction
FROM oneapi_channel.channels AS c
JOIN expected_channel_usage AS e ON e.channel_id = c.id
WHERE c.used_quota <> e.expected_used_quota
ORDER BY ABS(e.expected_used_quota - c.used_quota) DESC;

UPDATE oneapi_channel.channels AS c
JOIN expected_channel_usage AS e ON e.channel_id = c.id
SET c.used_quota = e.expected_used_quota
WHERE c.used_quota <> e.expected_used_quota;

DROP TEMPORARY TABLE IF EXISTS missing_usage_logs;
CREATE TEMPORARY TABLE missing_usage_logs AS
SELECT
  MIN(l.created_at) AS ledger_created_at,
  CAST(l.user_id AS UNSIGNED) AS user_id,
  r.request_id,
  MAX(l.token_name) AS token_name,
  MAX(l.model_name) AS model_name,
  MAX(l.quota) AS quota,
  MAX(l.prompt_tokens) AS prompt_tokens,
  MAX(l.completion_tokens) AS completion_tokens,
  MAX(l.cache_read_tokens) AS cache_read_tokens,
  MAX(l.cache_creation_5m_tokens) AS cache_creation_5m_tokens,
  MAX(l.cache_creation_1h_tokens) AS cache_creation_1h_tokens,
  MAX(l.channel_id) AS channel_id,
  MAX(l.subscription_account_id) AS subscription_account_id,
  MAX(l.elapsed_time) AS elapsed_time,
  MAX(l.is_stream) AS is_stream
FROM oneapi_billing.billing_ledgers AS l
JOIN oneapi_billing.billing_reservations AS r ON r.reservation_id = l.reference_id
WHERE l.type = 'consume'
  AND r.request_id <> ''
  AND NOT EXISTS (
    SELECT 1
    FROM oneapi_log.logs AS existing
    WHERE existing.level = 'consume'
      AND existing.user_id = CAST(l.user_id AS UNSIGNED)
      AND existing.request_id = r.request_id
  )
GROUP BY l.reference_id, l.user_id, r.request_id;

SELECT COUNT(*) AS rows_to_insert, COALESCE(SUM(quota), 0) AS quota_to_insert
FROM missing_usage_logs;

INSERT INTO oneapi_log.logs (
  level, message, source, request_id, user_id, created_at, username,
  token_name, model_name, quota, prompt_tokens, completion_tokens,
  cache_read_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens,
  channel_id, subscription_account_id, elapsed_time, is_stream
)
SELECT
  'consume', CONCAT('historical usage log backfill: request_id=', request_id),
  'relay-gateway', request_id, user_id, UNIX_TIMESTAMP(ledger_created_at), '',
  token_name, model_name, quota, prompt_tokens, completion_tokens,
  cache_read_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens,
  channel_id, subscription_account_id, elapsed_time, is_stream
FROM missing_usage_logs;

INSERT IGNORE INTO oneapi_log.log_ingest_dedupe_claims (dedupe_key, log_id, created_at)
SELECT CONCAT('consume:', logs.user_id, ':', logs.request_id), MIN(logs.id), MIN(logs.created_at)
FROM oneapi_log.logs AS logs
JOIN missing_usage_logs AS missing
  ON missing.user_id = logs.user_id AND missing.request_id = logs.request_id
WHERE logs.level = 'consume'
GROUP BY logs.user_id, logs.request_id;

SELECT
  (SELECT COUNT(DISTINCT reference_id) FROM oneapi_billing.billing_ledgers WHERE type = 'consume') AS ledger_requests,
  (SELECT COUNT(*) FROM oneapi_log.logs WHERE level = 'consume') AS consume_logs,
  (SELECT COALESCE(SUM(expected_used_quota), 0) FROM expected_channel_usage) AS ledger_channel_quota,
  (SELECT COALESCE(SUM(c.used_quota), 0)
     FROM oneapi_channel.channels AS c
     JOIN expected_channel_usage AS e ON e.channel_id = c.id) AS channel_used_quota;

ROLLBACK;
