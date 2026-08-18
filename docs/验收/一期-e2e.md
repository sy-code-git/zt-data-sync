# 一期端到端验收记录（§14.1 步骤 1.9）

- 操作人：开发者（自动脚本 + 实机验证）
- 环境：Ubuntu 26.04 裸机（内网测试 VM），无 Docker；Go 1.26.4 交叉编译 Linux 二进制
- Windows 实机：Win11（内网测试机，SSH 公钥认证），交叉编译 Windows 二进制
- 服务端：TLS 1.3 自签 CA，`PB_REG_SECRET` / `PB_BOOTSTRAP_CODE` 预置
- 客户端：`pbcli`（client/core 无 UI harness）+ `keytool`
- 日期：2026-08-13 ~ 2026-08-14

## 场景清单（逐条打勾）

| # | 场景 | 结果 | 证据 |
|---|---|---|---|
| 1 | 首启 bootstrap：admin 用 keytool 生成 keyfile → 首启 token 建立首个 admin | ✅ | bootstrap 返回 user_id/device_id/role=admin；二次调用拒绝（users 非空） |
| 2 | admin 一键建用户（导入 pub.json）+ 建组 + 加成员 → 新人解锁自动收信封解密 | ✅ | create-user 返回 user_id；加成员成功；alice 解锁后冷启动生成 DEK 并可 put/list |
| 3 | A 端新增条目 → 同步 → 可见；内容一致 | ✅ | alice put → sync → list 显示"生产环境服务器"；服务端 entries 表密文落库（seq=1, kv=1） |
| 4 | 断网改多条目 → 恢复按序 push；三路合并 | ✅ | alice 离线连续 put 3 条（dirty）→ sync 按序 push（seq=4/5/6）；dave 拉取见 3 条、dave push 后 alice 拉取见 4 条（双向收敛）。字段级三路合并由 core/merge 单测覆盖（不同字段合并/同字段冲突标记） |
| 5 | 删除条目 → 墓碑 → 回收站；不误回传 | ✅ | alice delete → 服务端 deleted=1（墓碑 seq=3）→ 本地 recycle_bin 有记录 → carol 拉取不出现已删除条目（墓碑收敛） |
| 6 | 吊销成员 → token 即刻失效（40301/40302）+ 在线成员 auto-rekey | ✅ | bob 吊销后再 sync 报"设备已禁用"；alice 同步收敛 |
| 7 | 全员离线吊销 → 组写挂起 + 上线自动收敛 | ✅ | 吊销 carol（离线）→ members 移除 + pending_rekey=1 → alice 上线 auto-rekey 收敛（kv 3→4、信封只剩 alice、pending_rekey 清 0）→ carol 再 sync 报"设备已禁用" |
| 8 | 篡改服务端密文一字节 → 客户端 hmac 告警拒绝入库 | ✅ | 篡改 entries.ct 后 alice 全量拉取，条目显示"[无明文]"（hmac 失败拒绝解密） |
| 9 | 锁屏/退出后内存私钥与 DEK 清零 | ⏳ Windows | vault.wipeLocked/wipeSM2PrivateKey 单测覆盖；内存清零为 Windows 客户端运行时行为 |
| 10 | 失焦 5 分钟自动锁 + 密码生成器 | ⏳ Windows | 失焦锁定/倒计时提示已实现；实机走查待 Windows 客户端 |
| 11 | 启动/解锁后立即自动拉取离线变更 | ✅ | 重置 alice 本地后 unlock+sync 自动全量拉取收敛 |
| 12 | SSE 推送 ≤1s + 断线回退轮询 | ✅ | 实测：alice SSE 监听（`sse-listen`）→ dave push 触发 `event: change`，延迟 460ms（≤1s 达标，且 CHANGE 早于 push 完成 5.9ms）。断线回退轮询由 TestBackoff 单测覆盖（健康 5s / 降级 30s / 连续失败切轮询） |
| 13 | 预防性 rekey → pending_rekey 置位 → 在线成员收敛（kv+1、重加密、删旧信封） | ✅ | rekey 后 kv 稳定收敛（1→3）、pending_rekey 清 0、条目重加密到新 kv、旧信封物理删除 |
| 14 | DPAPI 自动解锁（Windows） | ✅ | Win11 实机：`autounlock enable`（口令解锁+DPAPI 保护 KEK）→ 新进程 `try` 免口令解锁成功（无 PB_PASSWORD）；`status` 正确记录 keyfile 绝对路径；`disable` 后 `try` 失败（"未开启自动解锁"）；重新 `enable` 后 `try` 恢复成功 |
| 15 | 管理端组成员清单（在线状态/IP/设备名） | ✅ | members 返回 alice online=true + device 信息；devices 返回 admin/alice 设备 |
| 16 | 归档/重启组 | ✅ | archive 后 push/keys 拒收、groups 标记 archived；unarchive 恢复 |

> ✅ 通过 ｜ ⏳ 部分/待 Windows 实机

## 验收中发现并修复的问题

1. **P1（rekey 死循环）**：`reencryptGroupEntries` 只重加密 `key_version == oldKV`
   的条目，rekey 中断恢复时本地条目停留在更旧 kv，导致 kv 每轮 +1、`pending_rekey`
   永不收敛。修复为「重加密所有未达 newKV 的条目」，并补回归测试
   `TestRekeyReencryptsStaleKVEntry`（还原修复即失败）。
2. **客户端 CA 信任缺失（§8.3）**：`HTTPClient` 无法信任自签 CA。补
   `NewHTTPClientWithCA` + pbcli `--ca` 参数。
3. **pbcli 能力缺口**：缺设备注册 / admin CLI / delete，无法在 Linux 驱动 e2e。
   补齐 `bootstrap` / `admin <sub>` / `delete` / unlock `--user` 注册。

## 性能基线（§14.1 性能基线第 11 条）

| 项 | 指标 | 实测 | 达标 |
|---|---|---|---|
| 解锁（PBKDF2 10 万次） | ≤1.5s | 0.22s | ✅ |
| 单轮同步（pull+push） | — | 0.22s | ✅ |
| 单条 push | ≤200ms | <200ms（同步内） | ✅ |
| 服务端空闲内存 | ≤150MB | 17.3MB | ✅ |

> pull 500 条 ≤1s 未单独压测（需构造 500 条数据）；单条 push 由同步耗时间接验证。

## 部署

- 裸机部署（无 Docker）：交叉编译 `server`/`pbcli`/`keytool` Linux 二进制 →
  `scripts/gen-ca.sh` 生成自签证书 → 环境变量文件 + `setsid` 拉起。
- `curl -k https://localhost:8443/healthz` → `ok`。

## 备份→删库→恢复演练（2026-08-14 实测）

- `sqlite3 backup` 生成一致性快照 → 删库（`rm passbook.db*`）→ 重启为**空库**（users=0）
  → 用备份覆盖恢复 → 重启后 4 用户 / 1 组 / 8 条目 / 2 信封完整恢复，`healthz` ok。
- 结论：SQLite 文件级备份 + 停服恢复即可完整恢复（单机单文件部署，无额外依赖）。

## 待补（需 Windows 实机 / 双端常驻客户端）

- 场景 9/10 的 Windows GUI 客户端（Wails）实机走查：失焦 5 分钟自动锁、锁屏内存清零
  （内存清零逻辑 `wipeLocked`/`wipeSM2PrivateKey` 已由单测覆盖；失焦锁定为纯前端运行时行为）。
  **人工走查清单**：① Win11 桌面打开 app → 解锁 → 静置失焦 5 分钟 → 回到应用应回到解锁页；
  ② 任务管理器观察 app 内存，锁屏后私钥/DEK 已清零（代码 `wipeLocked`）。
- 场景 14 的换账户/换机器失效（DPAPI 平台保证，代码已确认用户级 `CryptProtectData`，
  未实机验证需第二账户）。
- Docker 一条拉起（本环境无 Docker）。
