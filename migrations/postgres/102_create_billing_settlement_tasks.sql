CREATE TABLE IF NOT EXISTS billing_settlement_tasks (
  id bigserial PRIMARY KEY,
  reservation_id varchar(64) NOT NULL UNIQUE,
  payload text NOT NULL,
  status varchar(16) NOT NULL DEFAULT 'pending',
  attempts integer NOT NULL DEFAULT 0,
  last_error text,
  next_retry_at bigint NOT NULL DEFAULT 0,
  created_at bigint NOT NULL DEFAULT 0,
  updated_at bigint NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_billing_settlement_tasks_pending ON billing_settlement_tasks(status, next_retry_at);
