package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"passbook/client/core"
	"passbook/client/core/api"
	"passbook/client/core/store"
	"passbook/internal/proto"
)

// App Wails 绑定层——UI 壳只转发 core 调用，不实现任何业务逻辑（§14.1 1.8）。
// 绑定方法签名不出现密钥类型：口令以 string 传入（前端不触碰密钥材料本身）。
type App struct {
	ctx       context.Context
	dataDir   string
	local     store.LocalStore
	core      *core.Core
	reinit    bool // --reinit 启动参数：强制重启初始化配置引导（§9.2）
	adminMode bool // --admin 启动参数：管理员模式（登录后进管理面板）

	keyfilePath string // 当前已解锁的 keyfile 路径（开启自动解锁时记录，§9.1）
}

// NewApp 构造绑定层（dataDir 为本地数据目录，默认与可执行文件同目录）。
func NewApp(dataDir string) *App {
	if dataDir == "" {
		// 默认：与可执行文件同目录（绿色部署，数据随程序走，便于迁移）
		if exe, err := os.Executable(); err == nil {
			dataDir = filepath.Dir(exe)
		} else {
			home, _ := os.UserHomeDir()
			dataDir = filepath.Join(home, ".passbook")
		}
	}
	return &App{dataDir: dataDir}
}

// IsReinit 是否以 --reinit 启动（前端据此强制走首次引导页，忽略已存地址）。
func (a *App) IsReinit() bool { return a.reinit }

// IsAdminMode 是否以 --admin 启动（管理员模式：登录后进管理面板）。
func (a *App) IsAdminMode() bool { return a.adminMode }

// startup Wails 启动回调：打开本地库并迁移、构造 core。
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	if err := os.MkdirAll(a.dataDir, 0o700); err != nil {
		runtime.LogErrorf(ctx, "创建数据目录失败: %v", err)
		return
	}
	// 管理员与普通用户使用不同数据库文件，互不干扰（同一客户端程序，不同库）
	dbName := "user.db"
	if a.adminMode {
		dbName = "admin.db"
	}
	local, err := store.OpenLocal(filepath.Join(a.dataDir, dbName))
	if err != nil {
		runtime.LogErrorf(ctx, "打开本地库失败: %v", err)
		return
	}
	if err := local.Migrate(); err != nil {
		runtime.LogErrorf(ctx, "本地库迁移失败: %v", err)
		return
	}
	a.local = local
	serverURL, _ := local.GetServerURL()
	a.core = core.New(local, serverURL)
	// 订阅 core 事件 → 转发给前端（解锁页/列表页监听刷新，§9.1）
	a.core.Subscribe(func(ev api.Event) {
		runtime.EventsEmit(ctx, "core:event", ev)
	})
}

// ---- 服务端地址配置（§9.2） ----

// GetServerURL 读取已存服务端地址（首次引导判断用）。
func (a *App) GetServerURL() (string, error) {
	if a.local == nil {
		return "", errors.New("本地库未就绪")
	}
	url, err := a.local.GetServerURL()
	if err != nil {
		return "", err
	}
	return url, nil
}

// SetServerURL 持久化服务端地址（首次引导验证通过后调用）。
func (a *App) SetServerURL(url string) error {
	if a.local == nil {
		return errors.New("本地库未就绪")
	}
	if err := a.local.SetServerURL(url); err != nil {
		return err
	}
	a.core.SetServerURL(url)
	return nil
}

// GetCA 读取已存自签 CA 证书路径（§8.3；空 = 系统默认验证）。
func (a *App) GetCA() (string, error) {
	if a.local == nil {
		return "", errors.New("本地库未就绪")
	}
	return a.local.GetCA()
}

// SetCA 持久化自签 CA 证书路径（§8.3）。
func (a *App) SetCA(caPath string) error {
	if a.local == nil {
		return errors.New("本地库未就绪")
	}
	if err := a.local.SetCA(caPath); err != nil {
		return err
	}
	a.core.SetCA(caPath)
	return nil
}

// VerifyServer 验证服务端地址连通（无鉴权 /healthz，§9.2 首次引导探活）。
// caPath 非空时用自签 CA pinning 验证（§8.3），否则系统默认验证。
func (a *App) VerifyServer(url, caPath string) error {
	if url == "" {
		return errors.New("服务端地址为空")
	}
	var hc *api.HTTPClient
	var err error
	if caPath != "" {
		hc, err = api.NewHTTPClientWithCA(url, "", caPath)
	} else {
		hc = api.NewHTTPClient(url, "")
	}
	if err != nil {
		return fmt.Errorf("CA 证书加载失败: %w", err)
	}
	if err := hc.Health(); err != nil {
		return fmt.Errorf("无法连接服务端 %s: %w", url, err)
	}
	return nil
}

// ---- 生命周期 ----

// ImportKeyfile 导入私钥备份（恢复身份 + 解锁；本地无 token 时返回 need_register）。
func (a *App) ImportKeyfile(path, username, role, password string) (*api.UnlockResult, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.ImportKeyfile(path, username, role, []byte(password))
}

// GenerateKeypair 首次初始化身份（生成密钥对 + 加密存本地库 + 解锁）。返回公钥（开户用）。
func (a *App) GenerateKeypair(username, role, password string) (string, error) {
	if a.core == nil {
		return "", errors.New("核心库未就绪")
	}
	return a.core.GenerateKeypair(username, role, []byte(password))
}

// Username 当前登录工号（未初始化返回空串）。
func (a *App) Username() string {
	if a.core == nil {
		return ""
	}
	return a.core.Username()
}

// Role 当前用户角色（admin | member）。
func (a *App) Role() string {
	if a.core == nil {
		return ""
	}
	return a.core.Role()
}

// Unlock 解锁并启动同步（§9.2：工号+密码，从本地库解私钥）。
func (a *App) Unlock(username, password string) (*api.UnlockResult, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.Unlock(username, []byte(password))
}

// RegisterDevice 首次注册设备（§9.1：解锁后本地无 token 时调用，需工号）。
func (a *App) RegisterDevice(username, deviceName string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.RegisterDevice(username, deviceName)
}

// Bootstrap 管理员首次部署（§6.3：生成密钥对 + bootstrap token 注册首个 admin）。
func (a *App) Bootstrap(username, password, bootstrapToken, name, deviceName string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.Bootstrap(username, []byte(password), bootstrapToken, name, deviceName)
}

// SetRegSecret 保存注册凭证密钥（PB_REG_SECRET，管理员首次部署时输入，§4.4）。
func (a *App) SetRegSecret(regSecret string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.SetRegSecret(regSecret)
}

// HasRegSecret 是否已配置注册凭证密钥。
func (a *App) HasRegSecret() bool {
	if a.core == nil {
		return false
	}
	return a.core.HasRegSecret()
}

// ---- 管理员（§6.3 admin API） ----

// AdminCreateUser 开户（用 REG_SECRET 计算 attestation）。
func (a *App) AdminCreateUser(username, name, publicKey string) (string, error) {
	if a.core == nil {
		return "", errors.New("核心库未就绪")
	}
	return a.core.AdminCreateUser(username, name, publicKey)
}

// AdminCreateGroup 建组。
func (a *App) AdminCreateGroup(name string) (string, error) {
	if a.core == nil {
		return "", errors.New("核心库未就绪")
	}
	return a.core.AdminCreateGroup(name)
}

// AdminArchiveGroup 归档（删除）组（组名二次确认）。
func (a *App) AdminArchiveGroup(groupID, confirmName string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.AdminArchiveGroup(groupID, confirmName)
}

// AdminUnarchiveGroup 恢复归档组。
func (a *App) AdminUnarchiveGroup(groupID string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.AdminUnarchiveGroup(groupID)
}

// AdminAddMember 加组成员。
func (a *App) AdminAddMember(groupID, userID string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.AdminAddMember(groupID, userID)
}

// AdminRevoke 吊销成员（成员名二次确认；返回吊销后已无成员的组名，供空组告警）。
func (a *App) AdminRevoke(userID, confirmName string) ([]string, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminRevoke(userID, confirmName)
}

// RegisterRequest 提交注册申请（免登录，凭邀请码；pending=待审核 / approved=已开户）。
func (a *App) RegisterRequest(inviteCode, username, publicKey, deviceName string) (string, string, error) {
	if a.core == nil {
		return "", "", errors.New("核心库未就绪")
	}
	return a.core.RegisterRequest(inviteCode, username, publicKey, deviceName)
}

// RegisterStatus 查询审核状态（免登录，按邀请码）。
func (a *App) RegisterStatus(inviteCode string) (string, error) {
	if a.core == nil {
		return "", errors.New("核心库未就绪")
	}
	return a.core.RegisterStatus(inviteCode)
}

// AdminCreateInvite 生成邀请码（绑定工号；autoApprove=免审核；ttlDays=0 默认 3 天）。
func (a *App) AdminCreateInvite(username string, autoApprove bool, ttlDays int) (*proto.InviteOut, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminCreateInvite(username, autoApprove, ttlDays)
}

// AdminListInvites 邀请码列表。
func (a *App) AdminListInvites() ([]proto.InviteOut, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminListInvites()
}

// AdminListRegisterRequests 注册申请列表（status 空=全部）。
func (a *App) AdminListRegisterRequests(status string) ([]proto.RegisterRequestOut, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminListRegisterRequests(status)
}

// AdminApproveRegisterRequest 审核通过（=开户，name 显示名，空默认工号）。
func (a *App) AdminApproveRegisterRequest(id, name string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.AdminApproveRegisterRequest(id, name)
}

// AdminRejectRegisterRequest 拒绝申请。
func (a *App) AdminRejectRegisterRequest(id string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.AdminRejectRegisterRequest(id)
}

// AdminListGroups 组列表。
func (a *App) AdminListGroups() ([]proto.GroupInfo, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminListGroups()
}

// AdminListUsers 用户列表（含工号）。
func (a *App) AdminListUsers() ([]proto.UserInfo, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminListUsers()
}

// AdminListMembers 组成员清单。
func (a *App) AdminListMembers(groupID string) ([]proto.GroupMemberInfo, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminListMembers(groupID)
}

// AdminRemoveMember 移出组成员（成员名二次确认）。
func (a *App) AdminRemoveMember(groupID, userID, confirmName string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.AdminRemoveMember(groupID, userID, confirmName)
}

// AdminListDevices 设备/主机列表（含在线状态/主机名/IP）。
func (a *App) AdminListDevices() ([]proto.AdminDevice, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.AdminListDevices()
}

// ---- 自动解锁（§9.1，Windows DPAPI） ----

// TryAutoUnlock 尝试 DPAPI 免口令解锁（启动/锁屏恢复时调用）。
func (a *App) TryAutoUnlock() (*api.UnlockResult, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	res, err := a.core.TryAutoUnlock()
	if err == nil {
		// 自动解锁进入时 keyfilePath 尚未记录，从自动解锁配置回填，
		// 保证后续设置页「关闭再开启」不因路径缺失报错（§9.1）。
		if cfg, e := a.local.GetAutoUnlock(); e == nil && cfg != nil {
			a.keyfilePath = cfg.KeyfilePath
		}
	}
	return res, err
}

// EnableAutoUnlock 开启自动解锁（设置页开关；须解锁态，用当前 keyfile 路径）。
func (a *App) EnableAutoUnlock() error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	if a.keyfilePath == "" {
		return errors.New("未记录 keyfile 路径，请先解锁")
	}
	return a.core.EnableAutoUnlock(a.keyfilePath)
}

// DisableAutoUnlock 关闭自动解锁（立即失效）。
func (a *App) DisableAutoUnlock() error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.DisableAutoUnlock()
}

// AutoUnlockEnabled 是否已开启自动解锁。
func (a *App) AutoUnlockEnabled() bool {
	return a.core != nil && a.core.AutoUnlockEnabled()
}

// Lock 锁定（停同步 + Wipe 内存密钥，§9.1）。
func (a *App) Lock() {
	if a.core != nil {
		a.core.Lock()
	}
}

// IsUnlocked 是否解锁态。
func (a *App) IsUnlocked() bool {
	return a.core != nil && a.core.IsUnlocked()
}

// ---- 条目操作 ----

// ListEntries 列出已解密条目（树形浏览数据源，§9.1）。
func (a *App) ListEntries() ([]api.EntryView, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.ListEntries()
}

// GetEntry 取单条条目。
func (a *App) GetEntry(id string) (*api.EntryView, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.GetEntry(id)
}

// PutEntry 保存/更新条目（本地加密 + 待推送）。
func (a *App) PutEntry(req *api.PutEntryRequest) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.PutEntry(req)
}

// DeleteEntry 删除条目（墓碑 push）。
func (a *App) DeleteEntry(id string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.DeleteEntry(id)
}

// ResolveConflict 冲突解决（§7.3 三路合并）；manual 非 nil 表示手动编辑后的明文 JSON。
func (a *App) ResolveConflict(id string, useLocal bool, manual []byte) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.ResolveConflict(id, useLocal, manual)
}

// GetConflict 冲突三方数据（base/ours/theirs，冲突解决页三栏 diff）。
func (a *App) GetConflict(id string) (*api.ConflictDetail, error) {
	if a.core == nil {
		return nil, errors.New("核心库未就绪")
	}
	return a.core.GetConflict(id)
}

// ---- 同步控制 ----

// SyncNow 手动触发一轮同步。
func (a *App) SyncNow() error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.SyncNow()
}

// SyncMode 当前同步方式（auto | manual）。
func (a *App) SyncMode() string {
	if a.core == nil {
		return "auto"
	}
	return a.core.SyncMode()
}

// SetSyncMode 切换同步方式（auto=自动同步 | manual=手动同步）。
func (a *App) SetSyncMode(mode string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.SetSyncMode(mode)
}

// Status 同步状态快照（UI 轮询展示）。
func (a *App) Status() api.SyncStatus {
	if a.core == nil {
		return api.SyncStatus{Phase: api.PhaseIdle}
	}
	return a.core.Status()
}

// ---- 设置 ----

// GeneratePassword 随机密码生成（§9.1 可配字符集）。
func (a *App) GeneratePassword(length int, upper, lower, digits, symbols, excludeAmbiguous bool) (string, error) {
	if a.core == nil {
		return "", errors.New("核心库未就绪")
	}
	return a.core.GeneratePassword(length, upper, lower, digits, symbols, excludeAmbiguous)
}

// DataDir 数据目录（设置页展示/备份提示用）。
func (a *App) DataDir() string { return a.dataDir }

// OpenFileDialog 选择文件（keyfile 浏览按钮；Wails v2 前端 runtime 无原生对话框，经绑定层转发）。
func (a *App) OpenFileDialog(title string) (string, error) {
	if a.ctx == nil {
		return "", errors.New("窗口上下文未就绪")
	}
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
		Filters: []runtime.FileFilter{
			{DisplayName: "Keyfile", Pattern: "*.key;*.kf;*.dat;*"},
		},
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// SaveFileDialog 选择保存路径（导出私钥备份用）。
func (a *App) SaveFileDialog(title string) (string, error) {
	if a.ctx == nil {
		return "", errors.New("窗口上下文未就绪")
	}
	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           title,
		DefaultFilename: "passbook-backup.key",
		Filters: []runtime.FileFilter{
			{DisplayName: "Keyfile", Pattern: "*.key"},
		},
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// ExportKeyfile 导出私钥备份（keyfile 格式）到指定路径。
func (a *App) ExportKeyfile(path string) error {
	if a.core == nil {
		return errors.New("核心库未就绪")
	}
	return a.core.ExportKeyfile(path)
}
