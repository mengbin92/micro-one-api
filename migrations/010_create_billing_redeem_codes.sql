-- Create billing_redeem_codes table
CREATE TABLE IF NOT EXISTS `billing_redeem_codes` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `code` varchar(64) NOT NULL COMMENT '兑换码',
  `name` varchar(100) DEFAULT NULL COMMENT '兑换码名称',
  `amount` bigint NOT NULL COMMENT '兑换金额',
  `count` int NOT NULL COMMENT '剩余次数',
  `status` tinyint NOT NULL DEFAULT '1' COMMENT '状态：0-禁用 1-启用 2-已使用',
  `created_by` varchar(64) DEFAULT NULL COMMENT '创建人',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_code` (`code`),
  KEY `idx_status` (`status`),
  KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='兑换码表';
