-- 0003_invites_registration.sql — 邀请码 + 注册申请（方案 C：邀请码绑定工号 + 审核制）
-- 邀请码：绑定工号、一次即废；同工号未开户可重复生成；默认 3 天过期（生成时可配）
-- 免审核邀请码（auto_approve=1）：用户凭码提交申请后服务端自动开户，无需人工审核
CREATE TABLE invites (
    id           TEXT PRIMARY KEY,             -- UUID
    code         TEXT NOT NULL UNIQUE,         -- 邀请码（短码）
    username     TEXT NOT NULL,                -- 绑定工号（登录标识，唯一、不可改）
    auto_approve INTEGER NOT NULL DEFAULT 0,   -- 1=免审核（提交申请即自动开户）
    status       TEXT NOT NULL DEFAULT 'unused', -- unused | used（一次即废）
    expires_at   INTEGER NOT NULL,             -- unix 秒（默认 now+3d）
    created_by   TEXT NOT NULL,                -- 管理员 user_id
    created_at   INTEGER NOT NULL,
    used_at      INTEGER                       -- 使用时间（null=未用）
);
CREATE INDEX idx_invites_username ON invites(username);

-- 注册申请：用户凭邀请码提交（公钥/设备名；IP 由服务端记录，审核时核对）
CREATE TABLE register_requests (
    id             TEXT PRIMARY KEY,           -- UUID
    invite_code    TEXT NOT NULL REFERENCES invites(code),
    username       TEXT NOT NULL,              -- 工号（须与邀请码绑定一致）
    sm2_public_key TEXT NOT NULL,              -- base64(DER)
    device_name    TEXT NOT NULL,              -- 申请设备名
    ip             TEXT,                       -- 申请来源 IP
    status         TEXT NOT NULL DEFAULT 'pending', -- pending | approved | rejected
    created_at     INTEGER NOT NULL,
    reviewed_by    TEXT,                       -- 审核管理员 user_id（null=未审）
    reviewed_at    INTEGER
);
CREATE INDEX idx_reg_req_status ON register_requests(status);
CREATE INDEX idx_reg_req_invite ON register_requests(invite_code);
