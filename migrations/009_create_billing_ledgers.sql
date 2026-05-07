-- Create billing_ledgers table
CREATE TABLE IF NOT EXISTS `billing_ledgers` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `user_id` varchar(64) NOT NULL COMMENT '用户 ID',
  `amount` bigint NOT NULL COMMENT '变更金额',
  `balance_after` bigint NOT NULL COMMENT '变更后余额',
  `type` varchar(32) NOT NULL COMMENT '类型：consume/recharge/refund/redeem',
  `reference_id` varchar(64) DEFAULT NULL COMMENT '关联 ID',
  `remark` text COMMENT '备注',
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_user_id` (`user_id`),
  KEY `idx_type` (`type`),
  KEY `idx_created_at` (`created_at`),
  KEY `idx_reference_id` (`reference_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账务流水表';
