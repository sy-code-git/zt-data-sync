-- 0002_username.sql — users 表新增工号登录名（身份体系重构：UUID user_id → 工号登录）
-- username = 工号（唯一、不可改、登录标识）；name 仍为显示名（允许重名）。
-- 存量用户（bootstrap/admin、create-user 建的）无 username，允许 NULL；后续开户必填。
-- SQLite 的 ALTER TABLE ADD COLUMN 不支持 UNIQUE 约束，用 partial unique index 实现唯一。

ALTER TABLE users ADD COLUMN username TEXT;

CREATE UNIQUE INDEX idx_users_username ON users(username) WHERE username IS NOT NULL;
