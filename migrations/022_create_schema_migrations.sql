-- Track applied migrations. The migrate runner inserts one row per
-- migration file it executes (basename without the .sql suffix as version).
-- See internal/pkg/migrate/runner.go.

CREATE TABLE IF NOT EXISTS `schema_migrations` (
  `version` varchar(255) NOT NULL,
  `applied_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
