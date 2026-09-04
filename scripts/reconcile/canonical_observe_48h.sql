-- Canonical usage 48-hour observe gate (v0.27).
--
-- Production MySQL stores billing_ledgers.created_at in UTC. The qualified
-- natural-traffic window starts at 2026-09-02 11:12:00.225 CST and ends 48
-- hours later. Every statement is read-only.
--
-- Run from the repository root against a MySQL client connected to production:
--   mysql --table < scripts/reconcile/canonical_observe_48h.sql

SET @observe_start = TIMESTAMP('2026-09-02 03:12:00.225');
SET @observe_end = TIMESTAMP('2026-09-04 03:12:00.225');
SET @controlled_test_model = 'step-explore';

SELECT
  'window_elapsed' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) >= @observe_end THEN 'PASS'
    ELSE 'WAIT'
  END AS status,
  @observe_start AS observe_start_utc,
  @observe_end AS observe_end_utc,
  UTC_TIMESTAMP(3) AS checked_at_utc;

-- Contract, canonical-bucket, and semantics validity. Estimated rows are legal
-- only when no cache bucket is present; cached usage must be verified and have
-- an explicit subset/exclusive verdict.
WITH window_rows AS (
  SELECT *
  FROM oneapi_billing.billing_ledgers
  WHERE type = 'consume'
    AND created_at >= @observe_start
    AND created_at < @observe_end
), contract_check AS (
  SELECT
    COUNT(*) AS total_rows,
    COALESCE(SUM(
      usage_contract_version <> 1
      OR usage_parse_status NOT IN ('verified', 'estimated')
      OR canonical_present <> 1
      OR billable_total_tokens <>
         uncached_input_tokens + cache_read_tokens
         + cache_creation_5m_tokens + cache_creation_1h_tokens
         + completion_tokens
      OR (
        cache_read_tokens + cache_creation_5m_tokens
          + cache_creation_1h_tokens > 0
        AND (
          usage_parse_status <> 'verified'
          OR usage_semantics NOT IN ('openai_subset', 'anthropic_exclusive')
        )
      )
    ), 0) AS invalid_rows,
    COALESCE(SUM(usage_parse_status = 'ambiguous'), 0) AS ambiguous_rows
  FROM window_rows
)
SELECT
  'contract_and_semantics' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) < @observe_end THEN 'WAIT'
    WHEN total_rows > 0 AND invalid_rows = 0 AND ambiguous_rows = 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  total_rows,
  invalid_rows,
  ambiguous_rows
FROM contract_check;

-- Every v1 consume row must identify the final execution source and resolve an
-- immutable pricing snapshot. Channel and subscription ids are checked locally
-- but are not printed by this report.
WITH evidence_check AS (
  SELECT
    COUNT(*) AS total_rows,
    COALESCE(SUM(
      l.source_kind NOT IN ('channel', 'subscription')
      OR l.upstream_model_id = ''
      OR (l.source_kind = 'channel' AND l.channel_id <= 0)
      OR (l.source_kind = 'subscription' AND l.subscription_account_id <= 0)
    ), 0) AS missing_source_rows,
    COALESCE(SUM(l.pricing_config_hash = ''), 0) AS missing_hash_rows,
    COALESCE(SUM(s.config_hash IS NULL), 0) AS unresolved_snapshot_rows
  FROM oneapi_billing.billing_ledgers l
  LEFT JOIN oneapi_billing.billing_pricing_snapshots s
    ON s.config_hash = l.pricing_config_hash
  WHERE l.type = 'consume'
    AND l.created_at >= @observe_start
    AND l.created_at < @observe_end
)
SELECT
  'source_and_pricing_evidence' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) < @observe_end THEN 'WAIT'
    WHEN total_rows > 0
      AND missing_source_rows = 0
      AND missing_hash_rows = 0
      AND unresolved_snapshot_rows = 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  total_rows,
  missing_source_rows,
  missing_hash_rows,
  unresolved_snapshot_rows
FROM evidence_check;

-- Idempotency gate: no empty/duplicate key, no ledger without claim, and no
-- claim without ledger for a consume row in this fixed window.
WITH window_ledgers AS (
  SELECT id, ledger_dedupe_key
  FROM oneapi_billing.billing_ledgers
  WHERE type = 'consume'
    AND created_at >= @observe_start
    AND created_at < @observe_end
), dedupe_check AS (
  SELECT
    (SELECT COUNT(*) FROM window_ledgers) AS total_rows,
    (SELECT COUNT(*) FROM window_ledgers WHERE ledger_dedupe_key = '') AS empty_keys,
    (
      SELECT COUNT(*)
      FROM (
        SELECT ledger_dedupe_key
        FROM window_ledgers
        WHERE ledger_dedupe_key <> ''
        GROUP BY ledger_dedupe_key
        HAVING COUNT(*) > 1
      ) duplicate_groups
    ) AS duplicate_key_groups,
    (
      SELECT COUNT(*)
      FROM window_ledgers l
      LEFT JOIN oneapi_billing.billing_ledger_dedupe_claims c
        ON c.ledger_dedupe_key = l.ledger_dedupe_key
      WHERE c.ledger_dedupe_key IS NULL
    ) AS ledgers_without_claim,
    (
      SELECT COUNT(*)
      FROM oneapi_billing.billing_ledger_dedupe_claims c
      LEFT JOIN oneapi_billing.billing_ledgers l
        ON l.ledger_dedupe_key = c.ledger_dedupe_key
      WHERE l.ledger_dedupe_key IS NULL
        AND c.created_at >= @observe_start
        AND c.created_at < @observe_end
    ) AS claims_without_ledger
)
SELECT
  'ledger_idempotency' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) < @observe_end THEN 'WAIT'
    WHEN total_rows > 0
      AND empty_keys = 0
      AND duplicate_key_groups = 0
      AND ledgers_without_claim = 0
      AND claims_without_ledger = 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  total_rows,
  empty_keys,
  duplicate_key_groups,
  ledgers_without_claim,
  claims_without_ledger
FROM dedupe_check;

-- Preserve the raw cost-mismatch count, then isolate the known step-explore
-- controlled cohort. Only natural-traffic mismatch is a charge blocker.
WITH mismatch_check AS (
  SELECT
    COUNT(*) AS total_rows,
    COALESCE(SUM(
      ABS(prompt_cost + completion_cost + cache_read_cost
        + cache_creation_5m_cost + cache_creation_1h_cost) <> ABS(amount)
    ), 0) AS raw_mismatch_rows,
    COALESCE(SUM(
      upstream_model_id = @controlled_test_model
      AND ABS(prompt_cost + completion_cost + cache_read_cost
        + cache_creation_5m_cost + cache_creation_1h_cost) <> ABS(amount)
    ), 0) AS controlled_mismatch_rows,
    COALESCE(SUM(
      upstream_model_id <> @controlled_test_model
      AND ABS(prompt_cost + completion_cost + cache_read_cost
        + cache_creation_5m_cost + cache_creation_1h_cost) <> ABS(amount)
    ), 0) AS natural_mismatch_rows
  FROM oneapi_billing.billing_ledgers
  WHERE type = 'consume'
    AND created_at >= @observe_start
    AND created_at < @observe_end
)
SELECT
  'persisted_cost_arithmetic' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) < @observe_end THEN 'WAIT'
    WHEN total_rows > 0 AND natural_mismatch_rows = 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  total_rows,
  raw_mismatch_rows,
  controlled_mismatch_rows,
  natural_mismatch_rows
FROM mismatch_check;

-- Rebuild canonical cost from the five canonical buckets and the frozen
-- snapshot. This mirrors AmountScale=10000 and per-bucket math.Round for
-- positive values. Snapshot cache_creation_mode decides whether creation
-- buckets participate in the final user cost.
WITH rebuilt AS (
  SELECT
    l.source_kind,
    l.usage_protocol,
    l.usage_semantics,
    l.upstream_model_id,
    (
      ROUND(l.uncached_input_tokens * s.input_price * s.group_ratio * 10000)
      + ROUND(l.cache_read_tokens * s.cache_read_price * s.group_ratio * 10000)
      + ROUND(l.completion_tokens * s.output_price * s.group_ratio * 10000)
      + CASE WHEN s.cache_creation_mode = 'charge' THEN
          ROUND(l.cache_creation_5m_tokens * s.cache_creation_5m_price
            * s.group_ratio * 10000)
          + ROUND(l.cache_creation_1h_tokens * s.cache_creation_1h_price
            * s.group_ratio * 10000)
        ELSE 0 END
    ) - ABS(l.amount) AS delta
  FROM oneapi_billing.billing_ledgers l
  JOIN oneapi_billing.billing_pricing_snapshots s
    ON s.config_hash = l.pricing_config_hash
  WHERE l.type = 'consume'
    AND l.created_at >= @observe_start
    AND l.created_at < @observe_end
    AND l.usage_contract_version = 1
    AND l.usage_parse_status IN ('verified', 'estimated')
), delta_check AS (
  SELECT
    COALESCE(SUM(
      upstream_model_id <> @controlled_test_model
      AND delta <> 0
      AND NOT (
        (source_kind = 'subscription'
          AND usage_protocol = 'responses'
          AND usage_semantics = 'openai_subset'
          AND delta < 0)
        OR
        (source_kind = 'channel'
          AND usage_protocol = 'anthropic_messages'
          AND usage_semantics = 'anthropic_exclusive'
          AND delta > 0)
      )
    ), 0) AS unexplained_natural_delta_rows
  FROM rebuilt
)
SELECT
  'natural_delta_explanations' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) < @observe_end THEN 'WAIT'
    WHEN unexplained_natural_delta_rows = 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  unexplained_natural_delta_rows
FROM delta_check;

-- Anonymous delta distribution. No request, user, channel, subscription,
-- token, or amount values leave the database.
WITH rebuilt AS (
  SELECT
    l.source_kind,
    l.usage_protocol,
    l.usage_semantics,
    l.upstream_model_id,
    (
      ROUND(l.uncached_input_tokens * s.input_price * s.group_ratio * 10000)
      + ROUND(l.cache_read_tokens * s.cache_read_price * s.group_ratio * 10000)
      + ROUND(l.completion_tokens * s.output_price * s.group_ratio * 10000)
      + CASE WHEN s.cache_creation_mode = 'charge' THEN
          ROUND(l.cache_creation_5m_tokens * s.cache_creation_5m_price
            * s.group_ratio * 10000)
          + ROUND(l.cache_creation_1h_tokens * s.cache_creation_1h_price
            * s.group_ratio * 10000)
        ELSE 0 END
    ) - ABS(l.amount) AS delta
  FROM oneapi_billing.billing_ledgers l
  JOIN oneapi_billing.billing_pricing_snapshots s
    ON s.config_hash = l.pricing_config_hash
  WHERE l.type = 'consume'
    AND l.created_at >= @observe_start
    AND l.created_at < @observe_end
    AND l.usage_contract_version = 1
    AND l.usage_parse_status IN ('verified', 'estimated')
)
SELECT
  CASE
    WHEN upstream_model_id = @controlled_test_model THEN 'controlled_test'
    ELSE 'natural'
  END AS cohort,
  source_kind,
  usage_protocol,
  usage_semantics,
  CASE WHEN delta < 0 THEN 'negative' ELSE 'positive' END AS direction,
  COUNT(*) AS rows_n
FROM rebuilt
WHERE delta <> 0
GROUP BY cohort, source_kind, usage_protocol, usage_semantics, direction
ORDER BY cohort, source_kind, usage_protocol, usage_semantics, direction;

-- K3/Kimi are operated under fixed monthly subscription plans. Their
-- per-request upstream_cost values are internal configured allocations, not a
-- vendor invoice. Verify that every natural delta row stays on the subscription
-- cost basis, but do not treat this as proof of real marginal supplier cost.
WITH rebuilt AS (
  SELECT
    l.cost_source,
    l.upstream_cost,
    l.upstream_model_id,
    (
      ROUND(l.uncached_input_tokens * s.input_price * s.group_ratio * 10000)
      + ROUND(l.cache_read_tokens * s.cache_read_price * s.group_ratio * 10000)
      + ROUND(l.completion_tokens * s.output_price * s.group_ratio * 10000)
      + CASE WHEN s.cache_creation_mode = 'charge' THEN
          ROUND(l.cache_creation_5m_tokens * s.cache_creation_5m_price
            * s.group_ratio * 10000)
          + ROUND(l.cache_creation_1h_tokens * s.cache_creation_1h_price
            * s.group_ratio * 10000)
        ELSE 0 END
    ) - ABS(l.amount) AS delta
  FROM oneapi_billing.billing_ledgers l
  JOIN oneapi_billing.billing_pricing_snapshots s
    ON s.config_hash = l.pricing_config_hash
  WHERE l.type = 'consume'
    AND l.created_at >= @observe_start
    AND l.created_at < @observe_end
    AND l.usage_contract_version = 1
    AND l.usage_parse_status IN ('verified', 'estimated')
), fixed_plan_check AS (
  SELECT
    COALESCE(SUM(
      upstream_model_id <> @controlled_test_model AND delta <> 0
    ), 0) AS natural_delta_rows,
    COALESCE(SUM(
      upstream_model_id <> @controlled_test_model
      AND delta <> 0
      AND cost_source <> 'subscription'
    ), 0) AS non_subscription_cost_basis_rows,
    COALESCE(SUM(
      upstream_model_id <> @controlled_test_model
      AND delta <> 0
      AND upstream_cost > 0
    ), 0) AS internally_allocated_cost_rows
  FROM rebuilt
)
SELECT
  'fixed_plan_cost_basis' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) < @observe_end THEN 'WAIT'
    WHEN natural_delta_rows > 0 AND non_subscription_cost_basis_rows = 0
      THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  natural_delta_rows,
  non_subscription_cost_basis_rows,
  internally_allocated_cost_rows
FROM fixed_plan_check;

-- Billing and log do not share a safe one-to-one request key. Compare their
-- 24 common usage/source/latency fields as multisets instead. Only aggregate
-- counts and the number of differing groups are printed.
WITH combined AS (
  SELECT
    CAST(user_id AS CHAR) AS user_id,
    token_name, model_name, quota,
    prompt_tokens, completion_tokens, cache_read_tokens,
    cache_creation_5m_tokens, cache_creation_1h_tokens,
    uncached_input_tokens, reported_prompt_tokens, reported_total_tokens,
    billable_total_tokens, usage_semantics, usage_protocol,
    usage_field_shape, usage_parse_status, usage_contract_version,
    canonical_present, usage_decision_reason, channel_id,
    subscription_account_id, elapsed_time, is_stream,
    1 AS billing_n, 0 AS log_n
  FROM oneapi_billing.billing_ledgers
  WHERE type = 'consume'
    AND created_at >= @observe_start
    AND created_at < @observe_end

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
    AND created_at >= FLOOR(UNIX_TIMESTAMP(@observe_start))
    AND created_at < CEIL(UNIX_TIMESTAMP(@observe_end))
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
    SUM(billing_n) AS billing_n,
    SUM(log_n) AS log_n
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
  SELECT
    COALESCE(SUM(billing_n), 0) AS billing_rows,
    COALESCE(SUM(log_n), 0) AS log_rows,
    COALESCE(SUM(billing_n <> log_n), 0) AS differing_groups
  FROM grouped
)
SELECT
  'billing_log_multiset' AS check_name,
  CASE
    WHEN UTC_TIMESTAMP(3) < @observe_end THEN 'WAIT'
    WHEN billing_rows > 0
      AND billing_rows = log_rows
      AND differing_groups = 0 THEN 'PASS'
    ELSE 'FAIL'
  END AS status,
  billing_rows,
  log_rows,
  differing_groups
FROM multiset_check;
