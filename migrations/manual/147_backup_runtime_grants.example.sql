-- Operator-reviewed template only. Not an automatic migration. Replace
-- account/host/schema names with the deployment's existing account contract.
-- Never put passwords or CREATE USER statements containing secrets here.

-- Admin control plane; existing IAM resource_write_audits grants remain needed.
GRANT SELECT, UPDATE ON oneapi_admin.backup_policies TO 'oneapi_admin'@'%';
GRANT SELECT, INSERT, UPDATE ON oneapi_admin.backup_jobs TO 'oneapi_admin'@'%';
GRANT SELECT, INSERT ON oneapi_admin.backup_events TO 'oneapi_admin'@'%';
GRANT SELECT ON oneapi_admin.backup_worker_state TO 'oneapi_admin'@'%';

-- Private host metadata worker (separately provisioned account).
GRANT SELECT ON oneapi_admin.backup_policies TO 'backup_worker'@'%';
GRANT SELECT, INSERT, UPDATE ON oneapi_admin.backup_jobs TO 'backup_worker'@'%';
GRANT INSERT ON oneapi_admin.backup_events TO 'backup_worker'@'%';
GRANT SELECT, UPDATE ON oneapi_admin.backup_worker_state TO 'backup_worker'@'%';

-- Private snapshot lock/instance inventory account. The actual mysqldump runs
-- through the existing private mysql Compose container's root credential.
GRANT SELECT, SHOW DATABASES, RELOAD ON *.* TO 'backup_snapshot'@'%';
