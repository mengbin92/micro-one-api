-- Create billing_reservations table
CREATE TABLE IF NOT EXISTS `billing_reservations` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `reservation_id` varchar(64) NOT NULL COMMENT '预扣 ID',
  `user_id` varchar(64) NOT NULL COMMENT '用户 ID',
  `request_id` varchar(64) NOT NULL COMMENT '请求 ID',
  `amount` bigint NOT NULL COMMENT '预扣金额',
  `status` varchar(20) NOT NULL COMMENT '状态：reserved/committed/released/expired',
  `model` varchar(64) DEFAULT NULL COMMENT '使用的模型',
  `channel_id` varchar(64) DEFAULT NULL COMMENT '渠道 ID',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `expired_at` datetime(3) DEFAULT NULL COMMENT '过期时间',
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_reservation_id` (`reservation_id`),
  KEY `idx_user_id` (`user_id`),
  KEY `idx_request_id` (`request_id`),
  KEY `idx_status_expired` (`status`, `expired_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='配额预扣表';
