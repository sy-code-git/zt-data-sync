// Package api 客户端核心库对外公开 API 契约（§3.1）。
// 输入输出全部为可序列化结构（JSON），UI 侧不接触密钥类型；
// 第三方 UI（Wails/Fyne/Electron/CLI…）import 后可直接复用加解密/同步/存储全链路。
package api

import (
	"time"

	"passbook/internal/proto"
)

// ---- Vault（解锁/加解密） ----

// EntryView 条目视图（列表/树渲染用，§9.1 plaintext_cache 解出）。
// 明文以 []byte 透传，core 不解析内容；业务结构（type/title/parent_id/fields…）由 UI 解析 Plaintext 获得。
type EntryView struct {
	ID         string `json:"id"`
	GroupID    string `json:"group_id"`
	Plaintext  []byte `json:"plaintext"` // 明文 JSON（UI 自行解析，core 不关心内容）
	Seq        int64  `json:"seq"`
	KeyVersion int    `json:"key_version"`
	UpdatedAt  int64  `json:"updated_at"`
	Deleted    bool   `json:"deleted"`
	ConflictOf string `json:"conflict_of,omitempty"` // 冲突副本归属（§7.3，非空=存在冲突）
	Dirty      bool   `json:"dirty,omitempty"`       // 未推送修改（UI 标记）
	Archived   bool   `json:"archived,omitempty"`    // 组已归档（只读）
}

// UnlockResult 解锁结果（UI 展示用）。
type UnlockResult struct {
	UserID       string `json:"user_id"`
	DeviceID     string `json:"device_id"`
	DeviceName   string `json:"device_name"`
	Groups       int    `json:"groups"`
	NeedRegister bool   `json:"need_register"` // 本地无设备 token，需首次注册设备（§9.1）
}

// PutEntryRequest 保存/更新条目（UI → core）。
// Plaintext 为 UI 序列化好的明文 JSON（任意结构，core 不解析）。
type PutEntryRequest struct {
	ID        string `json:"id"` // 空 = 新建（core 生成 UUID）
	GroupID   string `json:"group_id"`
	Plaintext []byte `json:"plaintext"`
}

// ---- Sync（同步状态） ----

// SyncPhase 同步阶段（UI 状态展示，§9.1）。
type SyncPhase string

const (
	PhaseIdle    SyncPhase = "idle"
	PhasePulling SyncPhase = "pulling"
	PhasePushing SyncPhase = "pushing"
	PhaseRekey   SyncPhase = "rekey"   // 组密钥升级中（§7.2 非阻塞提示）
	PhaseOffline SyncPhase = "offline" // SSE 断开回退轮询
	PhaseError   SyncPhase = "error"
)

// SyncStatus 同步引擎状态快照（UI 轮询展示）。
type SyncStatus struct {
	Phase          SyncPhase        `json:"phase"`
	ServerSeq      int64            `json:"server_seq"`
	LastSeq        int64            `json:"last_seq"`
	LastPullAt     int64            `json:"last_pull_at"`
	Connected      bool             `json:"connected"` // SSE 连接状态
	Groups         []GroupSyncState `json:"groups"`
	PendingEntries int              `json:"pending_entries"` // 等待信封的暂存数
	BadEntries     int              `json:"bad_entries"`     // 同步异常条数
	DirtyCount     int              `json:"dirty_count"`     // 未推送修改数
	Error          string           `json:"error,omitempty"`
}

// GroupSyncState 组同步状态（UI 展示 pending_rekey/archived）。
type GroupSyncState struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	KeyVersion   int    `json:"key_version"`
	PendingRekey bool   `json:"pending_rekey"`
	Archived     bool   `json:"archived"`
}

// ConflictDetail 冲突三方数据（§7.3：base/ours/theirs，供冲突解决页三栏 diff）。
// 三方均为明文 JSON 字节，UI 自行解析做 diff。
type ConflictDetail struct {
	ID     string `json:"id"`
	Base   []byte `json:"base"`   // 编辑前快照（无快照则 nil）
	Ours   []byte `json:"ours"`   // 本地明文（plaintext_cache）
	Theirs []byte `json:"theirs"` // 服务端明文（ciphertext 现场解密）
}

// ---- Core 接口（UI 绑定层调用） ----

// Core 客户端核心库对外接口（§3.1：UI 是薄壳，只调用本接口）。
type Core interface {
	// 生命周期
	// ImportKeyfile 导入私钥备份（换设备/清库后恢复身份）：读 keyfile + 口令解锁 +
	// 存 identity（username/role），返回解锁结果（无 token 时 need_register）。
	ImportKeyfile(path, username, role string, password []byte) (*UnlockResult, error)
	// ExportKeyfile 导出私钥备份（keyfile 格式）到指定路径（换设备恢复用）。
	ExportKeyfile(path string) error
	// GenerateKeypair 首次初始化：生成密钥对 + 加密存本地库 + 解锁。返回公钥 base64（开户用）。
	// role 为身份角色（admin | member）。
	GenerateKeypair(username, role string, password []byte) (string, error)
	// Unlock 解锁（工号+密码，从本地库解私钥）并启动同步。
	Unlock(username string, password []byte) (*UnlockResult, error)
	Lock()
	IsUnlocked() bool
	// Username 当前登录工号（未初始化返回空串）。
	Username() string
	// Role 当前用户角色（admin | member；未解锁返回空串）。
	Role() string
	// 首次注册设备（§9.1：本地无 token 时调用，解锁态）
	RegisterDevice(username, deviceName string) error
	// Bootstrap 管理员首次部署（§6.3）：生成密钥对 + bootstrap token 注册首个 admin
	Bootstrap(username string, password []byte, bootstrapToken, name, deviceName string) error
	// 服务端地址 / 自签 CA 配置（§9.2 / §8.3）
	SetServerURL(url string)
	SetCA(caPath string)
	ServerURL() string
	CA() string
	// 注册凭证密钥（§4.4：管理员首次部署时输入，开户时计算 attestation 用）
	SetRegSecret(regSecret string) error
	HasRegSecret() bool
	// 管理员（§6.3 admin API）
	AdminCreateUser(username, name, publicKey string) (string, error)
	AdminCreateGroup(name string) (string, error)
	AdminArchiveGroup(groupID, confirmName string) error
	AdminUnarchiveGroup(groupID string) error
	AdminAddMember(groupID, userID string) error
	AdminListGroups() ([]proto.GroupInfo, error)
	AdminListUsers() ([]proto.UserInfo, error)
	AdminListMembers(groupID string) ([]proto.GroupMemberInfo, error)
	// AdminRemoveMember 移出组成员（成员名二次确认）。
	AdminRemoveMember(groupID, userID, confirmName string) error
	// AdminListDevices 设备/主机列表（含在线状态/主机名/IP）。
	AdminListDevices() ([]proto.AdminDevice, error)
	// 自动解锁（§9.1，Windows DPAPI）
	TryAutoUnlock() (*UnlockResult, error)
	EnableAutoUnlock(keyfilePath string) error
	DisableAutoUnlock() error
	AutoUnlockEnabled() bool

	// 条目操作（解锁态）
	ListEntries() ([]EntryView, error)
	GetEntry(id string) (*EntryView, error)
	PutEntry(req *PutEntryRequest) error
	DeleteEntry(id string) error
	// 冲突解决：选择采纳版本后 push（§7.3）；manual 非 nil 表示手动编辑后的明文 JSON
	ResolveConflict(id string, useLocal bool, manual []byte) error
	// 冲突详情：三栏 diff 数据（base/ours/theirs，§7.3）
	GetConflict(id string) (*ConflictDetail, error)

	// 同步控制
	StartSync()
	StopSync()
	SyncNow() error
	// SyncMode 当前同步方式（auto | manual）；SetSyncMode 切换并持久化。
	SyncMode() string
	SetSyncMode(mode string) error
	Status() SyncStatus

	// 设置
	// 剪贴板密码 30s 自动清空由 UI 层实现（§9.1），core 提供生成器
	GeneratePassword(length int, upper, lower, digits, symbols, excludeAmbiguous bool) (string, error)
}

// ---- 事件（UI 订阅） ----

// EventType 事件类型（UI 监听刷新）。
type EventType string

const (
	EventEntriesChanged EventType = "entries_changed"
	EventSyncStatus     EventType = "sync_status"
	EventRekeyStarted   EventType = "rekey_started"
	EventRekeyDone      EventType = "rekey_done"
	EventLocked         EventType = "locked"
	EventError          EventType = "error"
)

// Event 事件结构（订阅通道发送）。
type Event struct {
	Type EventType `json:"type"`
	Data any       `json:"data,omitempty"`
	At   time.Time `json:"at"`
}

// Listener 事件监听函数。
type Listener func(Event)
