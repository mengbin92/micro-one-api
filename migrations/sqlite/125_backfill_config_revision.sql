-- Existing config keys from migration 105 need a positive revision for IAM deletes.
UPDATE configs SET revision = 1 WHERE revision = 0;
