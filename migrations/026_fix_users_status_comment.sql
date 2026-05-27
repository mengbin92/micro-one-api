-- Correct the stale comment on users.status. The original baseline (000)
-- documented '0=active, 1=disabled', but the code constants in
-- internal/identity/biz/auth.go define UserStatusEnabled=1 and
-- UserStatusDisabled=2 (0 is reserved/unspecified). This ALTER only updates
-- the column COMMENT; column type, default, and existing row values are
-- unchanged.
ALTER TABLE `users`
  MODIFY COLUMN `status` int DEFAULT 0 COMMENT '0=unspecified, 1=enabled, 2=disabled';
