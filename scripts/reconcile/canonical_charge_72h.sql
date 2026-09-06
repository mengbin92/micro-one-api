-- Canonical usage limited-source charge gate (v0.27).
--
-- Production billing was deployed at 2026-09-06 02:40:07.806 UTC with
-- BILLING_CANONICAL_USAGE_MODE=charge and one exact subscription-account /
-- upstream-model allowlist entry for K3. The source key is represented only by
-- its SHA-256 selector below; this report never prints the account id.
--
-- The fixed 72-hour window was frozen at the first qualifying allowlisted
-- consume after deployment. Every statement is read-only.

SET @charge_deployed_at = TIMESTAMP('2026-09-06 02:40:07.806');
SET @charge_source_hash = 'bc8fc070f270e9f88fc41ac5aa7932f88c10e9bd440ac2f1afa35bf174e2ebf0';
SET @charge_start = TIMESTAMP('2026-09-06 02:41:50.397');
SET @charge_end = DATE_ADD(@charge_start, INTERVAL 72 HOUR);

SELECT
  'window_elapsed' AS check_name,
  CASE
    WHEN @charge_start IS NULL THEN 'WAIT_FOR_FIRST_SAMPLE'
    WHEN UTC_TIMESTAMP(3) < @charge_end THEN 'WAIT'
    ELSE 'PASS'
  END AS status,
  @charge_deployed_at AS deployed_at_utc,
  @charge_start AS charge_start_utc,
  @charge_end AS charge_end_utc,
  UTC_TIMESTAMP(3) AS checked_at_utc;

-- The allowlisted source must remain one exact K3 subscription source. Every
-- allowlisted row must retain a valid v1 envelope and a resolvable frozen
-- pricing snapshot; an account switch for the same upstream model is a blocker.
WITH scope_check AS (
  SELECT
    COUNT(*) AS total_rows,
    COALESCE(SUM(
      source_kind = 'subscription'
      AND SHA2(CONCAT(subscription_account_id, ':', upstream_model_id), 256)
        = @charge_source_hash
    ), 0) AS allowlisted_rows,
    COALESCE(SUM(
      upstream_model_id = 'k3'
      AND (
        COALESCE(source_kind, '') <> 'subscription'
        OR COALESCE(SHA2(CONCAT(subscription_account_id, ':', upstream_model_id), 256), '')
          <> @charge_source_hash
      )
    ), 0) AS unexpected_k3_source_rows,
    COALESCE(SUM(
      source_kind = 'subscription'
      AND SHA2(CONCAT(subscription_account_id, ':', upstream_model_id), 256)
        = @charge_source_hash
      AND (
        usage_contract_version <> 1
        OR usage_parse_status NOT IN ('verified', 'estimated')
        OR canonical_present <> 1
        OR pricing_config_hash = ''
        OR pricing_snapshot_resolved = 0
      )
    ), 0) AS invalid_allowlisted_rows
  FROM (
    SELECT l.*, s.config_hash IS NOT NULL AS pricing_snapshot_resolved
    FROM oneapi_billing.billing_ledgers l
    LEFT JOIN oneapi_billing.billing_pricing_snapshots s
      ON s.config_hash = l.pricing_config_hash
    WHERE l.type = 'consume'
      AND l.created_at >= @charge_start
      AND l.created_at < @charge_end
  ) window_rows
)
SELECT
  'allowlisted_source_and_contract' AS check_name,
  CASE
    WHEN @charge_start IS NULL THEN 'WAIT'
    WHEN unexpected_k3_source_rows > 0 OR invalid_allowlisted_rows > 0 THEN 'FAIL'
    WHEN UTC_TIMESTAMP(3) < @charge_end THEN 'WAIT'
    WHEN allowlisted_rows > 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  total_rows,
  allowlisted_rows,
  unexpected_k3_source_rows,
  invalid_allowlisted_rows
FROM scope_check;

-- Rebuild both settlement candidates per reservation from immutable ledger and
-- snapshot evidence. Allowlisted K3 requests must equal canonical cost;
-- everything else must equal legacy cost. A distinguishable non-allowlisted
-- sample is required so this also proves the gate did not silently become
-- global. The minimum successful charge of 1 quota is applied to both sides.
WITH window_references AS (
  SELECT DISTINCT l.reference_id
  FROM oneapi_billing.billing_ledgers l
  WHERE l.type = 'consume'
    AND l.created_at >= @charge_start
    AND l.created_at < @charge_end
    AND l.reference_id IS NOT NULL
    AND l.reference_id <> ''
    AND NOT EXISTS (
      SELECT 1
      FROM oneapi_billing.billing_ledgers prior
      WHERE prior.type = 'consume'
        AND prior.reference_id = l.reference_id
        AND prior.created_at < @charge_start
    )
), request_rows AS (
  SELECT
    l.reference_id,
    MAX(l.source_kind) AS source_kind,
    MAX(l.upstream_model_id) AS upstream_model_id,
    MAX(l.subscription_account_id) AS subscription_account_id,
    SUM(ABS(l.amount)) AS charged_cost,
    MAX(l.prompt_tokens) AS prompt_tokens,
    MAX(l.completion_tokens) AS completion_tokens,
    MAX(l.cache_read_tokens) AS cache_read_tokens,
    MAX(l.cache_creation_5m_tokens) AS cache_creation_5m_tokens,
    MAX(l.cache_creation_1h_tokens) AS cache_creation_1h_tokens,
    MAX(l.uncached_input_tokens) AS uncached_input_tokens,
    MAX(l.billable_total_tokens - l.uncached_input_tokens
      - l.cache_read_tokens - l.cache_creation_5m_tokens
      - l.cache_creation_1h_tokens) AS canonical_output_tokens,
    MAX(l.usage_semantics) AS usage_semantics,
    MAX(s.input_price) AS input_price,
    MAX(s.output_price) AS output_price,
    MAX(s.cache_read_price) AS cache_read_price,
    MAX(s.cache_creation_5m_price) AS cache_creation_5m_price,
    MAX(s.cache_creation_1h_price) AS cache_creation_1h_price,
    MAX(s.group_ratio) AS group_ratio,
    MAX(s.cache_creation_mode) AS cache_creation_mode
  FROM oneapi_billing.billing_ledgers l
  JOIN window_references w
    ON w.reference_id = l.reference_id
  JOIN oneapi_billing.billing_pricing_snapshots s
    ON s.config_hash = l.pricing_config_hash
  WHERE l.type = 'consume'
    AND l.usage_contract_version = 1
    AND l.usage_parse_status IN ('verified', 'estimated')
  GROUP BY l.reference_id
), rebuilt AS (
  SELECT *,
    CASE
      WHEN source_kind = 'subscription'
        AND SHA2(CONCAT(subscription_account_id, ':', upstream_model_id), 256)
          = @charge_source_hash
      THEN 1 ELSE 0
    END AS is_allowlisted,
    GREATEST(1,
      ROUND(uncached_input_tokens * input_price * group_ratio * 10000)
      + ROUND(cache_read_tokens * COALESCE(cache_read_price, input_price)
        * group_ratio * 10000)
      + ROUND(canonical_output_tokens * output_price * group_ratio * 10000)
      + CASE WHEN cache_creation_mode = 'charge' THEN
          ROUND(cache_creation_5m_tokens
            * COALESCE(cache_creation_5m_price, 0) * group_ratio * 10000)
          + ROUND(cache_creation_1h_tokens
            * COALESCE(cache_creation_1h_price, 0) * group_ratio * 10000)
        ELSE 0 END
    ) AS canonical_cost,
    GREATEST(1,
      ROUND((CASE
        WHEN usage_semantics = 'anthropic_exclusive' THEN prompt_tokens
        ELSE GREATEST(prompt_tokens - cache_read_tokens, 0)
      END) * input_price * group_ratio * 10000)
      + ROUND(cache_read_tokens * COALESCE(cache_read_price, input_price)
        * group_ratio * 10000)
      + ROUND(completion_tokens * output_price * group_ratio * 10000)
      + CASE WHEN cache_creation_mode = 'charge' THEN
          ROUND(cache_creation_5m_tokens
            * COALESCE(cache_creation_5m_price, 0) * group_ratio * 10000)
          + ROUND(cache_creation_1h_tokens
            * COALESCE(cache_creation_1h_price, 0) * group_ratio * 10000)
        ELSE 0 END
    ) AS legacy_cost
  FROM request_rows
), cost_check AS (
  SELECT
    COUNT(*) AS total_requests,
    COALESCE(SUM(is_allowlisted), 0) AS allowlisted_requests,
    COALESCE(SUM(is_allowlisted AND charged_cost <> canonical_cost), 0)
      AS allowlisted_wrong_cost,
    COALESCE(SUM(is_allowlisted AND canonical_cost > legacy_cost), 0)
      AS allowlisted_positive_delta,
    COALESCE(SUM(NOT is_allowlisted AND canonical_cost <> legacy_cost), 0)
      AS nonallowlisted_distinguishable_requests,
    COALESCE(SUM(NOT is_allowlisted AND charged_cost <> legacy_cost), 0)
      AS nonallowlisted_wrong_cost
  FROM rebuilt
)
SELECT
  'charge_scope_cost_selection' AS check_name,
  CASE
    WHEN @charge_start IS NULL THEN 'WAIT'
    WHEN allowlisted_wrong_cost > 0
      OR allowlisted_positive_delta > 0
      OR nonallowlisted_wrong_cost > 0 THEN 'FAIL'
    WHEN UTC_TIMESTAMP(3) < @charge_end THEN 'WAIT'
    WHEN allowlisted_requests > 0
      AND nonallowlisted_distinguishable_requests > 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  total_requests,
  allowlisted_requests,
  allowlisted_wrong_cost,
  allowlisted_positive_delta,
  nonallowlisted_distinguishable_requests,
  nonallowlisted_wrong_cost
FROM cost_check;

-- The request state machine and dedupe claim must still be exact. Reservation
-- actual_cost is compared with the sum of split consume ledgers so a request
-- paid partly by subscription and partly by balance is handled correctly.
WITH window_references AS (
  SELECT DISTINCT l.reference_id
  FROM oneapi_billing.billing_ledgers l
  WHERE l.type = 'consume'
    AND l.created_at >= @charge_start
    AND l.created_at < @charge_end
    AND l.reference_id IS NOT NULL
    AND l.reference_id <> ''
    AND NOT EXISTS (
      SELECT 1
      FROM oneapi_billing.billing_ledgers prior
      WHERE prior.type = 'consume'
        AND prior.reference_id = l.reference_id
        AND prior.created_at < @charge_start
    )
), window_ledgers AS (
  SELECT l.*
  FROM oneapi_billing.billing_ledgers l
  JOIN window_references w
    ON w.reference_id = l.reference_id
  WHERE l.type = 'consume'
), settled AS (
  SELECT reference_id, SUM(ABS(amount)) AS charged_cost,
    MAX(CASE
      WHEN source_kind = 'subscription'
        AND SHA2(CONCAT(subscription_account_id, ':', upstream_model_id), 256)
          = @charge_source_hash
      THEN 1 ELSE 0
    END) AS is_allowlisted
  FROM window_ledgers
  GROUP BY reference_id
), integrity_check AS (
  SELECT
    (SELECT COUNT(*) FROM window_ledgers WHERE ledger_dedupe_key = '') AS empty_keys,
    (SELECT COUNT(*) FROM (
      SELECT ledger_dedupe_key FROM window_ledgers
      GROUP BY ledger_dedupe_key HAVING COUNT(*) > 1
    ) duplicates) AS duplicate_key_groups,
    (SELECT COUNT(*) FROM window_ledgers l
      LEFT JOIN oneapi_billing.billing_ledger_dedupe_claims c
        ON c.ledger_dedupe_key = l.ledger_dedupe_key
      WHERE c.ledger_dedupe_key IS NULL) AS ledgers_without_claim,
    (SELECT COUNT(*) FROM oneapi_billing.billing_ledger_dedupe_claims c
      LEFT JOIN oneapi_billing.billing_ledgers l
        ON l.ledger_dedupe_key = c.ledger_dedupe_key
      WHERE l.ledger_dedupe_key IS NULL
        AND c.created_at >= @charge_start
        AND c.created_at < @charge_end) AS claims_without_ledger,
    (SELECT COUNT(*) FROM settled x
      JOIN oneapi_billing.billing_reservations r
        ON r.reservation_id = x.reference_id
      WHERE x.is_allowlisted
        AND (r.status <> 'committed' OR r.actual_cost <> x.charged_cost))
      AS allowlisted_reservation_mismatches,
    (SELECT COUNT(*) FROM settled WHERE is_allowlisted) AS allowlisted_requests
)
SELECT
  'ledger_idempotency_and_reservation' AS check_name,
  CASE
    WHEN @charge_start IS NULL THEN 'WAIT'
    WHEN empty_keys > 0
      OR duplicate_key_groups > 0
      OR ledgers_without_claim > 0
      OR claims_without_ledger > 0
      OR allowlisted_reservation_mismatches > 0 THEN 'FAIL'
    WHEN UTC_TIMESTAMP(3) < @charge_end THEN 'WAIT'
    WHEN allowlisted_requests > 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  empty_keys,
  duplicate_key_groups,
  ledgers_without_claim,
  claims_without_ledger,
  allowlisted_reservation_mismatches
FROM integrity_check;

-- Billing and log have no shared one-to-one request key. Compare their common
-- usage/source/latency fields as multisets. Logs have whole-second timestamps,
-- so both sides use the same interior whole-second subwindow; floor/ceil in the
-- opposite direction would pull boundary-external log rows into the result.
-- Dual-track settlement may write subscription and balance rows for one
-- request, so identical audit fields are collapsed by reference first; rows
-- without a reference retain their individual ledger id.
WITH billing_requests AS (
  SELECT
    COALESCE(NULLIF(reference_id, ''), CONCAT('__ledger__:', id)) AS request_key,
    CAST(user_id AS CHAR) AS user_id,
    token_name, model_name, quota,
    prompt_tokens, completion_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens,
    uncached_input_tokens, reported_prompt_tokens, reported_total_tokens,
    billable_total_tokens, usage_semantics, usage_protocol,
    usage_field_shape, usage_parse_status, usage_contract_version,
    canonical_present, usage_decision_reason, channel_id,
    subscription_account_id, elapsed_time, is_stream
  FROM oneapi_billing.billing_ledgers
  WHERE type = 'consume'
    AND created_at >= FROM_UNIXTIME(CEIL(UNIX_TIMESTAMP(@charge_start)))
    AND created_at < FROM_UNIXTIME(FLOOR(UNIX_TIMESTAMP(@charge_end)))
  GROUP BY
    request_key,
    user_id, token_name, model_name, quota,
    prompt_tokens, completion_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens,
    uncached_input_tokens, reported_prompt_tokens, reported_total_tokens,
    billable_total_tokens, usage_semantics, usage_protocol,
    usage_field_shape, usage_parse_status, usage_contract_version,
    canonical_present, usage_decision_reason, channel_id,
    subscription_account_id, elapsed_time, is_stream
), combined AS (
  SELECT
    user_id, token_name, model_name, quota,
    prompt_tokens, completion_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens,
    uncached_input_tokens, reported_prompt_tokens, reported_total_tokens,
    billable_total_tokens, usage_semantics, usage_protocol,
    usage_field_shape, usage_parse_status, usage_contract_version,
    canonical_present, usage_decision_reason, channel_id,
    subscription_account_id, elapsed_time, is_stream,
    1 AS billing_n, 0 AS log_n
  FROM billing_requests

  UNION ALL

  SELECT
    CAST(user_id AS CHAR),
    token_name, model_name, quota,
    prompt_tokens, completion_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens,
    uncached_input_tokens, reported_prompt_tokens, reported_total_tokens,
    billable_total_tokens, usage_semantics, usage_protocol,
    usage_field_shape, usage_parse_status, usage_contract_version,
    canonical_present, usage_decision_reason, channel_id,
    subscription_account_id, elapsed_time, is_stream,
    0, 1
  FROM oneapi_log.logs
  WHERE level = 'consume'
    AND created_at >= CEIL(UNIX_TIMESTAMP(@charge_start))
    AND created_at < FLOOR(UNIX_TIMESTAMP(@charge_end))
), grouped AS (
  SELECT
    user_id, token_name, model_name, quota,
    prompt_tokens, completion_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens,
    uncached_input_tokens, reported_prompt_tokens, reported_total_tokens,
    billable_total_tokens, usage_semantics, usage_protocol,
    usage_field_shape, usage_parse_status, usage_contract_version,
    canonical_present, usage_decision_reason, channel_id,
    subscription_account_id, elapsed_time, is_stream,
    SUM(billing_n) AS billing_n, SUM(log_n) AS log_n
  FROM combined
  GROUP BY
    user_id, token_name, model_name, quota,
    prompt_tokens, completion_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens,
    uncached_input_tokens, reported_prompt_tokens, reported_total_tokens,
    billable_total_tokens, usage_semantics, usage_protocol,
    usage_field_shape, usage_parse_status, usage_contract_version,
    canonical_present, usage_decision_reason, channel_id,
    subscription_account_id, elapsed_time, is_stream
), multiset_check AS (
  SELECT COALESCE(SUM(billing_n), 0) AS billing_rows,
    COALESCE(SUM(log_n), 0) AS log_rows,
    COALESCE(SUM(billing_n <> log_n), 0) AS differing_groups
  FROM grouped
)
SELECT
  'billing_log_multiset' AS check_name,
  CASE
    WHEN @charge_start IS NULL OR UTC_TIMESTAMP(3) < @charge_end THEN 'WAIT'
    WHEN billing_rows > 0 AND billing_rows = log_rows
      AND differing_groups = 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  billing_rows,
  log_rows,
  differing_groups
FROM multiset_check;
