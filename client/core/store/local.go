// Package store 客户端本地存储（§9.1）。
// 本地 SQLite（modernc.org/sqlite），敏感列用 KEK 派生密钥 SM4-GCM 加密。
// 与 server/store 完全独立（本地 schema 不同，见 §9.1 本地存储）。
package store

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// LocalStore 本地存储接口（可换内存假实现测试）。
type LocalStore interface {
	Close() error
	Migrate() error

	// ---- 设备状态（device_state，§9.1） ----
	GetDeviceState() (*DeviceState, error)
	SetDeviceState(d *DeviceState) error
	// ---- 本地身份（identity，§9.1 方案 A：私钥加密存本地库） ----
	GetIdentity() (*Identity, error)
	SetIdentity(i *Identity) error
	// GetServerURL 读取服务端地址配置（§9.2；未配置返回空串）。
	GetServerURL() (string, error)
	// SetServerURL 持久化服务端地址配置（§9.2）。
	SetServerURL(url string) error
	// GetCA 读取自签 CA 证书路径（§8.3；未配置返回空串 = 走系统默认验证）。
	GetCA() (string, error)
	// SetCA 持久化自签 CA 证书路径（§8.3）。
	SetCA(path string) error
	// GetRegSecretEnc 读取注册凭证密钥（加密 blob，§4.4；未配置返回 nil）。
	GetRegSecretEnc() ([]byte, error)
	// SetRegSecretEnc 持久化注册凭证密钥（加密 blob，§4.4）。
	SetRegSecretEnc(enc []byte) error
	// GetSyncMode 读取同步方式（auto=自动同步 | manual=手动同步；未配置默认 auto）。
	GetSyncMode() (string, error)
	// SetSyncMode 持久化同步方式（auto/manual）。
	SetSyncMode(mode string) error

	// ---- 自动解锁（app_config 扩展，§9.1 自动解锁） ----
	// GetAutoUnlock 读取自动解锁配置（未配置返回零值，不报错）。
	GetAutoUnlock() (*AutoUnlockConfig, error)
	// SetAutoUnlock 持久化自动解锁配置（keyfile 路径 / 开关 / DPAPI 保护的 KEK blob）。
	SetAutoUnlock(c *AutoUnlockConfig) error

	// ---- 同步状态（sync_state，§9.1） ----
	GetLastSeq() (int64, error)
	SetLastSeq(seq int64) error

	// ---- 组状态（group_state，§9.1：archived 翻转检测 / 新组判定） ----
	GetGroupState(gid string) (*GroupState, error)
	SetGroupState(g *GroupState) error
	ListGroupStates() ([]GroupState, error)

	// ---- 本地条目（local_entries，§9.1） ----
	UpsertLocalEntry(e *LocalEntry) error
	GetLocalEntry(id string) (*LocalEntry, error)
	ListLocalEntries() ([]LocalEntry, error)
	ListDirtyEntries() ([]LocalEntry, error)
	SetDirty(id string, dirty bool) error
	SetBaseEnc(id string, baseEnc []byte) error
	SetPlaintextCache(id string, cache []byte) error
	SetConflict(id string, conflictOf string) error
	RemoveLocalEntry(id string) error

	// ---- 等待信封暂存（pending_entries，§9.1） ----
	PutPendingEntry(e *PendingEntry) error
	GetPendingEntry(id string) (*PendingEntry, error)
	ListPendingEntries() ([]PendingEntry, error)
	DeletePendingEntry(id string) error

	// ---- DEK 缓存（key_cache，§9.1，dek_enc 用 KEK 加密） ----
	PutDEK(groupID string, kv int, dekEnc []byte) error
	GetDEK(groupID string, kv int) ([]byte, error)
	DeleteGroupDEKs(groupID string) error
	ListDEKGroupIDs() ([]string, error)
	// ListDEKVersions 返回各组已持有信封的最高 key_version（X-Key-Versions 声明，§6.3）。
	ListDEKVersions() (map[string]int, error)

	// ---- 坏密文跳过清单（bad_seq，§9.1） ----
	MarkBadSeq(seq int64) error
	IncrementBadSeq(seq int64) error
	ListBadSeqs() (map[int64]int, error)
	ClearBadSeq(seq int64) error

	// ---- 回收站（recycle_bin，§7.4 本地 30 天） ----
	PutRecycleBin(id string, ciphertext string, deletedAt int64) error
}

// AutoUnlockConfig 自动解锁配置（§9.1，app_config 单行扩展）。
type AutoUnlockConfig struct {
	KeyfilePath string // 已导入 keyfile 绝对路径（自动解锁免口令定位）
	Enabled     bool   // 是否开启自动解锁
	KEKBlob     []byte // DPAPI 保护的 KEK blob（仅 Windows；关闭/非 Windows 为 nil）
}

// DeviceState 设备状态行（§9.1 device_state）。
type DeviceState struct {
	DeviceID  string
	TokenEnc  []byte // KEK 派生密钥加密
	ExpiresAt int64
}

// Identity 本地身份行（§9.1 identity，方案 A：私钥加密存本地库，替代外部 keyfile 文件）。
type Identity struct {
	Username    string // 工号（登录标识，唯一、不可改）
	Role        string // 身份角色：admin（管理员）| member（普通用户）
	KeyfileBlob []byte // keyfile JSON（含 KDF salt/iter + SM4-GCM 加密的私钥 DER）
	PublicKey   string // base64(DER) SM2 公钥（导出给管理员开户）
}

// GroupState 本地组状态行（§9.1 group_state）。
type GroupState struct {
	GroupID       string
	Name          string // 组名（0007 起本地记录，UI 组回退链展示用）
	Archived      bool
	KeyVersion    int
	InitializedAt int64
}

// LocalEntry 本地条目行（§9.1 local_entries）。
type LocalEntry struct {
	ID             string
	GroupID        string
	Seq            int64
	KeyVersion     int
	Ciphertext     string
	PlaintextCache []byte // KEK 派生密钥加密
	BaseEnc        []byte // 冲突三路合并的"共同祖先"快照（KEK 派生密钥加密）
	Dirty          bool
	Deleted        bool   // 本地墓碑（待推送删除，§7.2 4d）
	ConflictOf     string // 冲突副本归属的 entry_id（§7.3）
	UpdatedAt      int64
}

// PendingEntry 等待信封暂存行（§9.1 pending_entries）。
type PendingEntry struct {
	ID         string
	GroupID    string
	Seq        int64
	KeyVersion int
	Ciphertext string
	UpdatedAt  int64
}

// sqliteLocal LocalStore 的 SQLite 实现。
type sqliteLocal struct {
	db *sql.DB
}

// OpenLocal 打开本地数据库（path 或 ":memory:"）。
func OpenLocal(path string) (LocalStore, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &sqliteLocal{db: db}, nil
}

func (s *sqliteLocal) Close() error { return s.db.Close() }

func (s *sqliteLocal) Migrate() error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: 打开迁移目录: %w", err)
	}
	return applyMigrations(sub, s.db)
}

// applyMigrations 版本化迁移（schema_migrations 只增不改，§14.1 迁移约定）。
// 每个迁移在事务内执行并记录版本，重复启动幂等跳过已应用版本。
func applyMigrations(fsys fs.FS, db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version   TEXT PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("store: 创建 schema_migrations: %w", err)
	}

	applied := map[string]bool{}
	rows, err := db.Query(`SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("store: 查询已应用迁移: %w", err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			_ = rows.Close()
			return err
		}
		applied[v] = true
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("store: 读取迁移目录: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && len(e.Name()) > 4 && e.Name()[len(e.Name())-4:] == ".sql" {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		if applied[name] {
			continue
		}
		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("store: 读取迁移 %s: %w", name, err)
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(content)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: 执行迁移 %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, unixepoch())`, name); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: 记录迁移 %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
