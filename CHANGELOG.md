# Changelog

本文件按版本记录「在线密码本」的变更（§14.1 一期收口要求）。

## v0.1.0（一期 MVP 交付）

一期 = 服务端 + 客户端核心库（加解密/同步/冲突合并）+ 标准 UI 壳 + 管理端，
配功能驱动最小 UI（密码生成器、自动锁定、DPAPI 自动解锁）。

### 功能

- **加密体系**：国密 SM2/SM3/SM4 全链路（keyfile 口令派生 KEK、SM2 包裹 DEK 信封、
  SM4-GCM 条目加密 + HMAC-SM3 防伪章），wire format 带版本前向兼容。
- **服务端**：TLS 1.3；bootstrap 一次性引导、设备注册（challenge + SM2 签名核销）；
  同步（push/pull 增量 + SSE 推送 + 回退轮询）；admin API（用户/组/成员/设备/审计/
  rekey/归档）；限流、审计、墓碑清理。
- **客户端核心库 `client/core`**：vault（解锁/DEK 缓存/加解密）、syncer（SSE + 轮询 +
  auto-wrap 冷启动 + auto-rekey + 写挂起）、merge（字段级三路合并）、store（本地 SQLite）。
- **普通客户端 `client/app`**（Wails）：解锁/列表/编辑/冲突逐字段解决/设置页面；
  密码生成器；失焦 5 分钟自动锁定（含倒计时提示）；Windows DPAPI 自动解锁。
- **管理面板（`client/app` 内，`--admin` 模式）**：引导/组管理/成员清单（在线状态/设备）/成员开户与移除/设备（主机信息）/审计/密钥生成。
- **CLI**：`keytool`（genuser/pubkey/inspect）；`pbcli`（unlock/bootstrap/admin/
  put/delete/sync/list/genpass，含设备注册与 CA pinning）。

### 安全

- 服务端零明文（不定义/不记录任何明文敏感字段类型与日志）。
- 密钥一律 `[]byte` 驻留，锁定/退出覆写清零（Wipe）。
- 随机数仅 `crypto/rand`；密钥不跨层到前端 JS。
- 自签 CA + 客户端内置 CA 信任（pinning）支持（§8.3）。
- gosec 0 告警；go vet 零告警；`go test -race` 无数据竞态。

### 验收

- 一期 16 场景端到端验收：bootstrap / 建用户加组 / 双端同步 / rekey / 吊销 /
  归档重启 / 成员清单 / 篡改密文拒收等核心场景在 Linux 裸机实机通过（`docs/验收/一期-e2e.md`）。
- DPAPI 自动解锁（场景 14）Windows 实机单测闭环通过；失焦锁定/锁屏清零（场景 9/10）
  为 Windows 客户端 UI 行为，实机走查待补。
- 性能基线：解锁 ≤0.3s、单轮同步 ≤0.3s、服务端空闲内存 ≤20MB（详见验收文档）。

### 修复

- P1：auto-rekey 中断恢复时重加密只匹配 `key_version == oldKV`，本地条目停留在更旧
  kv 时每轮 kv+1、pending_rekey 永不收敛的死循环（改为重加密所有未达 newKV 的条目）。
- P0：token 二次解密导致 Unlock 必败。
- P1：auto-rekey 包裹对象错误（多组场景信封含非组成员，rekey 永不收敛）。
- 其余 P2/P3：密钥卫生、并发安全、错误码映射、半解锁态回滚等累计 15+ 项。

## 未收录（后续版本规划）

- 二期 UI 打磨、三期可选功能（TOTP/附件/备份回收站/Windows Hello/更新推送等）。
