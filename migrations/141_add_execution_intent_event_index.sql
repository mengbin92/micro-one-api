-- Bound owner event reads by intent and the stable event-ID cursor.
CREATE INDEX idx_execution_intent_event ON upstream_execution_evidence (intent_id, id);
