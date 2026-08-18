-- 0001_local.sql — 客户端本地存储 schema（§9.1 本地存储）。
-- 敏感列（token_enc/dek_enc/plaintext_cache_enc/base_enc）由 KEK 派生密钥加密后落库。

CREATE TABLE IF NOT EXISTS device_state (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    device_id  TEXT NOT NULL,
    token_enc  BLOB NOT NULL,        -- KEK 派生密钥加密的设备 token
    expires_at INTEGER NOT NULL      -- 注册/刷新时由 expires_in 计算（unix 秒）
);

CREATE TABLE IF NOT EXISTS app_config (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    server_url TEXT NOT NULL DEFAULT ''   -- 服务端地址+端口（§9.2 客户端配置持久化）
);

CREATE TABLE IF NOT EXISTS sync_state (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    last_seq    INTEGER NOT NULL DEFAULT 0,
    last_pull_at INTEGER
);

CREATE TABLE IF NOT EXISTS group_state (
    group_id       TEXT PRIMARY KEY,
    archived       INTEGER NOT NULL DEFAULT 0,
    key_version    INTEGER NOT NULL DEFAULT 1,  -- 组当前 kv（本地 put 加密用，§9.1）
    initialized_at INTEGER              -- 全量拉取完成时间；无记录=新加入的组（§7.2 4c）
);

CREATE TABLE IF NOT EXISTS local_entries (
    entry_id            TEXT PRIMARY KEY,
    group_id            TEXT NOT NULL,
    seq                 INTEGER NOT NULL DEFAULT 0,
    key_version         INTEGER NOT NULL,
    ciphertext          TEXT NOT NULL,        -- 4.3 密文包 JSON（墓碑为空）
    plaintext_cache_enc BLOB,                -- KEK 派生密钥加密的明文缓存（§9.1）
    base_enc            BLOB,                -- 编辑前共同祖先快照（§7.3 三路合并）
    dirty               INTEGER NOT NULL DEFAULT 0,
    deleted             INTEGER NOT NULL DEFAULT 0, -- 本地墓碑标记（待推送删除，§7.2 4d）
    conflict_of         TEXT,                -- 冲突副本归属（§7.3）
    updated_at          INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_local_group ON local_entries(group_id);

CREATE TABLE IF NOT EXISTS pending_entries (
    entry_id    TEXT PRIMARY KEY,
    group_id    TEXT NOT NULL,
    seq         INTEGER NOT NULL,
    key_version INTEGER NOT NULL,
    ciphertext  TEXT NOT NULL,
    updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS key_cache (
    group_id    TEXT NOT NULL,
    key_version INTEGER NOT NULL,
    dek_enc     BLOB NOT NULL,        -- KEK 加密的 DEK
    PRIMARY KEY (group_id, key_version)
);

CREATE TABLE IF NOT EXISTS bad_seq (
    seq        INTEGER PRIMARY KEY,
    fail_count INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE IF NOT EXISTS recycle_bin (
    entry_id    TEXT PRIMARY KEY,
    ciphertext  TEXT NOT NULL,
    deleted_at  INTEGER NOT NULL
);
