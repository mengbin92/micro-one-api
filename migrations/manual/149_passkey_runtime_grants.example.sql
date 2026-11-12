-- Operator-reviewed template; replace the existing runtime account/schema.
-- Apply migration 149 with the migration owner before granting runtime access.
-- MySQL: sign_count/last_used_at advance on every login; DELETE on revocation.
GRANT SELECT, INSERT, UPDATE, DELETE ON oneapi_identity.identity_passkey TO 'oneapi_identity'@'%';

-- PostgreSQL equivalent (use the actual Identity search_path and runtime role):
-- GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE oneapi_identity.identity_passkey TO oneapi_identity;
-- SQLite has file-level permissions, with no database GRANT statements.
