package store

import (
	"context"
	"database/sql"
	"errors"
)

// 组状态常量（§5.2 groups 表）。
const (
	GroupArchived   = 1
	GroupNotArchived = 0
	RekeyPending    = 1
	RekeyDone       = 0
)

// Group 组行（§5.2 groups 表）。
type Group struct {
	ID           string
	Name         string
	KeyVersion   int
	PendingRekey int
	Archived     int
	CreatedAt    int64
	ArchivedAt   int64 // 0 = 未归档
}

// Entry 条目行（§5.2 entries 表）。
type Entry struct {
	ID         string
	GroupID    string
	Seq        int64
	KeyVersion int
	Ciphertext string
	SizeBytes  int
	Deleted    bool
	UpdatedBy  string
	UpdatedAt  int64
}

// Envelope 信封行（§5.2 key_envelopes 表）。
type Envelope struct {
	GroupID    string
	KeyVersion int
	UserID     string
	WrappedDEK string
	UpdatedAt  int64
}

// GroupMember 组成员行（§5.2 group_members 表）。
type GroupMember struct {
	GroupID   string
	UserID    string
	CreatedAt int64
}

// 用户角色与状态（§5.2 users 表）。
const (
	RoleAdmin     = "admin"
	RoleMember    = "member"
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// 设备状态（§5.2 devices 表）。
const (
	DeviceActive   = "active"
	DeviceDisabled = "disabled"
)

// User 用户行（§5.2 users 表）。
type User struct {
	ID           string
	Username     string // 工号（唯一、不可改、登录标识）；存量用户可空
	Name         string // 显示名（允许重名）
	SM2PublicKey string // base64(DER)
	Attestation  string
	Role         string
	Status       string
	CreatedAt    int64
	RevokedAt    int64 // 0 = 未吊销
}

// Device 设备行（§5.2 devices 表）。
type Device struct {
	ID        string
	UserID    string
	Name      string
	Hostname  string
	LastIP    string
	TokenHash string
	Status    string
	CreatedAt int64
	LastSeen  int64
}

// AuditEvent 审计日志条目（§5.2 audit_log，仅元数据，不记任何明文）。
type AuditEvent struct {
	ID         int64  // 自增主键（查询回填）
	TS         int64
	DeviceID   string
	UserID     string
	Action     string
	EntryID    string // 可空
	IP         string
	DeviceName string
	Hostname   string
	Detail     string // 元数据 JSON
}

// invites / register_requests 状态常量（§6.3 方案 C）。
const (
	InviteUnused = "unused"
	InviteUsed   = "used"
	RegPending   = "pending"
	RegApproved  = "approved"
	RegRejected  = "rejected"
)

// Invite 邀请码（方案 C：绑定工号、一次即废；同工号未开户可重复生成；免审核码 auto_approve=1）。
type Invite struct {
	ID          string
	Code        string
	Username    string // 绑定工号（唯一、不可改）
	AutoApprove int    // 1=免审核（提交申请即自动开户）
	Status      string // unused | used
	ExpiresAt   int64
	CreatedBy   string
	CreatedAt   int64
	UsedAt      int64
}

// RegisterRequest 注册申请（用户凭邀请码提交；管理员审核通过即开户）。
type RegisterRequest struct {
	ID           string
	InviteCode   string
	Username     string
	SM2PublicKey string
	DeviceName   string
	IP           string // 申请来源 IP（服务端记录，审核时核对）
	Status       string // pending | approved | rejected
	CreatedAt    int64
	ReviewedBy   string
	ReviewedAt   int64
}

// Tx 事务内操作接口（§6.1：所有写操作包事务；写方法接收 tx）。
type Tx interface {
	// ---- 全局序列号 ----
	NextSeq() (int64, error)

	// ---- users ----
	CreateUser(u *User) error
	SetUserRevoked(userID string, revokedAt int64) error
	ReplaceUserPublicKey(userID, pubKey, attestation string) error

	// ---- devices ----
	CreateDevice(d *Device) error
	DisableDevice(deviceID string) error
	DisableUserDevices(userID string) error
	UpdateDeviceSeen(deviceID, hostname string, lastSeen int64) error
	UpdateDeviceIP(deviceID, ip string) error
	// RefreshTokenHash 轮换设备 token（§6.3 POST /auth/refresh：旧 token 即刻作废）。
	RefreshTokenHash(deviceID, tokenHash string) error
	Audit(e *AuditEvent) error

	// ---- groups ----
	CreateGroup(g *Group) error
	// SetGroupRekey 置位/清除 pending_rekey。
	SetGroupRekey(groupID string, pending int) error
	// SetGroupArchived 归档/重启（archived=1 时记录 archived_at）。
	SetGroupArchived(groupID string, archived int, at int64) error
	// SetGroupKeyVersion 升 kv（rekey 收尾，§6.3）。
	SetGroupKeyVersion(groupID string, kv int) error

	// ---- group_members ----
	AddGroupMember(gm *GroupMember) error
	RemoveGroupMember(groupID, userID string) error

	// ---- entries ----
	// UpsertEntry 新增（base_seq=0 且 id 不存在）或更新（base_seq=当前 seq）条目。
	// 返回新分配的 seq；id 已存在且 base_seq=0 → ErrConstraintUnique（40903 语义）。
	UpsertEntry(e *Entry) (int64, error)

	// ---- key_envelopes ----
	// UpsertEnvelope 入伙追加信封（kv 不变；已存在则 ErrConstraintUnique → 40904）。
	UpsertEnvelope(env *Envelope) error
	// ReplaceEnvelopes rekey 替换：删除该组该 kv 全部信封后写入新集合（同一事务）。
	ReplaceEnvelopes(groupID string, kv int, envs []Envelope, at int64) error
	// DeleteUserEnvelopes 删除用户全部组全部 kv 的信封（revoke / keyfile-reset）。
	DeleteUserEnvelopes(userID string) error
	// DeleteGroupUserEnvelopes 删除用户在指定组的全部信封（移出组）。
	DeleteGroupUserEnvelopes(groupID, userID string) error
	// DeleteOldKVEnvelopes 删除指定组所有 kv < 给定版本的旧信封（收尾，§6.3）。
	DeleteOldKVEnvelopes(groupID string, newKV int) error
	// DeleteOldTombstones 物理删除早于 before 的墓碑（§7.4 定时清理）。
	DeleteOldTombstones(before int64) error
}

// Store 存储接口（读方法 + 事务）。
type Store interface {
	// ---- 生命周期 ----
	Close() error
	Migrate() error
	WithTx(ctx context.Context, fn func(tx Tx) error) error

	// ---- users / devices 读 ----
	GetUserCount() (int, error)
	GetUserByID(id string) (*User, error)
	GetUserByName(name string) (*User, error)
	GetUserByUsername(username string) (*User, error) // 工号登录名（唯一）
	GetUserByPublicKey(pubKey string) (*User, error)
	GetDeviceByTokenHash(hash string) (*Device, error)
	GetDeviceByID(id string) (*Device, error)
	ListDevicesByUser(userID string) ([]Device, error)
	ListActiveUsers() ([]User, error) // GET /users（§6.3：仅 active）

	// ---- groups 读 ----
	GetGroup(groupID string) (*Group, error)
	ListGroups() ([]Group, error)
	GetGroupMember(groupID, userID string) (bool, error)
	ListGroupMembers(groupID string) ([]GroupMember, error)
	ListUserGroups(userID string) ([]Group, error) // 用户所在全部组（含归档）
	// CountEntriesWithKV 组内未到达指定 kv 的条目数（不含墓碑，§6.3 收尾判定）。
	CountEntriesBelowKV(groupID string, kv int) (int, error)

	// ---- entries 读 ----
	// GetEntry 按 id 查条目（push 冲突检测 base_seq 用）。
	GetEntry(entryID string) (*Entry, error)
	// PullChanges 增量拉取：since 之后、按 seq 升序、上限 limit；groupID 为空则返回全部（调用方按成员过滤）。
	PullChanges(since int64, limit int) ([]Entry, error)
	// PullGroupChanges 指定组增量拉取（since=0 & group_id=GID 全量）。
	PullGroupChanges(groupID string, since int64, limit int) ([]Entry, error)
	// GetServerSeq 当前全局 seq（seq_counter 值）。
	GetServerSeq() (int64, error)
	// CountEntries 组内条目总数（用于校验 500 条上限等）。
	CountEntries(groupID string) (int, error)

	// ---- key_envelopes 读 ----
	// GetUserEnvelopes 用户全部信封（GET /keys/mine，§6.2）。
	GetUserEnvelopes(userID string) ([]Envelope, error)
	// GetGroupEnvelopes 组某 kv 全部信封。
	GetGroupEnvelopes(groupID string, kv int) ([]Envelope, error)
	// HasEnvelope 组某 kv 下用户是否有信封。
	HasEnvelope(groupID string, kv int, userID string) (bool, error)

	// ---- audit 读 ----
	// QueryAudit 审计查询（from/to/user_id/action 过滤，按 ts 倒序，上限 limit）。
	QueryAudit(from, to int64, userID, action string, limit int) ([]AuditEvent, error)
	// ListAllDevices 全部设备（admin 设备列表，§6.3）。
	ListAllDevices() ([]Device, error)

	// ---- invites / register_requests（方案 C：邀请码 + 审核制，§6.3）----
	CreateInvite(inv *Invite) error
	GetInviteByCode(code string) (*Invite, error)
	MarkInviteUsed(code string, usedAt int64) error
	ListInvites() ([]Invite, error)
	CreateRegisterRequest(r *RegisterRequest) error
	GetRegisterRequestByInvite(code string) (*RegisterRequest, error)
	GetRegisterRequestByID(id string) (*RegisterRequest, error)
	ListRegisterRequests(status string) ([]RegisterRequest, error)
	UpdateRegisterRequest(id, status, reviewedBy string, reviewedAt int64) error
}

// 哨兵错误。
var (
	ErrTxDone          = errors.New("store: 事务已结束")
	ErrConstraintUnique = errors.New("store: 唯一约束冲突")
	ErrConstraintFK     = errors.New("store: 外键约束冲突")
	ErrNoRows           = sql.ErrNoRows
)
