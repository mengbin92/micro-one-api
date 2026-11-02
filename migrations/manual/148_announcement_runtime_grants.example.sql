-- Operator-reviewed template; replace the existing runtime account/schema.
-- Apply migration 148 with the migration owner before granting runtime access.
-- MySQL: archive uses UPDATE; receipt dedupe renders ON DUPLICATE KEY UPDATE.
GRANT SELECT, INSERT, UPDATE ON oneapi_admin.announcements TO 'oneapi_admin'@'%';
GRANT SELECT, INSERT, DELETE ON oneapi_admin.announcement_targets TO 'oneapi_admin'@'%';
GRANT SELECT, INSERT, UPDATE ON oneapi_admin.announcement_reads TO 'oneapi_admin'@'%';
GRANT INSERT ON oneapi_admin.announcement_events TO 'oneapi_admin'@'%';

-- PostgreSQL equivalent (use the actual Admin search_path and runtime role):
-- GRANT SELECT, INSERT, UPDATE ON TABLE oneapi_admin.announcements TO oneapi_admin;
-- GRANT SELECT, INSERT, DELETE ON TABLE oneapi_admin.announcement_targets TO oneapi_admin;
-- GRANT SELECT, INSERT ON TABLE oneapi_admin.announcement_reads TO oneapi_admin;
-- GRANT INSERT ON TABLE oneapi_admin.announcement_events TO oneapi_admin;
-- SQLite has file-level permissions, with no database GRANT statements.
