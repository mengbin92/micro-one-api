CREATE TABLE upstream_cost_resources (id INTEGER PRIMARY KEY AUTOINCREMENT, cost_key TEXT NOT NULL UNIQUE CHECK(length(cost_key) <= 512));
