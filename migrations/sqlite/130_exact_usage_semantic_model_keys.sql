-- SQLite's existing TEXT/BINARY column already preserves exact spelling without a length cap.
UPDATE usage_semantic_source_blocks SET upstream_model_id = upstream_model_id WHERE 1 = 0;
