-- Historical billing-ledger evidence audit (read-only; v0.27).
--
-- This query never updates billing_ledgers, claims, balances, prices, or
-- external billing systems. It deliberately classifies old rows instead of
-- inferring missing vendor evidence from current prices:
--   verified  = v1 verified usage + valid source + immutable pricing snapshot
--   candidate = v1 usage is structurally usable, but some evidence is absent
--   unknown   = legacy/ambiguous usage or an invalid/incomplete record
--
-- Set either variable to a UTC DATETIME(3) to restrict the audit. NULL means
-- all consume rows. The final ORDER BY makes exports deterministic.

SET @audit_start = NULL;
SET @audit_end = NULL;

SELECT
  l.id AS ledger_id,
  l.created_at,
  l.reference_id,
  l.amount AS ledger_amount,
  l.source_kind,
  l.channel_id,
  l.subscription_account_id,
  l.model_name,
  l.upstream_model_id,
  l.usage_protocol,
  l.usage_semantics,
  l.usage_field_shape,
  l.usage_parse_status,
  l.usage_contract_version,
  l.canonical_present,
  l.usage_decision_reason,
  l.uncached_input_tokens,
  l.cache_read_tokens,
  l.cache_creation_5m_tokens,
  l.cache_creation_1h_tokens,
  l.completion_tokens,
  l.prompt_cost,
  l.cache_read_cost,
  l.cache_creation_5m_cost,
  l.cache_creation_1h_cost,
  l.completion_cost,
  l.upstream_cost,
  l.cost_audit_status,
  NULLIF(l.pricing_config_hash, '') AS pricing_config_hash,
  CASE
    WHEN l.usage_contract_version = 1
      AND l.usage_parse_status = 'verified'
      AND l.canonical_present = 1
      AND l.source_kind IN ('channel', 'subscription')
      AND l.upstream_model_id <> ''
      AND l.pricing_config_hash <> ''
      AND s.config_hash IS NOT NULL
      THEN 'verified'
    WHEN l.usage_contract_version = 1
      AND l.usage_parse_status IN ('verified', 'estimated')
      AND l.canonical_present = 1
      THEN 'candidate'
    ELSE 'unknown'
  END AS audit_class,
  CASE
    WHEN l.usage_parse_status = 'legacy' THEN 'legacy_usage'
    WHEN l.usage_parse_status = 'ambiguous' THEN 'ambiguous_usage'
    WHEN COALESCE(l.usage_contract_version, 0) <> 1 THEN 'unsupported_usage_contract'
    WHEN COALESCE(l.canonical_present, 0) <> 1 THEN 'missing_canonical_usage'
    WHEN l.source_kind NOT IN ('channel', 'subscription')
      OR l.upstream_model_id = '' THEN 'missing_source'
    WHEN l.pricing_config_hash = '' OR s.config_hash IS NULL
      THEN 'missing_pricing_snapshot'
    WHEN l.usage_parse_status = 'estimated' THEN 'estimated_usage'
    WHEN l.usage_parse_status = 'verified' THEN 'verified_usage+pricing_snapshot'
    ELSE 'insufficient_evidence'
  END AS evidence_source,
  CASE
    WHEN COALESCE(l.usage_contract_version, 0) <> 1
      OR COALESCE(l.usage_parse_status, '') NOT IN ('verified', 'estimated')
      OR COALESCE(l.canonical_present, 0) <> 1
      OR s.config_hash IS NULL THEN NULL
    ELSE (
      ROUND(l.uncached_input_tokens * s.input_price * s.group_ratio * 10000)
      + ROUND(l.cache_read_tokens * s.cache_read_price * s.group_ratio * 10000)
      + ROUND(l.completion_tokens * s.output_price * s.group_ratio * 10000)
      + CASE WHEN s.cache_creation_mode = 'charge' THEN
          ROUND(l.cache_creation_5m_tokens * s.cache_creation_5m_price * s.group_ratio * 10000)
          + ROUND(l.cache_creation_1h_tokens * s.cache_creation_1h_price * s.group_ratio * 10000)
        ELSE 0 END
      - ABS(l.amount)
    )
  END AS candidate_delta_quota,
  CASE WHEN s.config_hash IS NULL THEN NULL ELSE s.cache_creation_mode END AS snapshot_cache_creation_mode
FROM oneapi_billing.billing_ledgers AS l
LEFT JOIN oneapi_billing.billing_pricing_snapshots AS s
  ON s.config_hash = l.pricing_config_hash
WHERE l.type = 'consume'
  AND (@audit_start IS NULL OR l.created_at >= @audit_start)
  AND (@audit_end IS NULL OR l.created_at < @audit_end)
ORDER BY l.created_at, l.id;
