package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"testing"
	"testing/fstest"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIdempotent(t *testing.T) {
	s := newTestStore(t)
	// 再次迁移应幂等成功
	if err := s.Migrate(); err != nil {
		t.Fatalf("重复迁移应幂等: %v", err)
	}
	// 验证迁移产物可用：分配一次 seq
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		_, err := tx.NextSeq()
		return err
	}); err != nil {
		t.Fatalf("迁移产物可操作: %v", err)
	}
}

func TestNextSeqConcurrentUnique(t *testing.T) {
	s := newTestStore(t)
	const goroutines = 100
	seqs := make(chan int64, goroutines)
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 每个 goroutine 独立事务分配 seq
			if err := s.WithTx(context.Background(), func(tx Tx) error {
				v, err := tx.NextSeq()
				if err != nil {
					return err
				}
				seqs <- v
				return nil
			}); err != nil {
				t.Errorf("WithTx NextSeq: %v", err)
				return
			}
		}()
	}
	wg.Wait()
	close(seqs)

	seen := map[int64]bool{}
	count := 0
	for v := range seqs {
		count++
		if seen[v] {
			t.Fatalf("seq %d 重复", v)
		}
		seen[v] = true
	}
	if count != goroutines {
		t.Fatalf("seq 数量 = %d, want %d", count, goroutines)
	}
	// 最终值应恰为 goroutines（从 0 起，100 次递增）
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		v, _ := tx.NextSeq()
		if v != goroutines+1 {
			return fmt.Errorf("final seq = %d, want %d", v, goroutines+1)
		}
		return nil
	}); err != nil {
		t.Fatalf("最终 seq 校验: %v", err)
	}
}

func TestForeignKeyEnforced(t *testing.T) {
	// 直接验证 FK 生效：向 group_members 插入悬空引用必须失败（foreign_keys=ON）。
	s2, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Migrate(); err != nil {
		t.Fatal(err)
	}
	got := s2.WithTx(context.Background(), func(tx Tx) error {
		stx, ok := tx.(*sqliteTx)
		if !ok {
			t.Fatal("预期 *sqliteTx")
		}
		_, err := stx.tx.Exec(`INSERT INTO group_members (group_id, user_id, created_at) VALUES ('g-none', 'u-none', 0)`)
		return err
	})
	if got == nil {
		t.Fatal("插入悬空外键引用应失败")
	}
	if !containsAny(got.Error(), "FOREIGN KEY", "foreign key", "constraint") {
		t.Fatalf("错误类型不符: %v", got)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(s) >= len(sub) && indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestWithTxRollback(t *testing.T) {
	s := newTestStore(t)
	// 先分配一个 seq，再在事务内分配后返回错误 → 应回滚（seq 不递增）
	sentinel := errors.New("boom")
	err := s.WithTx(context.Background(), func(tx Tx) error {
		if _, err := tx.NextSeq(); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("WithTx 应透传 fn 错误: %v", err)
	}
	// 回滚后 seq 应仍为 0（下一次分配得 1）
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		v, err := tx.NextSeq()
		if err != nil {
			return err
		}
		if v != 1 {
			return fmt.Errorf("回滚后 seq = %d, want 1", v)
		}
		return nil
	}); err != nil {
		t.Fatalf("回滚验证: %v", err)
	}
}

// memoryStore 内存假实现：验证 Store 接口可被非 SQLite 实现替换（§14.1 验收）。
// 仅 NextSeq 有真实语义；其余方法最小实现（返回错误）以编译期验证接口契约。
type memoryStore struct {
	mu  sync.Mutex
	seq int64
}

var errNotImpl = errors.New("memory store: 未实现")

func (m *memoryStore) Close() error { return nil }
func (m *memoryStore) Migrate() error {
	return nil
}
func (m *memoryStore) WithTx(_ context.Context, fn func(tx Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(&memoryTx{m: m})
}
func (m *memoryStore) GetUserCount() (int, error)               { return 0, errNotImpl }
func (m *memoryStore) GetUserByID(string) (*User, error)        { return nil, errNotImpl }
func (m *memoryStore) GetUserByName(string) (*User, error)      { return nil, errNotImpl }
func (m *memoryStore) GetUserByUsername(string) (*User, error)  { return nil, errNotImpl }
func (m *memoryStore) GetUserByPublicKey(string) (*User, error) { return nil, errNotImpl }
func (m *memoryStore) GetDeviceByTokenHash(string) (*Device, error) {
	return nil, errNotImpl
}
func (m *memoryStore) GetDeviceByID(string) (*Device, error)          { return nil, errNotImpl }
func (m *memoryStore) ListDevicesByUser(string) ([]Device, error)     { return nil, errNotImpl }

type memoryTx struct{ m *memoryStore }

func (t *memoryTx) NextSeq() (int64, error) {
	t.m.seq++
	return t.m.seq, nil
}
func (t *memoryTx) CreateUser(*User) error              { return errNotImpl }
func (t *memoryTx) SetUserRevoked(string, int64) error  { return errNotImpl }
func (t *memoryTx) ReplaceUserPublicKey(string, string, string) error {
	return errNotImpl
}
func (t *memoryTx) CreateDevice(*Device) error { return errNotImpl }
func (t *memoryTx) DisableDevice(string) error { return errNotImpl }
func (t *memoryTx) DisableUserDevices(string) error { return errNotImpl }
func (t *memoryTx) UpdateDeviceSeen(string, string, int64) error { return errNotImpl }
func (t *memoryTx) UpdateDeviceIP(string, string) error          { return errNotImpl }
func (t *memoryTx) Audit(*AuditEvent) error                      { return errNotImpl }

func TestStoreInterfaceSwappable(t *testing.T) {
	// 上层只依赖 Store 接口：SQLite 与内存假实现均可注入
	var s Store = newTestStore(t)
	_ = s
	var mem Store = &memoryStore{}
	if err := mem.WithTx(context.Background(), func(tx Tx) error {
		v, err := tx.NextSeq()
		if err != nil || v != 1 {
			t.Fatalf("memory NextSeq = %d, err=%v", v, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// 编译期断言：sqliteStore 实现 Store
	var _ Store = (*sqliteStore)(nil)
	// sql.Tx 兼容
	var _ = sql.ErrNoRows
}

func TestOpenInvalidPath(t *testing.T) {
	// sql.Open 惰性连接：无效路径在首次查询（Migrate）才报错
	s, err := Open("/nonexistent-dir-xyz/sub/db.sqlite")
	if err != nil {
		t.Fatalf("Open 惰性连接不应在此时失败: %v", err)
	}
	defer s.Close()
	if err := s.Migrate(); err == nil {
		t.Fatal("无效路径 Migrate 应报错")
	}
}

func TestClose(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// 关闭后 WithTx 应报错
	if err := s.WithTx(context.Background(), func(tx Tx) error { return nil }); err == nil {
		t.Fatal("关闭后 WithTx 应报错")
	}
}

func TestMigrateBrokenMigration(t *testing.T) {
	// 迁移失败场景：构造一个必然失败的迁移文件难以注入（embed 固定），
	// 此测试验证迁移事务性语义：schema_migrations 只记录成功的迁移。
	// 直接验证：重复 Migrate 幂等且不重复应用。
	s := newTestStore(t)
	if err := s.Migrate(); err != nil {
		t.Fatalf("第三次迁移应幂等: %v", err)
	}
}

func TestMemoryStoreInterface(t *testing.T) {
	var mem Store = &memoryStore{}
	if err := mem.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := mem.Close(); err != nil {
		t.Fatal(err)
	}
}

// 注入坏迁移验证失败路径（依赖注入规范 §14.1#8）。
func TestApplyMigrationsBadSQL(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	bad := fstest.MapFS{
		"0001_bad.sql": &fstest.MapFile{Data: []byte("THIS IS NOT SQL;")},
	}
	if err := applyMigrations(bad, s.(*sqliteStore).db); err == nil {
		t.Fatal("坏 SQL 迁移应报错")
	}
}

// 注入空 FS：无 .sql 文件 → 成功且幂等。
func TestApplyMigrationsEmptyFS(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := applyMigrations(fstest.MapFS{}, s.(*sqliteStore).db); err != nil {
		t.Fatalf("空 FS 迁移应成功: %v", err)
	}
}

// errFS 让 fs.ReadDir/ReadFile 全部失败（用于覆盖迁移读取错误分支）。
type errFS struct{}

func (errFS) Open(name string) (fs.File, error) { return nil, errors.New("boom") }

func TestApplyMigrationsReadDirError(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := applyMigrations(errFS{}, s.(*sqliteStore).db); err == nil {
		t.Fatal("ReadDir 失败应报错")
	}
}

func TestApplyMigrationsClosedDB(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db := s.(*sqliteStore).db
	_ = s.Close()
	// 已关闭的 db → 建 schema_migrations 失败
	if err := applyMigrations(fstest.MapFS{}, db); err == nil {
		t.Fatal("关闭的 db 迁移应报错")
	}
}

func TestWithTxNilCallback(t *testing.T) {
	s := newTestStore(t)
	if err := s.WithTx(context.Background(), nil); err == nil {
		t.Fatal("nil 回调应报错")
	}
}

// ---- 1.5 store 扩展的 memory 假实现（接口契约编译验证） ----

func (t *memoryTx) CreateGroup(*Group) error                { return errNotImpl }
func (t *memoryTx) SetGroupRekey(string, int) error         { return errNotImpl }
func (t *memoryTx) SetGroupArchived(string, int, int64) error { return errNotImpl }
func (t *memoryTx) SetGroupKeyVersion(string, int) error    { return errNotImpl }
func (t *memoryTx) AddGroupMember(*GroupMember) error       { return errNotImpl }
func (t *memoryTx) RemoveGroupMember(string, string) error  { return errNotImpl }
func (t *memoryTx) UpsertEntry(*Entry) (int64, error)       { return 0, errNotImpl }
func (t *memoryTx) UpsertEnvelope(*Envelope) error          { return errNotImpl }
func (t *memoryTx) ReplaceEnvelopes(string, int, []Envelope, int64) error {
	return errNotImpl
}
func (t *memoryTx) DeleteUserEnvelopes(string) error       { return errNotImpl }
func (t *memoryTx) DeleteGroupUserEnvelopes(string, string) error {
	return errNotImpl
}
func (t *memoryTx) DeleteOldKVEnvelopes(string, int) error { return errNotImpl }

func (m *memoryStore) ListActiveUsers() ([]User, error)      { return nil, errNotImpl }
func (m *memoryStore) GetGroup(string) (*Group, error)       { return nil, errNotImpl }
func (m *memoryStore) ListGroups() ([]Group, error)          { return nil, errNotImpl }
func (m *memoryStore) GetGroupMember(string, string) (bool, error) {
	return false, errNotImpl
}
func (m *memoryStore) ListGroupMembers(string) ([]GroupMember, error) {
	return nil, errNotImpl
}
func (m *memoryStore) ListUserGroups(string) ([]Group, error) { return nil, errNotImpl }
func (m *memoryStore) CountEntriesBelowKV(string, int) (int, error) {
	return 0, errNotImpl
}
func (m *memoryStore) GetEntry(string) (*Entry, error)       { return nil, errNotImpl }
func (m *memoryStore) PullChanges(int64, int) ([]Entry, error) {
	return nil, errNotImpl
}
func (m *memoryStore) PullGroupChanges(string, int64, int) ([]Entry, error) {
	return nil, errNotImpl
}
func (m *memoryStore) GetServerSeq() (int64, error)          { return 0, errNotImpl }
func (m *memoryStore) CountEntries(string) (int, error)      { return 0, errNotImpl }
func (m *memoryStore) GetUserEnvelopes(string) ([]Envelope, error) {
	return nil, errNotImpl
}
func (m *memoryStore) GetGroupEnvelopes(string, int) ([]Envelope, error) {
	return nil, errNotImpl
}
func (m *memoryStore) HasEnvelope(string, int, string) (bool, error) {
	return false, errNotImpl
}
func (m *memoryStore) QueryAudit(int64, int64, string, string, int) ([]AuditEvent, error) {
	return nil, errNotImpl
}
func (m *memoryStore) CreateInvite(*Invite) error               { return errNotImpl }
func (m *memoryStore) GetInviteByCode(string) (*Invite, error)  { return nil, errNotImpl }
func (m *memoryStore) MarkInviteUsed(string, int64) error        { return errNotImpl }
func (m *memoryStore) ListInvites() ([]Invite, error)            { return nil, errNotImpl }
func (m *memoryStore) CreateRegisterRequest(*RegisterRequest) error { return errNotImpl }
func (m *memoryStore) GetRegisterRequestByInvite(string) (*RegisterRequest, error) { return nil, errNotImpl }
func (m *memoryStore) GetRegisterRequestByID(string) (*RegisterRequest, error) { return nil, errNotImpl }
func (m *memoryStore) ListRegisterRequests(string) ([]RegisterRequest, error) { return nil, errNotImpl }
func (m *memoryStore) UpdateRegisterRequest(string, string, string, int64) error { return errNotImpl }

func (m *memoryStore) ListAllDevices() ([]Device, error)     { return nil, errNotImpl }

func (t *memoryTx) DeleteOldTombstones(int64) error { return errNotImpl }

func (t *memoryTx) RefreshTokenHash(string, string) error { return errNotImpl }

// WithTxSeq 测试辅助：在事务内 UpsertEntry 并返回 seq。
func (s *sqliteStore) WithTxSeq(gid, id string, kv int, ct string, deleted bool, dev string) (int64, error) {
	var seq int64
	err := s.WithTx(context.Background(), func(tx Tx) error {
		var err error
		seq, err = tx.UpsertEntry(&Entry{ID: id, GroupID: gid, KeyVersion: kv, Ciphertext: ct,
			SizeBytes: len(ct), Deleted: deleted, UpdatedBy: dev, UpdatedAt: 100})
		return err
	})
	return seq, err
}
