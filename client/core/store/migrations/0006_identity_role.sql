-- 0006_identity_role.sql — identity 表新增身份角色列（§9.1 方案 A）。
-- role 区分管理员（admin）与普通用户（member），用于「是否首次」按模式独立判断：
-- 管理员模式只看 admin 身份，普通用户模式只看 member 身份，避免管理员部署后
-- 普通用户模式被误判为「已注册」。
-- 迁移只增不改：本文件只 ADD COLUMN，不改动已有列。

ALTER TABLE identity ADD COLUMN role TEXT NOT NULL DEFAULT '';
