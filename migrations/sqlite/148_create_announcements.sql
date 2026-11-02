-- Admin-owned announcements; read receipts are per user and content revision.
CREATE TABLE IF NOT EXISTS announcements (
 id VARCHAR(36) PRIMARY KEY,
 revision BIGINT NOT NULL,
 title VARCHAR(480) NOT NULL,
 body TEXT NOT NULL,
 audience VARCHAR(16) NOT NULL,
 targets TEXT NOT NULL,
 status VARCHAR(16) NOT NULL,
 publish_at BIGINT NOT NULL,
 expires_at BIGINT NOT NULL DEFAULT 0,
 updated_at BIGINT NOT NULL
);
CREATE INDEX idx_announcements_publication ON announcements (status, publish_at);
CREATE TABLE IF NOT EXISTS announcement_targets (
 announcement_id VARCHAR(36) NOT NULL,
 target VARCHAR(20) NOT NULL,
 PRIMARY KEY (announcement_id, target)
);
CREATE TABLE IF NOT EXISTS announcement_reads (
 announcement_id VARCHAR(36) NOT NULL,
 user_id BIGINT NOT NULL,
 revision BIGINT NOT NULL,
 read_at BIGINT NOT NULL,
 PRIMARY KEY (announcement_id, user_id, revision)
);
CREATE TABLE IF NOT EXISTS announcement_events (
 id VARCHAR(36) PRIMARY KEY,
 announcement_id VARCHAR(36) NOT NULL,
 revision BIGINT NOT NULL,
 actor VARCHAR(80) NOT NULL,
 reason VARCHAR(960) NOT NULL,
 at BIGINT NOT NULL
);
