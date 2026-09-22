-- Additive execution audit fields. Historical identities remain unknown.
ALTER TABLE billing_reservations ADD COLUMN root_request_id VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE billing_reservations ADD COLUMN attempt_number INTEGER NOT NULL DEFAULT 0;
ALTER TABLE billing_reservations ADD COLUMN source_kind VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE billing_reservations ADD COLUMN upstream_model_id VARCHAR(255) NOT NULL DEFAULT '';
CREATE INDEX idx_reservation_user_root ON billing_reservations (user_id, root_request_id, attempt_number);
