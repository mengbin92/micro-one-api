CREATE TABLE IF NOT EXISTS identity_token_quota_dedupe (
  id bigserial PRIMARY KEY,
  reservation_id varchar(64) NOT NULL,
  user_id bigint NOT NULL,
  token_id bigint NOT NULL,
  amount bigint NOT NULL,
  remaining bigint NOT NULL,
  created_at bigint NOT NULL DEFAULT 0,
  UNIQUE (reservation_id, user_id, token_id)
);
