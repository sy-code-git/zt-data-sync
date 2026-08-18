-- 0002_autounlock.sql — 自动解锁（Windows DPAPI）配置扩展（§9.1）。
-- 在 app_config 单行表上新增三列：
--   keyfile_path       已导入 keyfile 的绝对路径（自动解锁免口令定位 keyfile 用）
--   autounlock_enabled 是否开启自动解锁（0/1）
--   autounlock_kek     DPAPI 保护的 KEK blob（仅 Windows 可解密；非 Windows 恒为 NULL）
-- 迁移只增不改：本文件只 ADD COLUMN，不改动已有列。

ALTER TABLE app_config ADD COLUMN keyfile_path TEXT NOT NULL DEFAULT '';
ALTER TABLE app_config ADD COLUMN autounlock_enabled INTEGER NOT NULL DEFAULT 0;
ALTER TABLE app_config ADD COLUMN autounlock_kek BLOB;
