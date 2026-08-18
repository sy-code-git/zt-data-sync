// Package core 客户端核心库——组装 vault/syncer/merge/store，实现 api.Core 对外接口（§3.1）。
// UI 壳（Wails 绑定）只调用本包的 Core；密钥类型不出本包。
package core

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"passbook/client/core/api"
	"passbook/client/core/store"
	"passbook/client/core/syncer"
	"passbook/client/core/vault"
	"passbook/internal/crypto"
	"passbook/internal/proto"
)

// Core 客户端核心对外接口实现（§3.1）。
type Core struct {
	local     store.LocalStore
	serverURL string
	caPath    string // 自签 CA 证书路径（§8.3；空 = 系统默认验证）
	username  string // 当前登录工号（identity 表；未初始化空串）
	role      string // 当前用户角色（admin | member；bootstrap 时置 admin）
	vault     *vault.Vault
	engine    *syncer.Engine
	now       func() time.Time
	// token 设备 token（unlock 后持有，供 engine 重建；锁定即清空，密钥卫生）
	token string
	// syncMode 同步方式：auto（自动同步，默认）| manual（手动同步）。
	// manual 不启动后台 SSE/心跳/轮询，保存条目也不自动推送，仅 SyncNow 手动触发。
	syncMode string
	// listener 订阅回调（engine 惰性创建后自动挂载，避免订阅时序丢失）
	listener    api.Listener
	eventHooked bool // engine 上是否已挂 forwardEvent 桥接
}

// SyncModeAuto / SyncModeManual 同步方式常量。
const (
	SyncModeAuto   = "auto"
	SyncModeManual = "manual"
)

// New 构造 Core（本地库 + 服务端地址 + CA 路径；engine 在 unlock 后惰性创建）。
func New(local store.LocalStore, serverURL string) *Core {
	caPath, _ := local.GetCA()
	username := ""
	role := ""
	if id, err := local.GetIdentity(); err == nil {
		username = id.Username
		role = id.Role
	}
	syncMode, _ := local.GetSyncMode()
	if syncMode != SyncModeManual {
		syncMode = SyncModeAuto
	}
	return &Core{local: local, serverURL: serverURL, caPath: caPath, username: username, role: role, syncMode: syncMode, vault: vault.New(local), now: time.Now}
}

// startEngineIfAuto 按同步方式启动引擎：auto 启动后台循环；manual 不启动（仅手动 SyncNow）。
func (c *Core) startEngineIfAuto() {
	if c.engine == nil {
		return
	}
	if c.syncMode == SyncModeManual {
		return
	}
	c.engine.Start()
}

// newHTTPClient 构造服务端 HTTP 客户端：配置了自签 CA 则 pinning，否则系统默认验证（§8.3）。
func (c *Core) newHTTPClient(token string) (*api.HTTPClient, error) {
	if c.caPath != "" {
		return api.NewHTTPClientWithCA(c.serverURL, token, c.caPath)
	}
	return api.NewHTTPClient(c.serverURL, token), nil
}

// SetServerURL 更新服务端地址（设置页修改后调用）。
// 解锁态且 engine 已启动时立即重建 engine 指向新地址（§9.2：修改后立即生效），
// 否则仅保存地址，下次 unlock 生效。
func (c *Core) SetServerURL(url string) {
	c.serverURL = url
	c.rebuildEngineIfUnlocked()
}

// SetCA 更新自签 CA 证书路径（§8.3；空串 = 系统默认验证）。
// 解锁态立即重建 engine 用新 CA 重连，否则下次 unlock 生效。
func (c *Core) SetCA(caPath string) {
	c.caPath = caPath
	c.rebuildEngineIfUnlocked()
}

// CA 当前自签 CA 证书路径（UI 展示用）。
func (c *Core) CA() string { return c.caPath }

// rebuildEngineIfUnlocked 解锁态且有 token 时重建 engine（地址/CA 变更后立即生效）。
func (c *Core) rebuildEngineIfUnlocked() {
	if !c.IsUnlocked() || c.engine == nil || c.token == "" {
		return
	}
	c.engine.Stop()
	c.engine = nil
	c.eventHooked = false
	hc, err := c.newHTTPClient(c.token)
	if err != nil {
		return
	}
	c.engine = syncer.New(c.vault, c.local, hc, hc.SSEBaseClient(), c.serverURL, c.token, c.now)
	c.hookEngine()
	c.startEngineIfAuto()
}

// ServerURL 当前服务端地址（UI 展示用）。
func (c *Core) ServerURL() string { return c.serverURL }

// ---- 生命周期 ----

// ImportKeyfile 导入私钥备份（换设备/清库后恢复身份，§4.3）。
// 流程：读 keyfile → 口令解私钥解锁 → 存 identity（username/role/公钥）→ finishUnlock。
// 返回解锁结果（本地无设备 token 时 need_register=true，需注册设备）。
func (c *Core) ImportKeyfile(path, username, role string, password []byte) (*api.UnlockResult, error) {
	// 1. 读 keyfile blob
	kf, err := crypto.LoadKeyfile(path)
	if err != nil {
		return nil, err
	}
	blob, err := kf.MarshalJSON()
	if err != nil {
		return nil, err
	}
	// 2. 口令解私钥并解锁（口令错误/损坏返回错误）
	ds, err := c.vault.UnlockWithKeyfileBlob(blob, password)
	if err != nil {
		return nil, err
	}
	// 3. 取公钥
	pubB64, err := c.vault.PublicKeyB64()
	if err != nil {
		c.Lock()
		return nil, err
	}
	// 4. 存 identity（恢复身份）
	if err := c.local.SetIdentity(&store.Identity{Username: username, Role: role, KeyfileBlob: blob, PublicKey: pubB64}); err != nil {
		c.Lock()
		return nil, err
	}
	c.username = username
	c.role = role
	// 5. 读设备 token → 建 engine → 组装结果（无 token 则 need_register）
	return c.finishUnlock(ds)
}

// ExportKeyfile 导出私钥备份（keyfile 格式）到指定路径（换设备恢复用，§4.3）。
// 需已初始化身份（identity 表有加密私钥）。
func (c *Core) ExportKeyfile(path string) error {
	id, err := c.local.GetIdentity()
	if err != nil || len(id.KeyfileBlob) == 0 {
		return errors.New("本地无身份信息，请先生成密钥对")
	}
	return os.WriteFile(path, id.KeyfileBlob, 0o600)
}

// GenerateKeypair 首次初始化身份（方案 A）：生成密钥对 + 加密存本地库 + 解锁。
// role 为身份角色（admin | member）。返回公钥 base64（开户用）。
// 成功时 vault 处于解锁态（私钥驻留内存，供注册设备签名）。
func (c *Core) GenerateKeypair(username, role string, password []byte) (string, error) {
	pubB64, blob, err := c.vault.GenerateKeypair(password)
	if err != nil {
		return "", err
	}
	if err := c.local.SetIdentity(&store.Identity{Username: username, Role: role, KeyfileBlob: blob, PublicKey: pubB64}); err != nil {
		c.Lock()
		return "", err
	}
	c.username = username
	c.role = role
	return pubB64, nil
}

// Unlock 解锁（工号+密码，从本地库解私钥）并启动同步（§9.2）。
func (c *Core) Unlock(username string, password []byte) (*api.UnlockResult, error) {
	id, err := c.local.GetIdentity()
	if err != nil {
		return nil, errors.New("本地无身份信息，请先初始化（生成密钥对）")
	}
	if id.Username != username {
		return nil, errors.New("工号不匹配")
	}
	ds, err := c.vault.UnlockWithKeyfileBlob(id.KeyfileBlob, password)
	if err != nil {
		return nil, err
	}
	c.username = username
	return c.finishUnlock(ds)
}

// Username 当前登录工号（未初始化返回空串）。
func (c *Core) Username() string { return c.username }

// Role 当前用户角色（admin | member；未解锁返回空串）。
func (c *Core) Role() string { return c.role }

// TryAutoUnlock 尝试 DPAPI 免口令解锁（§9.1 自动解锁，Windows 专属）。
// 失败返回错误，调用方回退口令解锁。
func (c *Core) TryAutoUnlock() (*api.UnlockResult, error) {
	ds, err := c.vault.TryAutoUnlock()
	if err != nil {
		return nil, err
	}
	return c.finishUnlock(ds)
}

// finishUnlock 解锁后的公共后置处理：解密 token、建 engine、启动同步、组装结果。
// 任一步失败即回滚锁定，避免"vault 已解锁但 token/engine 未就绪"的半解锁态
// 使密钥与明文 token 驻留内存（§9.1 密钥卫生）。
func (c *Core) finishUnlock(ds *store.DeviceState) (*api.UnlockResult, error) {
	// 解密设备 token（若有）构造同步引擎
	token := ""
	var err error
	if ds != nil && len(ds.TokenEnc) > 0 {
		token, err = c.vault.DecryptToken(ds.TokenEnc)
		if err != nil {
			c.Lock()
			return nil, fmt.Errorf("解密设备 token 失败: %w", err)
		}
	}
	if token != "" {
		c.token = token
		hc, err := c.newHTTPClient(token)
		if err != nil {
			c.Lock()
			return nil, fmt.Errorf("构造服务端客户端失败: %w", err)
		}
		// 验证连通（§9.2：必填配置验证通过后才进入主界面）
		if err := hc.Heartbeat(""); err != nil {
			c.Lock()
			return nil, fmt.Errorf("服务端连接验证失败（检查地址或 token 有效性）: %w", err)
		}
		c.engine = syncer.New(c.vault, c.local, hc, hc.SSEBaseClient(), c.serverURL, token, c.now)
		c.hookEngine()
		c.startEngineIfAuto()
	}
	// 组数（已持有 DEK 的组）
	groups, _ := c.local.ListDEKGroupIDs()
	out := &api.UnlockResult{Groups: len(groups)}
	if ds != nil {
		out.DeviceID = ds.DeviceID
	}
	// 无设备 token → 首次使用，需走设备注册流程（§9.1）
	out.NeedRegister = token == ""
	return out, nil
}

// RegisterDevice 首次注册设备（§9.1：本地无设备 token 时调用）。
// 流程：取一次性挑战 → SM2 私钥签名 → 注册 → token 加密落盘 → 建 engine。
// 需已解锁（vault 持私钥）且未注册（本地无 token）。
// username 为工号（登录标识）。
func (c *Core) RegisterDevice(username, deviceName string) error {
	if !c.IsUnlocked() {
		return errors.New("未解锁")
	}
	if c.serverURL == "" {
		return errors.New("未配置服务端地址")
	}
	hc, err := c.newHTTPClient("")
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	if deviceName == "" {
		deviceName = hostname
	}
	// 1. 取一次性挑战
	chResp, err := hc.DeviceChallenge(username)
	if err != nil {
		return fmt.Errorf("获取设备挑战失败: %w", err)
	}
	// 2. 签名 challenge（msg = challenge 字符串原文，§6.3 验签同此）
	sig, err := c.vault.SignChallenge(chResp.Challenge)
	if err != nil {
		return err
	}
	// 3. 注册设备
	regResp, err := hc.DeviceRegister(&proto.DeviceRegisterRequest{
		Username:   username,
		DeviceName: deviceName,
		Hostname:   hostname,
		Challenge:  chResp.Challenge,
		Signature:  base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		return fmt.Errorf("设备注册失败: %w", err)
	}
	// 4. token 加密落盘 + 建 engine（与 bootstrap 共用的收尾逻辑）
	if err := c.completeDeviceSetup(regResp.Token, regResp.DeviceID, regResp.ExpiresIn); err != nil {
		return err
	}
	return nil
}

// Bootstrap 管理员首次部署（§6.3/§9.3）：生成密钥对 + bootstrap token 注册首个 admin + 建 engine。
// password 为本地口令（保护生成的私钥）；bootstrapToken 为服务端一次性引导 token。
func (c *Core) Bootstrap(username string, password []byte, bootstrapToken, name, deviceName string) error {
	if c.serverURL == "" {
		return errors.New("未配置服务端地址")
	}
	// 1. 生成密钥对（存 identity + 解锁，角色 admin）
	pubB64, err := c.GenerateKeypair(username, "admin", password)
	if err != nil {
		return err
	}
	// 2. bootstrap 注册首个 admin
	hc, err := c.newHTTPClient("")
	if err != nil {
		return err
	}
	hostname, _ := os.Hostname()
	if deviceName == "" {
		deviceName = hostname
	}
	resp, err := hc.Bootstrap(&proto.BootstrapRequest{
		BootstrapToken: bootstrapToken,
		Username:       username,
		Name:           name,
		DeviceName:     deviceName,
		SM2PublicKey:   pubB64,
	})
	if err != nil {
		c.Lock()
		return fmt.Errorf("bootstrap 失败: %w", err)
	}
	// 3. token 加密落盘 + 建 engine
	if err := c.completeDeviceSetup(resp.Token, resp.DeviceID, resp.ExpiresIn); err != nil {
		c.Lock()
		return err
	}
	c.role = resp.Role // admin
	return nil
}

// completeDeviceSetup 设备注册/bootstrap 成功后的公共收尾：token 加密落盘 + 建 engine 启动同步。
func (c *Core) completeDeviceSetup(token, deviceID string, expiresIn int64) error {
	enc, err := c.vault.EncryptToken(token)
	if err != nil {
		return err
	}
	ds := &store.DeviceState{DeviceID: deviceID, TokenEnc: enc, ExpiresAt: expiresIn}
	if err := c.local.SetDeviceState(ds); err != nil {
		return err
	}
	c.token = token
	hc, err := c.newHTTPClient(token)
	if err != nil {
		return err
	}
	c.engine = syncer.New(c.vault, c.local, hc, hc.SSEBaseClient(), c.serverURL, token, c.now)
	c.hookEngine()
	c.startEngineIfAuto()
	return nil
}

// ---- 管理员（§6.3 admin API，需解锁态 + 设备 token） ----

// attestationPrefix 注册凭证计算前缀（与服务端 server/api/admin.go 一致，§4.4）。
const attestationPrefix = "passbook-attestation-v1"

// adminHC 构造带设备 token 的 HTTP 客户端（admin API 用，需已解锁+已注册）。
func (c *Core) adminHC() (*api.HTTPClient, error) {
	if !c.IsUnlocked() || c.token == "" {
		return nil, errors.New("未解锁或未注册设备")
	}
	return c.newHTTPClient(c.token)
}

// SetRegSecret 保存注册凭证密钥（PB_REG_SECRET，管理员首次部署时输入，§4.4）。
// 用 KEK 派生密钥加密后存本地，供后续「开户」计算成员 attestation。
func (c *Core) SetRegSecret(regSecret string) error {
	if !c.IsUnlocked() {
		return errors.New("未解锁")
	}
	enc, err := c.vault.EncryptCache([]byte(regSecret))
	if err != nil {
		return err
	}
	return c.local.SetRegSecretEnc(enc)
}

// HasRegSecret 是否已配置注册凭证密钥。
func (c *Core) HasRegSecret() bool {
	enc, err := c.local.GetRegSecretEnc()
	return err == nil && len(enc) > 0
}

// computeAttestation 用本地 REG_SECRET 计算成员注册凭证（§4.4）。
func (c *Core) computeAttestation(name, pubKey string) (string, error) {
	enc, err := c.local.GetRegSecretEnc()
	if err != nil || len(enc) == 0 {
		return "", errors.New("未配置注册密钥（REG_SECRET），请先在首次部署时输入")
	}
	regSecret, err := c.vault.DecryptCache(enc)
	if err != nil {
		return "", err
	}
	defer crypto.Wipe(regSecret)
	att := crypto.HMACSM3(regSecret, []byte(attestationPrefix+name+pubKey))
	return base64.StdEncoding.EncodeToString(att), nil
}

// AdminCreateUser 开户（§6.3）：用 REG_SECRET 计算 attestation → 建用户。返回 user_id。
func (c *Core) AdminCreateUser(username, name, publicKey string) (string, error) {
	hc, err := c.adminHC()
	if err != nil {
		return "", err
	}
	att, err := c.computeAttestation(name, publicKey)
	if err != nil {
		return "", err
	}
	resp, err := hc.AdminCreateUser(&proto.CreateUserRequest{
		Username: username, Name: name, SM2PublicKey: publicKey, Attestation: att,
	})
	if err != nil {
		return "", err
	}
	return resp.UserID, nil
}

// AdminCreateGroup 建组（§6.3）。返回 group_id。
func (c *Core) AdminCreateGroup(name string) (string, error) {
	hc, err := c.adminHC()
	if err != nil {
		return "", err
	}
	resp, err := hc.AdminCreateGroup(name)
	if err != nil {
		return "", err
	}
	return resp.GroupID, nil
}

// AdminArchiveGroup 归档（删除）组（§6.3，组名二次确认）。
func (c *Core) AdminArchiveGroup(groupID, confirmName string) error {
	hc, err := c.adminHC()
	if err != nil {
		return err
	}
	_, err = hc.AdminArchive(groupID, confirmName)
	return err
}

// AdminUnarchiveGroup 恢复归档组（§6.3）。
func (c *Core) AdminUnarchiveGroup(groupID string) error {
	hc, err := c.adminHC()
	if err != nil {
		return err
	}
	return hc.AdminUnarchive(groupID)
}

// AdminAddMember 加组成员（§6.3，userID 为成员 UUID）。
func (c *Core) AdminAddMember(groupID, userID string) error {
	hc, err := c.adminHC()
	if err != nil {
		return err
	}
	return hc.AdminAddMember(groupID, userID)
}

// AdminListGroups 组列表（§6.3）。
func (c *Core) AdminListGroups() ([]proto.GroupInfo, error) {
	hc, err := c.adminHC()
	if err != nil {
		return nil, err
	}
	return hc.AdminListGroups()
}

// AdminListUsers 用户列表（含工号，§6.3）。
func (c *Core) AdminListUsers() ([]proto.UserInfo, error) {
	hc, err := c.adminHC()
	if err != nil {
		return nil, err
	}
	return hc.ListUsers()
}

// AdminListMembers 组成员清单（§6.3）。
func (c *Core) AdminListMembers(groupID string) ([]proto.GroupMemberInfo, error) {
	hc, err := c.adminHC()
	if err != nil {
		return nil, err
	}
	return hc.AdminListMembers(groupID)
}

// AdminRemoveMember 移出组成员（§6.3，成员名二次确认）。
func (c *Core) AdminRemoveMember(groupID, userID, confirmName string) error {
	hc, err := c.adminHC()
	if err != nil {
		return err
	}
	return hc.AdminRemoveMember(groupID, userID, confirmName)
}

// AdminListDevices 设备/主机列表（§6.3，含在线状态/last_ip/hostname）。
func (c *Core) AdminListDevices() ([]proto.AdminDevice, error) {
	hc, err := c.adminHC()
	if err != nil {
		return nil, err
	}
	return hc.AdminListDevices()
}

// EnableAutoUnlock 开启自动解锁（§9.1，须解锁态；记录当前 keyfile 路径）。
func (c *Core) EnableAutoUnlock(keyfilePath string) error {
	return c.vault.EnableAutoUnlock(keyfilePath)
}

// DisableAutoUnlock 关闭自动解锁（§9.1 关闭后立即失效）。
func (c *Core) DisableAutoUnlock() error {
	return c.vault.DisableAutoUnlock()
}

// AutoUnlockEnabled 是否已开启自动解锁。
func (c *Core) AutoUnlockEnabled() bool {
	return c.vault.AutoUnlockEnabled()
}

// Lock 锁定（停同步 + Wipe 内存密钥，§9.1）。
func (c *Core) Lock() {
	if c.engine != nil {
		c.engine.Stop()
		c.engine = nil
		c.eventHooked = false
	}
	c.token = ""
	c.vault.Lock()
}

func (c *Core) IsUnlocked() bool { return c.vault.IsUnlocked() }

// Subscribe 订阅同步事件（UI 刷新）。
// engine 未创建（未解锁）时先保存回调，unlock 建 engine 后自动挂载。
func (c *Core) Subscribe(fn api.Listener) {
	c.listener = fn
	c.hookEngine()
}

// hookEngine 确保 engine 上挂了 forwardEvent 桥接（engine 重建后需重新挂）。
func (c *Core) hookEngine() {
	if c.engine == nil || c.eventHooked {
		return
	}
	c.engine.Subscribe(c.forwardEvent)
	c.eventHooked = true
}

// forwardEvent 桥接：engine 事件 → 当前 listener（换订阅者不重复挂载）。
func (c *Core) forwardEvent(ev api.Event) {
	if c.listener != nil {
		c.listener(ev)
	}
}

// ---- 条目操作 ----

// ListEntries 列出已解密条目（跳过无明文的 pending/bad_seq，§9.1）。
func (c *Core) ListEntries() ([]api.EntryView, error) {
	if !c.IsUnlocked() {
		return nil, errors.New("未解锁")
	}
	entries, err := c.local.ListLocalEntries()
	if err != nil {
		return nil, err
	}
	// 组归档 map（gid → archived，UI 只读标记，§7.2 4d）
	archived := map[string]bool{}
	if gss, err := c.local.ListGroupStates(); err == nil {
		for _, gs := range gss {
			archived[gs.GroupID] = gs.Archived
		}
	}
	out := make([]api.EntryView, 0, len(entries))
	for i := range entries {
		ev, err := c.toEntryView(&entries[i])
		if err != nil {
			continue // 无明文缓存（pending/bad_seq）跳过
		}
		ev.Archived = archived[ev.GroupID]
		out = append(out, *ev)
	}
	// core 不解析明文（title 在 Plaintext 里），排序/组树由 UI 负责；此处仅按 ID 保证确定性
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// GetEntry 取单条已解密条目。
func (c *Core) GetEntry(id string) (*api.EntryView, error) {
	if !c.IsUnlocked() {
		return nil, errors.New("未解锁")
	}
	le, err := c.local.GetLocalEntry(id)
	if err != nil {
		return nil, err
	}
	return c.toEntryView(le)
}

func (c *Core) toEntryView(le *store.LocalEntry) (*api.EntryView, error) {
	if len(le.PlaintextCache) == 0 {
		return nil, errors.New("无明文缓存")
	}
	plain, err := c.vault.DecryptCache(le.PlaintextCache)
	if err != nil {
		return nil, err
	}
	// 明文原样透传给 UI（core 不解析业务字段）
	return &api.EntryView{
		ID: le.ID, GroupID: le.GroupID, Plaintext: plain,
		Seq: le.Seq, KeyVersion: le.KeyVersion, UpdatedAt: le.UpdatedAt, Deleted: le.Deleted,
		ConflictOf: le.ConflictOf, Dirty: le.Dirty,
	}, nil
}

// GetConflict 取冲突三方数据（base/ours/theirs，§7.3 冲突解决页三栏 diff）。
func (c *Core) GetConflict(id string) (*api.ConflictDetail, error) {
	if !c.IsUnlocked() {
		return nil, errors.New("未解锁")
	}
	le, err := c.local.GetLocalEntry(id)
	if err != nil {
		return nil, err
	}
	if le.ConflictOf == "" {
		return nil, errors.New("该条目无冲突")
	}
	d := &api.ConflictDetail{ID: id}
	// ours = 本地明文缓存（bytes 透传，core 不解析）
	if len(le.PlaintextCache) > 0 {
		if plain, err := c.vault.DecryptCache(le.PlaintextCache); err == nil {
			d.Ours = plain
		}
	}
	// theirs = 服务端当前密文现场解密（conflict 副本密文已存服务端版，§7.3）
	if plain, err := c.vault.DecryptPlaintext(le.GroupID, id, le.Ciphertext); err == nil {
		d.Theirs = plain
	}
	// base = 编辑前快照（可能为空）
	if len(le.BaseEnc) > 0 {
		if plain, err := c.vault.DecryptCache(le.BaseEnc); err == nil {
			d.Base = plain
		}
	}
	if d.Ours == nil && d.Theirs == nil {
		return nil, errors.New("冲突数据不可解（明文缓存与服务端密文均缺失）")
	}
	return d, nil
}

// PutEntry 保存/更新条目（本地加密 + 待推送，§7.2）。
func (c *Core) PutEntry(req *api.PutEntryRequest) error {
	if !c.IsUnlocked() {
		return errors.New("未解锁")
	}
	// 组当前 kv（本地 group_state，§9.1）
	kv := 1
	if gs, err := c.local.GetGroupState(req.GroupID); err == nil && gs != nil && gs.KeyVersion > 0 {
		kv = gs.KeyVersion
	}
	// 明文：UI 已序列化为 JSON 字节，core 直接加密（不解析内容，§架构原则）
	plaintext := req.Plaintext
	// 条目 id：新建生成 UUID；更新沿用
	id := req.ID
	var baseSeq int64 = 0
	var baseEnc []byte
	if id != "" {
		if le, err := c.local.GetLocalEntry(id); err == nil {
			baseSeq = le.Seq
			// §7.3：编辑时保存"共同祖先"快照（base_enc，供三路合并）
			// 首次编辑（无 base 且非冲突副本）→ 旧明文作 base；连续编辑 → 保留原 base
			if len(le.BaseEnc) == 0 && le.ConflictOf == "" && len(le.PlaintextCache) > 0 {
				baseEnc = le.PlaintextCache
			} else if len(le.BaseEnc) > 0 {
				baseEnc = le.BaseEnc
			}
		}
	} else {
		id = newUUID()
	}
	ct, err := c.vault.EncryptPlaintext(req.GroupID, id, plaintext, kv)
	if err != nil {
		return fmt.Errorf("加密失败（确认该组已获取 DEK）: %w", err)
	}
	// 本地入库（dirty 待推送）+ 明文缓存
	cache, err := c.vault.EncryptCache(plaintext)
	if err != nil {
		return err
	}
	if err := c.local.UpsertLocalEntry(&store.LocalEntry{
		ID: id, GroupID: req.GroupID, Seq: baseSeq, KeyVersion: kv, Ciphertext: ct,
		PlaintextCache: cache, BaseEnc: baseEnc, Dirty: true, UpdatedAt: c.now().Unix(),
	}); err != nil {
		return err
	}
	// 触发同步（异步）：手动同步模式不自动推送，仅标记 dirty，等用户点「立即同步」
	if c.engine != nil && c.syncMode != SyncModeManual {
		go func() { _ = c.engine.SyncNow() }()
	}
	return nil
}

// DeleteEntry 删除条目（墓碑 push，§7.2 4d）。
func (c *Core) DeleteEntry(id string) error {
	if !c.IsUnlocked() {
		return errors.New("未解锁")
	}
	le, err := c.local.GetLocalEntry(id)
	if err != nil {
		return err
	}
	// 墓碑 mutation：空密文 + Deleted=true（服务端墓碑 ciphertext 允许空，§5.2）
	// 本地标记：先放入回收站（§7.3 #5 可恢复），再置墓碑待推送
	_ = c.local.PutRecycleBin(id, le.Ciphertext, c.now().Unix())
	if err := c.local.UpsertLocalEntry(&store.LocalEntry{
		ID: id, GroupID: le.GroupID, Seq: le.Seq, KeyVersion: le.KeyVersion, Ciphertext: "",
		Dirty: true, Deleted: true, UpdatedAt: c.now().Unix(),
	}); err != nil {
		return err
	}
	if c.engine != nil && c.syncMode != SyncModeManual {
		go func() { _ = c.engine.SyncNow() }()
	}
	return nil
}

// ResolveConflict 应用冲突解决结果并 push（§7.3 三路合并）。
// useLocal=true 采纳本地版；false 采纳服务端版；manual 非 nil 采纳手动编辑版（明文 JSON 字节）。
func (c *Core) ResolveConflict(id string, useLocal bool, manual []byte) error {
	if !c.IsUnlocked() {
		return errors.New("未解锁")
	}
	le, err := c.local.GetLocalEntry(id)
	if err != nil {
		return err
	}
	if le.ConflictOf == "" {
		return errors.New("该条目无冲突")
	}
	var plaintext []byte
	switch {
	case manual != nil:
		plaintext = manual
	case useLocal:
		// ours 版本：取本地明文缓存
		plain, err := c.vault.DecryptCache(le.PlaintextCache)
		if err != nil {
			return err
		}
		plaintext = plain
	default:
		// theirs 版本：服务端当前密文现场解密
		plaintext, err = c.vault.DecryptPlaintext(le.GroupID, id, le.Ciphertext)
		if err != nil {
			return err
		}
	}
	// 重新加密为当前版 push（base_seq=服务端当前 seq）
	kv := le.KeyVersion
	ct, err := c.vault.EncryptPlaintext(le.GroupID, id, plaintext, kv)
	if err != nil {
		return err
	}
	cache, err := c.vault.EncryptCache(plaintext)
	if err != nil {
		return err
	}
	if err := c.local.UpsertLocalEntry(&store.LocalEntry{
		ID: id, GroupID: le.GroupID, Seq: le.Seq, KeyVersion: kv, Ciphertext: ct,
		PlaintextCache: cache, Dirty: true, ConflictOf: "", UpdatedAt: c.now().Unix(),
	}); err != nil {
		return err
	}
	if c.engine != nil && c.syncMode != SyncModeManual {
		go func() { _ = c.engine.SyncNow() }()
	}
	return nil
}

// ---- 同步控制 ----

func (c *Core) StartSync() {
	if c.engine != nil {
		c.startEngineIfAuto()
	}
}

func (c *Core) StopSync() {
	if c.engine != nil {
		c.engine.Stop()
	}
}

// SyncMode 当前同步方式（auto | manual）。
func (c *Core) SyncMode() string { return c.syncMode }

// SetSyncMode 切换同步方式（auto=自动同步 | manual=手动同步）。
// 持久化到本地库；解锁态立即生效：manual 停后台（SSE/心跳/轮询），auto 恢复。
func (c *Core) SetSyncMode(mode string) error {
	if mode != SyncModeAuto && mode != SyncModeManual {
		return fmt.Errorf("非法同步方式: %s", mode)
	}
	c.syncMode = mode
	if err := c.local.SetSyncMode(mode); err != nil {
		return err
	}
	if c.engine != nil {
		if mode == SyncModeManual {
			c.engine.Stop()
		} else {
			c.engine.Start()
		}
	}
	return nil
}

func (c *Core) SyncNow() error {
	if c.engine == nil {
		return errors.New("未解锁或未连接")
	}
	return c.engine.SyncNow()
}

func (c *Core) Status() api.SyncStatus {
	if c.engine == nil {
		return api.SyncStatus{Phase: api.PhaseIdle}
	}
	st := c.engine.Status()
	// 补充组状态/计数
	if pends, err := c.local.ListPendingEntries(); err == nil {
		st.PendingEntries = len(pends)
	}
	if dirty, err := c.local.ListDirtyEntries(); err == nil {
		st.DirtyCount = len(dirty)
	}
	if bad, err := c.local.ListBadSeqs(); err == nil {
		st.BadEntries = len(bad)
	}
	return st
}

// ---- 设置 ----

// GeneratePassword 随机密码生成（§9.1：crypto/rand，可配字符集）。
func (c *Core) GeneratePassword(length int, upper, lower, digits, symbols, excludeAmbiguous bool) (string, error) {
	if length < 4 || length > 128 {
		return "", errors.New("长度须在 4-128 之间")
	}
	var charset []rune
	if lower {
		charset = append(charset, []rune("abcdefghijklmnopqrstuvwxyz")...)
	}
	if upper {
		charset = append(charset, []rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ")...)
	}
	if digits {
		charset = append(charset, []rune("0123456789")...)
	}
	if symbols {
		charset = append(charset, []rune("!@#$%^&*-_=+")...)
	}
	if excludeAmbiguous {
		charset = removeAmbiguous(charset)
	}
	if len(charset) == 0 {
		return "", errors.New("至少选择一种字符集")
	}
	// 拒绝采样消除模偏差：仅接受落在 [0, limit) 的随机字节，
	// limit = 256 - (256 % len(charset))，保证每个字符等概率（§9.1 密码生成）。
	limit := 256 - (256 % len(charset))
	one := make([]byte, 1)
	out := make([]rune, length)
	for i := 0; i < length; i++ {
		for {
			if _, err := rand.Read(one); err != nil {
				return "", err
			}
			if int(one[0]) < limit {
				out[i] = charset[int(one[0])%len(charset)]
				break
			}
		}
	}
	return string(out), nil
}

// removeAmbiguous 去除易混淆字符（0/O/1/l/I 等）。
func removeAmbiguous(cs []rune) []rune {
	amb := map[rune]bool{'0': true, 'O': true, 'o': true, '1': true, 'l': true, 'I': true, '|': true}
	out := cs[:0]
	for _, c := range cs {
		if !amb[c] {
			out = append(out, c)
		}
	}
	return out
}

// ---- 内部辅助 ----

// newUUID 生成 UUID v4（crypto/rand）。
func newUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("core: crypto/rand 失败: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// 确保接口实现（编译期断言）。
var _ api.Core = (*Core)(nil)
