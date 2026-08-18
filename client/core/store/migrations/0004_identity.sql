-- 0004_identity.sql — 本地身份表（方案 A：私钥加密存本地库，替代外部 keyfile 文件）
-- keyfile_blob = keyfile JSON（含 KDF salt/iter + SM4-GCM 加密的私钥 DER）
-- public_key  = base64(DER) 公钥（非敏感，导出给管理员开户用）

CREATE TABLE IF NOT EXISTS identity (
    id           INTEGER PRIMARY KEY CHECK (id = 1),
    username     TEXT NOT NULL,   -- 工号（登录标识，唯一、不可改）
    keyfile_blob BLOB NOT NULL,   -- 加密的 keyfile（口令保护私钥）
    public_key   TEXT NOT NULL    -- base64(DER) SM2 公钥
);
