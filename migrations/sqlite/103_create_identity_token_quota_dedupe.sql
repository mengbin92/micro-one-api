CREATE TABLE IF NOT EXISTS identity_token_quota_dedupe (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  reservation_id TEXT NOT NULL,
  user_id INTEGER NOT NULL,
  token_id INTEGER NOT NULL,
  amount INTEGER NOT NULL,
  remaining INTEGER NOT NULL,
  created_at INTEGER NOT NULL DEFAULT 0,
  UNIQUE (reservation_id, user_id, token_id)
);
