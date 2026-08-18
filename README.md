# 在线密码本（passbook）

零知识架构的团队密码本：**国密 SM2/SM3/SM4 全链路加密**，Go 服务端 + Wails 桌面客户端，团队成员间的密码/凭证安全共享。

服务端**零明文存储**——条目以密文入库，任何一方（含服务端）都无法读取明文；加解密、组密钥、冲突合并全部在客户端完成。

---

## 核心特性

- **国密全链路**：SM2（非对称包裹 DEK）、SM3（HMAC 防伪章）、SM4-GCM（条目加密），wire format 带版本前向兼容
- **零知识架构**：服务端不定义/不记录任何明文敏感字段，密钥 `[]byte` 驻留、锁定即 Wipe
- **组协同**：多成员组共享密钥（DEK 信封）、新成员入组自动交接（auto-wrap）、组密钥升级（auto-rekey）
- **实时同步**：SSE 推送 + 回退轮询，增量拉取 + 字段级三路冲突合并
- **离线可用**：离线可编辑，联网后自动合并推送；手动/自动同步方式可切换
- **桌面客户端**：Wails GUI，解锁/列表/编辑/冲突解决/设置，Windows DPAPI 自动解锁
- **管理面板**：组/成员/设备/审计管理，成员在线状态与主机信息可视
- **自签 CA 支持**：内网部署证书 pinning，不依赖公网信任链

## 技术栈

| 层 | 技术 |
|---|---|
| 服务端 | Go 1.21+、TLS 1.3、SQLite（无外部依赖）、chi 路由 |
| 客户端 | Wails v2、Vue 3 + Vite、Go core 库（vault/syncer/merge） |
| CLI | `keytool`（密钥生成）、`pbcli`（命令行客户端） |
| 部署 | Docker Compose（多阶段构建，distroless/alpine） |

## 安全机制

- 私钥口令派生 KEK → SM2 包裹组 DEK 信封 → SM4-GCM 加密条目 + HMAC-SM3 防伪章
- 注册凭证 attestation（`PB_REG_SECRET`）+ 一次性 bootstrap token（首启建立 admin）
- 设备注册 challenge + SM2 签名核销；吊销/移出即时断权 + 自动重加密
- 随机数仅 `crypto/rand`；密钥不跨层到前端 JS；本地库敏感列 SM4-GCM
- 限流（认证/同步/心跳/admin 分桶）、审计日志、墓碑清理

## 系统组成

| 组件 | 目录 | 产物 |
|---|---|---|
| 服务端 | `server/` + `cmd/server` | `编译/服务端产物/密码本服务端.exe` |
| 桌面客户端（含管理面板） | `client/app` | `编译/客户端产物/在线密码本.exe` |
| 命令行客户端 | `cmd/pbcli` | `编译/客户端产物/密码本命令行.exe` |
| 密钥工具 | `cmd/keytool` | `编译/客户端产物/钥匙工具.exe` |

## 快速开始

完整的「环境准备 → 编译 → 部署 → 运维 → 客户端使用」见 **[`docs/编译与部署全流程.md`](docs/编译与部署全流程.md)**。

```bash
# 1. 编译（Windows 双击 编译\全部编译.bat；Linux 用 .sh）
编译/全部编译.bat

# 2. 部署（服务器上）
bash scripts/gen-ca.sh ./deployments/certs <服务器IP>   # 生成证书
# 写 deployments/.env（PB_REG_SECRET / PB_BOOTSTRAP_CODE）
cd deployments && docker compose up -d
curl -sk https://localhost:8443/healthz   # → ok

# 3. 客户端
编译\客户端产物\在线密码本.exe --admin      # 管理员首次部署
编译\客户端产物\在线密码本.exe              # 普通用户
# 解锁页填服务端地址 + CA 路径（编译\ca.crt，可用 编译\服务端运维.bat ca 下载）
```

运维：`编译/服务端运维.bat [view|reset|ca]`（查看/重置部署密钥、下载 CA）。

## 分支模型（Git 通用约定）

| 分支 | 定位 | 远端 | 说明 |
|------|------|------|------|
| `main` | 发布分支 | ✅ 推送 | 只保留干净发布历史 |
| `dev` | 开发分支 | ❌ 不推送 | 保留完整开发历史，仅本地 |

- 日常开发在 `dev`；发布时用 `发布.sh`/`发布.bat` 一键 squash 合入 `main` 并推送远端、回同步 dev。
- 详细约定见 [`docs/Git通用约定.md`](docs/Git通用约定.md)。

## 目录结构

```
在线密码本/
├── client/         桌面客户端
│   ├── app/        Wails 壳（前端 + Go 绑定 + 管理面板 --admin 模式）
│   └── core/       核心库（vault 加解密 / syncer 同步 / merge 三路合并 / store 本地库 / api 契约）
├── server/         服务端（api / store / sync / authn / middleware）
├── internal/       共享库（crypto / model / proto / merge）
├── cmd/            CLI 入口（server / pbcli / keytool）
├── deployments/    Dockerfile + docker-compose.yml
├── scripts/        gen-ca.sh（证书生成）
├── docs/           设计文档 + 约定 + 部署指南
└── 编译/           编译脚本（bat/sh）+ 产物目录 + 运维脚本
```

## 文档索引

| 文档 | 说明 |
|---|---|
| [`docs/设计文档-v3-无感协同版.md`](docs/设计文档-v3-无感协同版.md) | 主设计文档（架构/协议/安全） |
| [`docs/客户端开发约定.md`](docs/客户端开发约定.md) | 客户端开发约定 |
| [`docs/加解密与人员协同机制图解.html`](docs/加解密与人员协同机制图解.html) | 机制图解 |
| [`docs/编译与部署全流程.md`](docs/编译与部署全流程.md) | 编译/部署/运维/使用全流程 |
| [`docs/Git通用约定.md`](docs/Git通用约定.md) | 分支与提交规范 |
| [`docs/多服务端容灾路线图.md`](docs/多服务端容灾路线图.md) | 二期/三期规划 |
| `CHANGELOG.md` | 版本变更记录 |

## 同步约定

- 设计文档以本地 `docs/` 为基准，AI 助手在修改后同步到项目资产空间（云空间）。
- 项目资产空间（云空间）目录划分：
  - `文档/` —— 设计文档、客户端约定、机制图解等文档类资产
  - `code/` —— 代码仓库资产（与本地 git 仓库对应）
- 代码以本地 git 仓库为准；需要归档到云空间时，将仓库快照上传至 `code/` 文件夹。

## 版本记录

- **v0.1.0**（一期 MVP 交付）：服务端 + 客户端核心库 + 标准 UI + 管理面板 + CLI；16 场景端到端验收通过（详见 `CHANGELOG.md` 与 `docs/验收/一期-e2e.md`）。
