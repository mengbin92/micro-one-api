-- Independent model management system (方案B).
-- Centralises model metadata, aliases, channel/subscription mappings and
-- usage stats. The tables are owned by channel-service (schema isolation
-- Phase 2.4) because models derive from channels and subscription accounts.
-- See docs/model-management-design.md for the full design.

-- ── models: model registry ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS `models` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `model_id` varchar(255) NOT NULL COMMENT 'unique model identifier, e.g. gpt-4o',
  `display_name` varchar(255) NOT NULL COMMENT 'human-readable name',
  `description` text,
  `provider` varchar(100) NOT NULL DEFAULT '' COMMENT 'openai, anthropic, zhipu, …',
  `model_type` varchar(50) NOT NULL DEFAULT 'chat' COMMENT 'chat, completion, embedding, image',
  `context_window` int NOT NULL DEFAULT 0,
  `pricing_input` decimal(10,6) NOT NULL DEFAULT 0 COMMENT 'input price per 1K tokens',
  `pricing_output` decimal(10,6) NOT NULL DEFAULT 0 COMMENT 'output price per 1K tokens',
  `status` tinyint NOT NULL DEFAULT 1 COMMENT '0=disabled, 1=enabled, 2=testing',
  `is_public` tinyint(1) NOT NULL DEFAULT 1 COMMENT 'visible to end users',
  `capabilities` text COMMENT 'JSON array: ["vision","function_calling","streaming"]',
  `tags` text COMMENT 'JSON array: ["large-context","fast"]',
  `category` varchar(100) NOT NULL DEFAULT '' COMMENT 'large-language, image, audio',
  `tier` varchar(50) NOT NULL DEFAULT '' COMMENT 'entry, standard, premium',
  `metadata` text COMMENT 'JSON object for extension',
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_model_id` (`model_id`),
  KEY `idx_models_provider` (`provider`),
  KEY `idx_models_status` (`status`),
  KEY `idx_models_type` (`model_type`),
  KEY `idx_models_category` (`category`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='model registry';

-- ── model_aliases: alternative names that resolve to a model ────────────
CREATE TABLE IF NOT EXISTS `model_aliases` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `model_id` bigint NOT NULL,
  `alias` varchar(255) NOT NULL,
  `is_primary` tinyint(1) NOT NULL DEFAULT 0,
  `created_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_alias` (`alias`),
  KEY `idx_model_aliases_model_id` (`model_id`),
  CONSTRAINT `fk_aliases_model` FOREIGN KEY (`model_id`) REFERENCES `models` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='model alias table';

-- ── model_channel_mapping: which channels serve which models ────────────
CREATE TABLE IF NOT EXISTS `model_channel_mapping` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `channel_id` bigint NOT NULL,
  `model_id` bigint NOT NULL,
  `enabled` tinyint(1) NOT NULL DEFAULT 1,
  `priority` int NOT NULL DEFAULT 0,
  `config` text COMMENT 'JSON: channel-model-specific config',
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_channel_model` (`channel_id`, `model_id`),
  KEY `idx_mcm_channel_id` (`channel_id`),
  KEY `idx_mcm_model_id` (`model_id`),
  CONSTRAINT `fk_mcm_model` FOREIGN KEY (`model_id`) REFERENCES `models` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='channel-model mapping';

-- ── model_subscription_mapping: which subscription accounts serve which models ──
CREATE TABLE IF NOT EXISTS `model_subscription_mapping` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `subscription_account_id` bigint NOT NULL,
  `model_id` bigint NOT NULL,
  `group_name` varchar(100) NOT NULL DEFAULT 'default',
  `enabled` tinyint(1) NOT NULL DEFAULT 1,
  `priority` int NOT NULL DEFAULT 0,
  `created_at` bigint NOT NULL DEFAULT 0,
  `updated_at` bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_account_model_group` (`subscription_account_id`, `model_id`, `group_name`),
  KEY `idx_msm_account_id` (`subscription_account_id`),
  KEY `idx_msm_model_id` (`model_id`),
  KEY `idx_msm_group` (`group_name`),
  CONSTRAINT `fk_msm_model` FOREIGN KEY (`model_id`) REFERENCES `models` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='subscription-account-model mapping';

-- ── model_usage_stats: daily usage aggregation per model ────────────────
CREATE TABLE IF NOT EXISTS `model_usage_stats` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `model_id` bigint NOT NULL,
  `date` date NOT NULL,
  `request_count` int NOT NULL DEFAULT 0,
  `token_count` bigint NOT NULL DEFAULT 0,
  `error_count` int NOT NULL DEFAULT 0,
  `avg_latency` int NOT NULL DEFAULT 0 COMMENT 'average latency in ms',
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_model_date` (`model_id`, `date`),
  KEY `idx_mus_date` (`date`),
  CONSTRAINT `fk_mus_model` FOREIGN KEY (`model_id`) REFERENCES `models` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='model usage statistics';
