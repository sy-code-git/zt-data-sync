-- 0003_ca.sql — 自签 CA 证书路径配置（§8.3 证书 pinning）。
-- 在 app_config 单行表上新增一列：
--   ca_path  自签 CA 证书（PEM）的本地路径；空串 = 走系统默认验证（公网受信证书）
-- 迁移只增不改：本文件只 ADD COLUMN，不改动已有列。

ALTER TABLE app_config ADD COLUMN ca_path TEXT NOT NULL DEFAULT '';
