-- 0005_regsecret.sql — 注册凭证密钥（PB_REG_SECRET）本地加密存储（§4.4 attestation）。
-- 管理员首次部署（bootstrap）时输入 REG_SECRET，用 KEK 派生密钥加密后存本地，
-- 后续「开户（create-user）」时取出计算成员 attestation。
-- 迁移只增不改：本文件只 ADD COLUMN，不改动已有列。

ALTER TABLE app_config ADD COLUMN reg_secret_enc BLOB;
