-- Input/output modalities are independent of the model's service type.
-- JSON arrays keep the storage shape aligned with capabilities and tags.

ALTER TABLE models ADD COLUMN input_modalities VARCHAR(255) NOT NULL DEFAULT '[]';
ALTER TABLE models ADD COLUMN output_modalities VARCHAR(255) NOT NULL DEFAULT '[]';
