-- Core tables for micro-one-api services

CREATE TABLE IF NOT EXISTS `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `username` varchar(64) NOT NULL,
  `display_name` varchar(128) DEFAULT '',
  `email` varchar(128) DEFAULT '',
  `group` varchar(32) DEFAULT 'default',
  `status` int DEFAULT 0 COMMENT '0=active, 1=disabled',
  `password_hash` varchar(256) DEFAULT '',
  `oauth_provider` varchar(32) DEFAULT '',
  `oauth_id` varchar(128) DEFAULT '',
  `quota` bigint DEFAULT 0,
  `used_quota` bigint DEFAULT 0,
  `request_count` bigint DEFAULT 0,
  `frozen_quota` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_username` (`username`),
  KEY `idx_oauth_provider` (`oauth_provider`),
  KEY `idx_oauth_id` (`oauth_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `tokens` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `key` varchar(64) NOT NULL,
  `status` int DEFAULT 0 COMMENT '0=enabled, 1=disabled',
  `expired_time` bigint DEFAULT 0,
  `remain_quota` bigint DEFAULT 0,
  `unlimited_quota` tinyint(1) DEFAULT 0,
  `models` text DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_user_id` (`user_id`),
  KEY `idx_key` (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `channels` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `type` int DEFAULT 0,
  `key` text DEFAULT NULL,
  `status` int DEFAULT 0 COMMENT '0=enabled, 1=disabled',
  `name` varchar(128) DEFAULT '',
  `base_url` varchar(256) DEFAULT NULL,
  `models` text DEFAULT NULL,
  `group` varchar(32) DEFAULT 'default',
  `priority` bigint DEFAULT 0,
  `config` text DEFAULT NULL,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `abilities` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `group` varchar(32) NOT NULL DEFAULT 'default',
  `model` varchar(128) NOT NULL,
  `channel_id` bigint NOT NULL,
  `enabled` tinyint(1) DEFAULT 1,
  `priority` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_channel_id` (`channel_id`),
  KEY `idx_model_group` (`model`, `group`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `configs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `namespace` varchar(64) NOT NULL DEFAULT '',
  `key` varchar(128) NOT NULL,
  `value` text NOT NULL,
  `comment` varchar(256) DEFAULT '',
  `updated_at` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_namespace` (`namespace`),
  KEY `idx_key` (`key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `level` varchar(16) NOT NULL DEFAULT 'info',
  `message` text NOT NULL,
  `source` varchar(64) DEFAULT '',
  `request_id` varchar(64) DEFAULT '',
  `user_id` bigint DEFAULT 0,
  `created_at` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_level` (`level`),
  KEY `idx_source` (`source`),
  KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `notifications` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `type` varchar(32) NOT NULL DEFAULT '',
  `recipient` varchar(128) NOT NULL DEFAULT '',
  `subject` varchar(256) DEFAULT '',
  `content` text DEFAULT NULL,
  `status` varchar(16) NOT NULL DEFAULT 'pending',
  `retry_count` int DEFAULT 0,
  `created_at` bigint DEFAULT 0,
  `sent_at` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_type` (`type`),
  KEY `idx_status` (`status`),
  KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `health_checks` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `service_name` varchar(64) NOT NULL,
  `status` varchar(16) NOT NULL DEFAULT 'unknown',
  `response_time` bigint DEFAULT 0,
  `checked_at` bigint DEFAULT 0,
  PRIMARY KEY (`id`),
  KEY `idx_service_name` (`service_name`),
  KEY `idx_checked_at` (`checked_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS `alert_rules` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `name` varchar(128) NOT NULL DEFAULT '',
  `service_name` varchar(64) NOT NULL DEFAULT '',
  `metric` varchar(64) NOT NULL DEFAULT '',
  `threshold` double DEFAULT 0,
  `operator` varchar(8) NOT NULL DEFAULT '>',
  `duration` int DEFAULT 0,
  `enabled` tinyint(1) DEFAULT 1,
  `created_at` bigint DEFAULT 0,
  PRIMARY KEY (`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Seed data: default admin user and test token
-- status=1 means enabled (matches biz.TokenStatusEnabled=1, biz.UserStatusEnabled=1)
INSERT INTO `users` (`id`, `username`, `display_name`, `email`, `group`, `status`, `password_hash`, `quota`)
VALUES (1, 'admin', 'Administrator', 'admin@example.com', 'default', 1, '', 1000000)
ON DUPLICATE KEY UPDATE `username`=`username`;

INSERT INTO `tokens` (`id`, `user_id`, `key`, `status`, `expired_time`, `remain_quota`, `unlimited_quota`)
VALUES (1, 1, 'sk-test-token-001', 1, 0, 1000000, 1)
ON DUPLICATE KEY UPDATE `key`=`key`;

-- Seed data: a test channel (OpenAI-compatible, status=1 means enabled)
INSERT INTO `channels` (`id`, `type`, `key`, `status`, `name`, `base_url`, `models`, `group`, `priority`)
VALUES (1, 1, 'sk-placeholder', 1, 'test-openai', 'https://api.openai.com', 'gpt-3.5-turbo,gpt-4', 'default', 0)
ON DUPLICATE KEY UPDATE `name`=`name`;

INSERT INTO `abilities` (`group`, `model`, `channel_id`, `enabled`, `priority`)
VALUES ('default', 'gpt-3.5-turbo', 1, 1, 0),
       ('default', 'gpt-4', 1, 1, 0)
ON DUPLICATE KEY UPDATE `model`=`model`;
