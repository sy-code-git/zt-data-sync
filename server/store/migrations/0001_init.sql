-- 0001_init.sql — 服务端 SQLite 全量建表（设计文档 §5.2）
-- 迁移只增不改；已应用的版本记录于 schema_migrations，幂等跳过。

-- 全局序列号：同步的唯一时钟
CREATE TABLE seq_counter (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    value INTEGER NOT NULL DEFAULT 0
);
INSERT INTO seq_counter (id, value) VALUES (1, 0);

CREATE TABLE users (
    id             TEXT PRIMARY KEY,            -- UUID
    name           TEXT NOT NULL,               -- 显示名，不设 UNIQUE（小团队允许重名）
    sm2_public_key TEXT NOT NULL,               -- base64(DER)
    attestation    TEXT NOT NULL,               -- HMAC-SM3(PB_REG_SECRET, "passbook-attestation-v1"||name||pubkey)
    role           TEXT NOT NULL DEFAULT 'member',  -- admin | member
    status         TEXT NOT NULL DEFAULT 'active',  -- active | revoked
    created_at     INTEGER NOT NULL,            -- unix 秒
    revoked_at     INTEGER
);

CREATE TABLE devices (
    id         TEXT PRIMARY KEY,                -- UUID
    user_id    TEXT NOT NULL REFERENCES users(id),
    name       TEXT NOT NULL,                   -- 设备名（注册时用户填写，如 zhangsan-mbp）
    hostname   TEXT,                            -- 机器名（客户端上报 os.Hostname()，可空）
    last_ip    TEXT,                            -- 最近一次请求来源 IP（中间件更新，可空）
    token_hash TEXT NOT NULL UNIQUE,            -- SM3(token)，token 本身不落库
    status     TEXT NOT NULL DEFAULT 'active',  -- active | disabled
    created_at INTEGER NOT NULL,
    last_seen  INTEGER                          -- 最近一次请求时间（在线判定用）
);

CREATE TABLE groups (
    id            TEXT PRIMARY KEY,             -- UUID
    name          TEXT NOT NULL,                -- 建议代号，如 G1
    key_version   INTEGER NOT NULL DEFAULT 1,
    pending_rekey INTEGER NOT NULL DEFAULT 0,   -- 1=有待完成的重加密
    archived      INTEGER NOT NULL DEFAULT 0,   -- 1=已归档，协作冻结（历史只读）
    created_at    INTEGER NOT NULL,
    archived_at   INTEGER                       -- 归档时间，null=未归档
);

CREATE TABLE group_members (
    group_id   TEXT NOT NULL REFERENCES groups(id),
    user_id    TEXT NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE entries (
    id          TEXT PRIMARY KEY,               -- UUID，客户端生成
    group_id    TEXT NOT NULL REFERENCES groups(id),
    seq         INTEGER NOT NULL UNIQUE,        -- 全局递增，服务端分配
    key_version INTEGER NOT NULL,
    ciphertext  TEXT NOT NULL,                  -- 4.3 条目密文包 JSON
    size_bytes  INTEGER NOT NULL,
    deleted     INTEGER NOT NULL DEFAULT 0,     -- 墓碑标记
    updated_by  TEXT NOT NULL REFERENCES devices(id),
    updated_at  INTEGER NOT NULL                -- 服务端时间，仅展示用
);
CREATE INDEX idx_entries_group_seq ON entries(group_id, seq);
CREATE INDEX idx_entries_seq       ON entries(seq);

CREATE TABLE key_envelopes (
    group_id    TEXT NOT NULL REFERENCES groups(id),
    key_version INTEGER NOT NULL,
    user_id     TEXT NOT NULL REFERENCES users(id),
    wrapped_dek TEXT NOT NULL,                  -- 4.3 信封 JSON
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (group_id, key_version, user_id)
);
CREATE INDEX idx_envelopes_user ON key_envelopes(user_id);

CREATE TABLE audit_log (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ts          INTEGER NOT NULL,
    device_id   TEXT NOT NULL,
    user_id     TEXT NOT NULL,
    action      TEXT NOT NULL,   -- bootstrap/device_register/device_refresh/device_online/
                                 -- device_offline/create_user/revoke/keyfile_reset/
                                 -- add_member/remove_member/disable_device/push/delete/rekey/wrap/
                                 -- archive/unarchive
                                 -- 注：pull 不逐次记审计（回退轮询 5s/次会爆量）；
                                 --   仅异常路径记录（如 40302 越权拉取），action=pull_denied
    entry_id    TEXT,            -- 可空，不记任何明文
    ip          TEXT,            -- 事件来源 IP（登录/上线事件记，三期启用）
    device_name TEXT,            -- 设备名快照（设备重命名/删除后仍可溯源）
    hostname    TEXT,            -- 机器名快照（客户端上报）
    detail      TEXT             -- 元数据 JSON，如 {"result":"conflict"}
);
CREATE INDEX idx_audit_ts   ON audit_log(ts);
CREATE INDEX idx_audit_user ON audit_log(user_id, ts);
