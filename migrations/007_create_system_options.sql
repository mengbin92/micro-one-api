CREATE TABLE IF NOT EXISTS `system_options` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `option_key` varchar(128) NOT NULL COMMENT '配置键',
  `option_value` text NOT NULL COMMENT '配置值（JSON）',
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_option_key` (`option_key`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='系统配置表';
