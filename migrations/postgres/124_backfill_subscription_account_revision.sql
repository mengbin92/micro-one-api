-- Existing accounts from migration 108 need a positive revision for IAM writes.
UPDATE subscription_accounts SET credential_revision = 1 WHERE credential_revision = 0;
