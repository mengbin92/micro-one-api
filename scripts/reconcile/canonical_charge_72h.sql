-- Canonical usage limited-source charge gate (v0.27).
--
-- Production billing was deployed at 2026-09-06 02:40:07.806 UTC with
-- BILLING_CANONICAL_USAGE_MODE=charge and one exact subscription-account /
-- upstream-model allowlist entry for K3. The source key is represented only by
-- its SHA-256 selector below; this report never prints the account id.
--
-- The fixed 72-hour window was frozen at the first qualifying allowlisted
-- consume after deployment. Every statement is read-only.
--
-- Cost rebuild replicates production float64 semantics, not DECIMAL exact
-- arithmetic (2026-09-09 scoped-PASS decision): production roundScaled
-- computes float64(tokens)*price*multiplier*AmountScale left-to-right and
-- rounds with Go math.Round (half away from zero), so a bucket whose exact
-- decimal product lands on a .5 boundary can be 1 ULP below it in float64
-- and round DOWN, while DECIMAL ROUND rounds it UP (the 8 glm-5.3-flash
-- rows in the frozen window). Snapshot prices are decimal(32,17), which
-- round-trips the float64 values the pricing function consumed (migration
-- 088), so CAST(... AS DOUBLE) restores the exact production operands.
-- Go math.Round is emulated as FLOOR(x) + (x - FLOOR(x) >= 0.5): both the
-- subtraction and the comparison are exact in double arithmetic, so the
-- result is bit-for-bit identical for every non-negative double. The
-- naive FLOOR(x + 0.5) is NOT equivalent: for x = 0.49999999999999994
-- (1 ULP below 0.5) the addition x + 0.5 lands exactly halfway between
-- 0.9999999999999999 and 1.0 and rounds to even 1.0, producing 1 where
-- Go yields 0 (the glm-5.3-flash 200-completion bucket).
--
-- The legacy rebuild mirrors production legacyCanonicalBuckets: the flat
-- prompt is reduced by cache_read ONLY when the producer is not
-- PromptExclusive. PromptExclusive is not persisted on the ledger; it is
-- derived here from the subscription account platform
-- (claude/zhipu/minimax/kimi) or the channel type
-- (2,4,17,27,33,34,35,36) -- the same inputs relay uses
-- (internal/biz/usage.go IsPromptExclusiveChannel). usage_semantics is NOT
-- the discriminator: a kimi/zhipu subscription /v1/responses row is
-- openai_subset in envelope semantics but PromptExclusive in legacy cost,
-- which the 2026-09-10 rollback drill exposed (17 K3 rows charged legacy
-- full-prompt while the gate rebuilt prompt-minus-cache).

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
    -- PromptExclusive per legacyCanonicalBuckets (see header): flat prompt is
    -- reduced by cache_read only for non-exclusive producers.
    MAX(CASE
      WHEN l.source_kind = 'subscription'
        AND sa.platform IN ('claude', 'zhipu', 'minimax', 'kimi') THEN 1
      WHEN l.source_kind = 'channel'
        AND c.type IN (2, 4, 17, 27, 33, 34, 35, 36) THEN 1
      ELSE 0 END) AS prompt_exclusive,
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
  LEFT JOIN oneapi_channel.subscription_accounts sa
    ON l.source_kind = 'subscription'
    AND sa.id = l.subscription_account_id
  LEFT JOIN oneapi_channel.channels c
    ON l.source_kind = 'channel'
    AND c.id = l.channel_id
  WHERE l.type = 'consume'
    AND l.usage_contract_version = 1
    AND l.usage_parse_status IN ('verified', 'estimated')
  GROUP BY l.reference_id
), rebuilt_raw AS (
  SELECT *,
    CASE
      WHEN source_kind = 'subscription'
        AND SHA2(CONCAT(subscription_account_id, ':', upstream_model_id), 256)
          = @charge_source_hash
      THEN 1 ELSE 0
    END AS is_allowlisted,
    -- roundScaled replica, step 1: float64(tokens)*price*multiplier*AmountScale
    -- as left-to-right double multiplication (billing.go roundScaled).
    CAST(uncached_input_tokens AS DOUBLE) * CAST(input_price AS DOUBLE)
      * CAST(group_ratio AS DOUBLE) * 10000 AS raw_canon_input,
    CAST((CASE
      WHEN prompt_exclusive = 1 THEN prompt_tokens
      ELSE GREATEST(prompt_tokens - cache_read_tokens, 0)
    END) AS DOUBLE) * CAST(input_price AS DOUBLE)
      * CAST(group_ratio AS DOUBLE) * 10000 AS raw_legacy_input,
    CAST(cache_read_tokens AS DOUBLE)
      * CAST(COALESCE(cache_read_price, input_price) AS DOUBLE)
      * CAST(group_ratio AS DOUBLE) * 10000 AS raw_cache_read,
    CAST(canonical_output_tokens AS DOUBLE) * CAST(output_price AS DOUBLE)
      * CAST(group_ratio AS DOUBLE) * 10000 AS raw_canon_output,
    CAST(completion_tokens AS DOUBLE) * CAST(output_price AS DOUBLE)
      * CAST(group_ratio AS DOUBLE) * 10000 AS raw_legacy_output,
    CAST(cache_creation_5m_tokens AS DOUBLE)
      * CAST(COALESCE(cache_creation_5m_price, 0) AS DOUBLE)
      * CAST(group_ratio AS DOUBLE) * 10000 AS raw_cc5m,
    CAST(cache_creation_1h_tokens AS DOUBLE)
      * CAST(COALESCE(cache_creation_1h_price, 0) AS DOUBLE)
      * CAST(group_ratio AS DOUBLE) * 10000 AS raw_cc1h
  FROM request_rows
), rebuilt AS (
  SELECT *,
    -- roundScaled replica, step 2: Go math.Round per bucket (exact
    -- FLOOR(x) + (fraction >= 0.5), NOT FLOOR(x + 0.5)), integer sum
    -- (billing.go calculateCanonicalCost / calculateModelPriceCost).
    GREATEST(1,
      CAST(FLOOR(raw_canon_input)
        + (raw_canon_input - FLOOR(raw_canon_input) >= 0.5) AS SIGNED)
      + CAST(FLOOR(raw_cache_read)
        + (raw_cache_read - FLOOR(raw_cache_read) >= 0.5) AS SIGNED)
      + CAST(FLOOR(raw_canon_output)
        + (raw_canon_output - FLOOR(raw_canon_output) >= 0.5) AS SIGNED)
      + CASE WHEN cache_creation_mode = 'charge' THEN
          CAST(FLOOR(raw_cc5m)
            + (raw_cc5m - FLOOR(raw_cc5m) >= 0.5) AS SIGNED)
          + CAST(FLOOR(raw_cc1h)
            + (raw_cc1h - FLOOR(raw_cc1h) >= 0.5) AS SIGNED)
        ELSE 0 END
    ) AS canonical_cost,
    GREATEST(1,
      CAST(FLOOR(raw_legacy_input)
        + (raw_legacy_input - FLOOR(raw_legacy_input) >= 0.5) AS SIGNED)
      + CAST(FLOOR(raw_cache_read)
        + (raw_cache_read - FLOOR(raw_cache_read) >= 0.5) AS SIGNED)
      + CAST(FLOOR(raw_legacy_output)
        + (raw_legacy_output - FLOOR(raw_legacy_output) >= 0.5) AS SIGNED)
      + CASE WHEN cache_creation_mode = 'charge' THEN
          CAST(FLOOR(raw_cc5m)
            + (raw_cc5m - FLOOR(raw_cc5m) >= 0.5) AS SIGNED)
          + CAST(FLOOR(raw_cc1h)
            + (raw_cc1h - FLOOR(raw_cc1h) >= 0.5) AS SIGNED)
        ELSE 0 END
    ) AS legacy_cost
  FROM rebuilt_raw
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
