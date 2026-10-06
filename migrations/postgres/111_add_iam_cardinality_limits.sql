-- Platform context limits count distinct effective roles (including inheritance).
-- NULL is unlimited; zero/negative values are never a permissive fallback.
ALTER TABLE iam_policy_state ADD COLUMN max_roles_per_user BIGINT NULL CHECK (max_roles_per_user IS NULL OR max_roles_per_user > 0);
ALTER TABLE iam_policy_state ADD COLUMN max_roles_per_session BIGINT NULL CHECK (max_roles_per_session IS NULL OR max_roles_per_session > 0);
