package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动（无 CGO，§3 技术栈）
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// sqliteStore SQLite 实现。
type sqliteStore struct {
	db *sql.DB
}

// Open 打开 SQLite 数据库（path 为文件路径或 ":memory:"）。
func Open(path string) (Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: sql.Open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

func (s *sqliteStore) Migrate() error {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("store: 打开迁移目录: %w", err)
	}
	return applyMigrations(sub, s.db)
}

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

func (s *sqliteStore) WithTx(ctx context.Context, fn func(tx Tx) error) error {
	if fn == nil {
		return errors.New("store: WithTx 回调不能为 nil")
	}
	sqlTx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stx := &sqliteTx{tx: sqlTx}
	if err := fn(stx); err != nil {
		_ = sqlTx.Rollback()
		return err
	}
	if err := sqlTx.Commit(); err != nil {
		return err
	}
	return nil
}

// ---- Store 读方法 ----

func (s *sqliteStore) GetUserCount() (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: GetUserCount: %w", err)
	}
	return n, nil
}

func (s *sqliteStore) GetUserByID(id string) (*User, error) {
	return s.queryUser(`SELECT id, COALESCE(username,''), name, sm2_public_key, attestation, role, status, created_at, COALESCE(revoked_at,0) FROM users WHERE id = ?`, id)
}

func (s *sqliteStore) GetUserByName(name string) (*User, error) {
	return s.queryUser(`SELECT id, COALESCE(username,''), name, sm2_public_key, attestation, role, status, created_at, COALESCE(revoked_at,0) FROM users WHERE name = ?`, name)
}

func (s *sqliteStore) GetUserByUsername(username string) (*User, error) {
	return s.queryUser(`SELECT id, COALESCE(username,''), name, sm2_public_key, attestation, role, status, created_at, COALESCE(revoked_at,0) FROM users WHERE username = ?`, username)
}

func (s *sqliteStore) GetUserByPublicKey(pubKey string) (*User, error) {
	return s.queryUser(`SELECT id, COALESCE(username,''), name, sm2_public_key, attestation, role, status, created_at, COALESCE(revoked_at,0) FROM users WHERE sm2_public_key = ?`, pubKey)
}

func (s *sqliteStore) queryUser(q string, arg any) (*User, error) {
	var u User
	err := s.db.QueryRow(q, arg).Scan(&u.ID, &u.Username, &u.Name, &u.SM2PublicKey, &u.Attestation, &u.Role, &u.Status, &u.CreatedAt, &u.RevokedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoRows
		}
		return nil, fmt.Errorf("store: queryUser: %w", err)
	}
	return &u, nil
}

func (s *sqliteStore) ListActiveUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(username,''), name, sm2_public_key, attestation, role, status, created_at, COALESCE(revoked_at,0) FROM users WHERE status = ? ORDER BY created_at`, StatusActive)
	if err != nil {
		return nil, fmt.Errorf("store: ListActiveUsers: %w", err)
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.Name, &u.SM2PublicKey, &u.Attestation, &u.Role, &u.Status, &u.CreatedAt, &u.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetDeviceByTokenHash(hash string) (*Device, error) {
	return s.queryDevice(`SELECT id, user_id, name, COALESCE(hostname,''), COALESCE(last_ip,''), token_hash, status, created_at, COALESCE(last_seen,0) FROM devices WHERE token_hash = ?`, hash)
}

func (s *sqliteStore) GetDeviceByID(id string) (*Device, error) {
	return s.queryDevice(`SELECT id, user_id, name, COALESCE(hostname,''), COALESCE(last_ip,''), token_hash, status, created_at, COALESCE(last_seen,0) FROM devices WHERE id = ?`, id)
}

func (s *sqliteStore) ListDevicesByUser(userID string) ([]Device, error) {
	return s.queryDevices(`SELECT id, user_id, name, COALESCE(hostname,''), COALESCE(last_ip,''), token_hash, status, created_at, COALESCE(last_seen,0) FROM devices WHERE user_id = ? ORDER BY created_at`, userID)
}

func (s *sqliteStore) ListAllDevices() ([]Device, error) {
	return s.queryDevices(`SELECT d.id, d.user_id, d.name, COALESCE(d.hostname,''), COALESCE(d.last_ip,''), d.token_hash, d.status, d.created_at, COALESCE(d.last_seen,0) FROM devices d ORDER BY d.created_at`)
}

func (s *sqliteStore) queryDevice(q string, arg any) (*Device, error) {
	var d Device
	err := s.db.QueryRow(q, arg).Scan(&d.ID, &d.UserID, &d.Name, &d.Hostname, &d.LastIP, &d.TokenHash, &d.Status, &d.CreatedAt, &d.LastSeen)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoRows
		}
		return nil, fmt.Errorf("store: queryDevice: %w", err)
	}
	return &d, nil
}

func (s *sqliteStore) queryDevices(q string, args ...any) ([]Device, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: queryDevices: %w", err)
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.UserID, &d.Name, &d.Hostname, &d.LastIP, &d.TokenHash, &d.Status, &d.CreatedAt, &d.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetGroup(groupID string) (*Group, error) {
	var g Group
	err := s.db.QueryRow(`SELECT id, name, key_version, pending_rekey, archived, created_at, COALESCE(archived_at,0) FROM groups WHERE id = ?`, groupID).
		Scan(&g.ID, &g.Name, &g.KeyVersion, &g.PendingRekey, &g.Archived, &g.CreatedAt, &g.ArchivedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoRows
		}
		return nil, fmt.Errorf("store: GetGroup: %w", err)
	}
	return &g, nil
}

func (s *sqliteStore) ListGroups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT id, name, key_version, pending_rekey, archived, created_at, COALESCE(archived_at,0) FROM groups ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("store: ListGroups: %w", err)
	}
	defer rows.Close()
	return scanGroups(rows)
}

func scanGroups(rows *sql.Rows) ([]Group, error) {
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.KeyVersion, &g.PendingRekey, &g.Archived, &g.CreatedAt, &g.ArchivedAt); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetGroupMember(groupID, userID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *sqliteStore) ListGroupMembers(groupID string) ([]GroupMember, error) {
	rows, err := s.db.Query(`SELECT group_id, user_id, created_at FROM group_members WHERE group_id = ?`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupMember
	for rows.Next() {
		var m GroupMember
		if err := rows.Scan(&m.GroupID, &m.UserID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *sqliteStore) ListUserGroups(userID string) ([]Group, error) {
	rows, err := s.db.Query(`SELECT g.id, g.name, g.key_version, g.pending_rekey, g.archived, g.created_at, COALESCE(g.archived_at,0)
		FROM groups g JOIN group_members m ON m.group_id = g.id WHERE m.user_id = ? ORDER BY g.created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanGroups(rows)
}

func (s *sqliteStore) GetEntry(entryID string) (*Entry, error) {
	var e Entry
	var deleted int
	err := s.db.QueryRow(`SELECT id, group_id, seq, key_version, ciphertext, size_bytes, deleted, updated_by, updated_at FROM entries WHERE id = ?`, entryID).
		Scan(&e.ID, &e.GroupID, &e.Seq, &e.KeyVersion, &e.Ciphertext, &e.SizeBytes, &deleted, &e.UpdatedBy, &e.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNoRows
		}
		return nil, err
	}
	e.Deleted = deleted != 0
	return &e, nil
}

func (s *sqliteStore) PullChanges(since int64, limit int) ([]Entry, error) {
	rows, err := s.db.Query(`SELECT id, group_id, seq, key_version, ciphertext, size_bytes, deleted, updated_by, updated_at FROM entries WHERE seq > ? ORDER BY seq LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

func (s *sqliteStore) PullGroupChanges(groupID string, since int64, limit int) ([]Entry, error) {
	rows, err := s.db.Query(`SELECT id, group_id, seq, key_version, ciphertext, size_bytes, deleted, updated_by, updated_at FROM entries WHERE group_id = ? AND seq > ? ORDER BY seq LIMIT ?`, groupID, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

func scanEntries(rows *sql.Rows) ([]Entry, error) {
	var out []Entry
	for rows.Next() {
		var e Entry
		var deleted int
		if err := rows.Scan(&e.ID, &e.GroupID, &e.Seq, &e.KeyVersion, &e.Ciphertext, &e.SizeBytes, &deleted, &e.UpdatedBy, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Deleted = deleted != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetServerSeq() (int64, error) {
	var v int64
	if err := s.db.QueryRow(`SELECT value FROM seq_counter WHERE id = 1`).Scan(&v); err != nil {
		return 0, err
	}
	return v, nil
}

func (s *sqliteStore) CountEntries(groupID string) (int, error) {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE group_id = ?`, groupID).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *sqliteStore) CountEntriesBelowKV(groupID string, kv int) (int, error) {
	var n int
	// 排除墓碑（§6.3 收尾判定：deleted=true 不参与）
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM entries WHERE group_id = ? AND deleted = 0 AND key_version < ?`, groupID, kv).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func (s *sqliteStore) GetUserEnvelopes(userID string) ([]Envelope, error) {
	rows, err := s.db.Query(`SELECT group_id, key_version, user_id, wrapped_dek, updated_at FROM key_envelopes WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEnvelopes(rows)
}

func (s *sqliteStore) GetGroupEnvelopes(groupID string, kv int) ([]Envelope, error) {
	rows, err := s.db.Query(`SELECT group_id, key_version, user_id, wrapped_dek, updated_at FROM key_envelopes WHERE group_id = ? AND key_version = ?`, groupID, kv)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEnvelopes(rows)
}

func (s *sqliteStore) HasEnvelope(groupID string, kv int, userID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM key_envelopes WHERE group_id = ? AND key_version = ? AND user_id = ?`, groupID, kv, userID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func scanEnvelopes(rows *sql.Rows) ([]Envelope, error) {
	var out []Envelope
	for rows.Next() {
		var e Envelope
		if err := rows.Scan(&e.GroupID, &e.KeyVersion, &e.UserID, &e.WrappedDEK, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *sqliteStore) QueryAudit(from, to int64, userID, action string, limit int) ([]AuditEvent, error) {
	var conds []string
	var args []any
	if from > 0 {
		conds = append(conds, "ts >= ?")
		args = append(args, from)
	}
	if to > 0 {
		conds = append(conds, "ts <= ?")
		args = append(args, to)
	}
	if userID != "" {
		conds = append(conds, "user_id = ?")
		args = append(args, userID)
	}
	if action != "" {
		conds = append(conds, "action = ?")
		args = append(args, action)
	}
	q := `SELECT id, ts, device_id, user_id, action, COALESCE(entry_id,''), COALESCE(ip,''), COALESCE(device_name,''), COALESCE(hostname,''), COALESCE(detail,'') FROM audit_log`
	if len(conds) > 0 {
		// #nosec G202 -- 拼接的是固定白名单条件字符串（"user_id = ?"/"action = ?"），
		// 值经 args 参数化传入，非用户输入拼接
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += " ORDER BY ts DESC LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEvent
	for rows.Next() {
		var e AuditEvent
		if err := rows.Scan(&e.ID, &e.TS, &e.DeviceID, &e.UserID, &e.Action, &e.EntryID, &e.IP, &e.DeviceName, &e.Hostname, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---- Tx 写方法 ----

type sqliteTx struct {
	tx *sql.Tx
}

func (t *sqliteTx) NextSeq() (int64, error) {
	var v int64
	if err := t.tx.QueryRow(`UPDATE seq_counter SET value = value + 1 WHERE id = 1 RETURNING value`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: NextSeq: %w", err)
	}
	return v, nil
}

func (t *sqliteTx) CreateUser(u *User) error {
	// 空 username（存量/测试用户未设工号）存 NULL，避免 partial unique index 约束多个空串
	var username any = u.Username
	if u.Username == "" {
		username = nil
	}
	_, err := t.tx.Exec(`INSERT INTO users (id, username, name, sm2_public_key, attestation, role, status, created_at, revoked_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		u.ID, username, u.Name, u.SM2PublicKey, u.Attestation, u.Role, u.Status, u.CreatedAt)
	return classifyErr(err)
}

func (t *sqliteTx) SetUserRevoked(userID string, revokedAt int64) error {
	_, err := t.tx.Exec(`UPDATE users SET status = ?, revoked_at = ? WHERE id = ?`, StatusRevoked, revokedAt, userID)
	return classifyErr(err)
}

func (t *sqliteTx) ReplaceUserPublicKey(userID, pubKey, attestation string) error {
	_, err := t.tx.Exec(`UPDATE users SET sm2_public_key = ?, attestation = ? WHERE id = ?`, pubKey, attestation, userID)
	return classifyErr(err)
}

func (t *sqliteTx) CreateDevice(d *Device) error {
	_, err := t.tx.Exec(`INSERT INTO devices (id, user_id, name, hostname, last_ip, token_hash, status, created_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		d.ID, d.UserID, d.Name, d.Hostname, d.LastIP, d.TokenHash, d.Status, d.CreatedAt)
	return classifyErr(err)
}

func (t *sqliteTx) DisableDevice(deviceID string) error {
	_, err := t.tx.Exec(`UPDATE devices SET status = ? WHERE id = ?`, DeviceDisabled, deviceID)
	return classifyErr(err)
}

func (t *sqliteTx) DisableUserDevices(userID string) error {
	_, err := t.tx.Exec(`UPDATE devices SET status = ? WHERE user_id = ?`, DeviceDisabled, userID)
	return classifyErr(err)
}

func (t *sqliteTx) UpdateDeviceSeen(deviceID, hostname string, lastSeen int64) error {
	_, err := t.tx.Exec(`UPDATE devices SET hostname = ?, last_seen = ? WHERE id = ?`, hostname, lastSeen, deviceID)
	return classifyErr(err)
}

func (t *sqliteTx) UpdateDeviceIP(deviceID, ip string) error {
	_, err := t.tx.Exec(`UPDATE devices SET last_ip = ? WHERE id = ?`, ip, deviceID)
	return classifyErr(err)
}

func (t *sqliteTx) RefreshTokenHash(deviceID, tokenHash string) error {
	_, err := t.tx.Exec(`UPDATE devices SET token_hash = ? WHERE id = ?`, tokenHash, deviceID)
	return classifyErr(err)
}

func (t *sqliteTx) Audit(e *AuditEvent) error {
	_, err := t.tx.Exec(`INSERT INTO audit_log (ts, device_id, user_id, action, entry_id, ip, device_name, hostname, detail)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.TS, e.DeviceID, e.UserID, e.Action, nullStr(e.EntryID), nullStr(e.IP),
		nullStr(e.DeviceName), nullStr(e.Hostname), nullStr(e.Detail))
	return classifyErr(err)
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (t *sqliteTx) CreateGroup(g *Group) error {
	_, err := t.tx.Exec(`INSERT INTO groups (id, name, key_version, pending_rekey, archived, created_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		g.ID, g.Name, g.KeyVersion, g.PendingRekey, g.Archived, g.CreatedAt)
	return classifyErr(err)
}

func (t *sqliteTx) SetGroupRekey(groupID string, pending int) error {
	_, err := t.tx.Exec(`UPDATE groups SET pending_rekey = ? WHERE id = ?`, pending, groupID)
	return classifyErr(err)
}

func (t *sqliteTx) SetGroupArchived(groupID string, archived int, at int64) error {
	_, err := t.tx.Exec(`UPDATE groups SET archived = ?, archived_at = ? WHERE id = ?`, archived, intOrNull(archived, at), groupID)
	return classifyErr(err)
}

func intOrNull(archived int, at int64) any {
	if archived == GroupArchived {
		return at
	}
	return nil
}

func (t *sqliteTx) SetGroupKeyVersion(groupID string, kv int) error {
	_, err := t.tx.Exec(`UPDATE groups SET key_version = ? WHERE id = ?`, kv, groupID)
	return classifyErr(err)
}

func (t *sqliteTx) AddGroupMember(gm *GroupMember) error {
	_, err := t.tx.Exec(`INSERT INTO group_members (group_id, user_id, created_at) VALUES (?, ?, ?)`,
		gm.GroupID, gm.UserID, gm.CreatedAt)
	return classifyErr(err)
}

func (t *sqliteTx) RemoveGroupMember(groupID, userID string) error {
	_, err := t.tx.Exec(`DELETE FROM group_members WHERE group_id = ? AND user_id = ?`, groupID, userID)
	return classifyErr(err)
}

func (t *sqliteTx) UpsertEntry(e *Entry) (int64, error) {
	seq, err := t.NextSeq()
	if err != nil {
		return 0, err
	}
	deleted := 0
	if e.Deleted {
		deleted = 1
	}
	// SQLite UPSERT：id 存在则更新（seq 新分配），不存在则插入
	_, err = t.tx.Exec(`INSERT INTO entries (id, group_id, seq, key_version, ciphertext, size_bytes, deleted, updated_by, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET seq = excluded.seq, key_version = excluded.key_version,
			ciphertext = excluded.ciphertext, size_bytes = excluded.size_bytes, deleted = excluded.deleted,
			updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
		e.ID, e.GroupID, seq, e.KeyVersion, e.Ciphertext, e.SizeBytes, deleted, e.UpdatedBy, e.UpdatedAt)
	if err != nil {
		return 0, classifyErr(err)
	}
	return seq, nil
}

func (t *sqliteTx) UpsertEnvelope(env *Envelope) error {
	res, err := t.tx.Exec(`INSERT INTO key_envelopes (group_id, key_version, user_id, wrapped_dek, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(group_id, key_version, user_id) DO NOTHING`,
		env.GroupID, env.KeyVersion, env.UserID, env.WrappedDEK, env.UpdatedAt)
	if err != nil {
		return classifyErr(err)
	}
	// 已存在（覆盖被拒，§6.3 入伙只允许追加）→ 唯一约束语义
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConstraintUnique
	}
	return nil
}

func (t *sqliteTx) ReplaceEnvelopes(groupID string, kv int, envs []Envelope, at int64) error {
	if _, err := t.tx.Exec(`DELETE FROM key_envelopes WHERE group_id = ? AND key_version = ?`, groupID, kv); err != nil {
		return classifyErr(err)
	}
	for _, env := range envs {
		if _, err := t.tx.Exec(`INSERT INTO key_envelopes (group_id, key_version, user_id, wrapped_dek, updated_at)
			VALUES (?, ?, ?, ?, ?)`, groupID, kv, env.UserID, env.WrappedDEK, at); err != nil {
			return classifyErr(err)
		}
	}
	return nil
}

func (t *sqliteTx) DeleteUserEnvelopes(userID string) error {
	_, err := t.tx.Exec(`DELETE FROM key_envelopes WHERE user_id = ?`, userID)
	return classifyErr(err)
}

func (t *sqliteTx) DeleteGroupUserEnvelopes(groupID, userID string) error {
	_, err := t.tx.Exec(`DELETE FROM key_envelopes WHERE group_id = ? AND user_id = ?`, groupID, userID)
	return classifyErr(err)
}

func (t *sqliteTx) DeleteOldKVEnvelopes(groupID string, newKV int) error {
	_, err := t.tx.Exec(`DELETE FROM key_envelopes WHERE group_id = ? AND key_version < ?`, groupID, newKV)
	return classifyErr(err)
}

// DeleteOldTombstones 物理删除早于 before 的墓碑（§7.4：每天 03:00 UTC 清理 90 天前）。
func (t *sqliteTx) DeleteOldTombstones(before int64) error {
	_, err := t.tx.Exec(`DELETE FROM entries WHERE deleted = 1 AND updated_at < ?`, before)
	return classifyErr(err)
}

// classifyErr 将 SQLite 驱动错误映射为 store 哨兵错误。
func classifyErr(err error) error {
	if err == nil {
		return nil
	}
	if err == sql.ErrNoRows {
		return ErrNoRows
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		switch se.Code() {
		case sqlite3.SQLITE_CONSTRAINT_UNIQUE:
			return ErrConstraintUnique
		case sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY:
			return ErrConstraintFK
		}
	}
	return err
}

// ---- invites / register_requests（方案 C：审核制）----

func (s *sqliteStore) CreateInvite(inv *Invite) error {
	_, err := s.db.Exec(`INSERT INTO invites (id, code, username, auto_approve, status, expires_at, created_by, created_at, used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, COALESCE(?, 0))`,
		inv.ID, inv.Code, inv.Username, inv.AutoApprove, inv.Status, inv.ExpiresAt, inv.CreatedBy, inv.CreatedAt, inv.UsedAt)
	return err
}

func (s *sqliteStore) GetInviteByCode(code string) (*Invite, error) {
	row := s.db.QueryRow(`SELECT id, code, username, auto_approve, status, expires_at, created_by, created_at, COALESCE(used_at, 0)
		FROM invites WHERE code = ?`, code)
	var i Invite
	if err := row.Scan(&i.ID, &i.Code, &i.Username, &i.AutoApprove, &i.Status, &i.ExpiresAt, &i.CreatedBy, &i.CreatedAt, &i.UsedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoRows
		}
		return nil, err
	}
	return &i, nil
}

func (s *sqliteStore) MarkInviteUsed(code string, usedAt int64) error {
	_, err := s.db.Exec(`UPDATE invites SET status = 'used', used_at = ? WHERE code = ? AND status = 'unused'`, usedAt, code)
	return err
}

func (s *sqliteStore) ListInvites() ([]Invite, error) {
	rows, err := s.db.Query(`SELECT id, code, username, auto_approve, status, expires_at, created_by, created_at, COALESCE(used_at, 0)
		FROM invites ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var i Invite
		if err := rows.Scan(&i.ID, &i.Code, &i.Username, &i.AutoApprove, &i.Status, &i.ExpiresAt, &i.CreatedBy, &i.CreatedAt, &i.UsedAt); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (s *sqliteStore) CreateRegisterRequest(r *RegisterRequest) error {
	_, err := s.db.Exec(`INSERT INTO register_requests (id, invite_code, username, sm2_public_key, device_name, ip, status, created_at, reviewed_by, reviewed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(?, 0))`,
		r.ID, r.InviteCode, r.Username, r.SM2PublicKey, r.DeviceName, r.IP, r.Status, r.CreatedAt, r.ReviewedBy, r.ReviewedAt)
	return err
}

func (s *sqliteStore) GetRegisterRequestByInvite(code string) (*RegisterRequest, error) {
	row := s.db.QueryRow(`SELECT id, invite_code, username, sm2_public_key, device_name, COALESCE(ip,''), status, created_at, COALESCE(reviewed_by,''), COALESCE(reviewed_at,0)
		FROM register_requests WHERE invite_code = ? ORDER BY created_at DESC LIMIT 1`, code)
	var r RegisterRequest
	if err := row.Scan(&r.ID, &r.InviteCode, &r.Username, &r.SM2PublicKey, &r.DeviceName, &r.IP, &r.Status, &r.CreatedAt, &r.ReviewedBy, &r.ReviewedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoRows
		}
		return nil, err
	}
	return &r, nil
}

func (s *sqliteStore) GetRegisterRequestByID(id string) (*RegisterRequest, error) {
	row := s.db.QueryRow(`SELECT id, invite_code, username, sm2_public_key, device_name, COALESCE(ip,''), status, created_at, COALESCE(reviewed_by,''), COALESCE(reviewed_at,0)
		FROM register_requests WHERE id = ?`, id)
	var r RegisterRequest
	if err := row.Scan(&r.ID, &r.InviteCode, &r.Username, &r.SM2PublicKey, &r.DeviceName, &r.IP, &r.Status, &r.CreatedAt, &r.ReviewedBy, &r.ReviewedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNoRows
		}
		return nil, err
	}
	return &r, nil
}

func (s *sqliteStore) ListRegisterRequests(status string) ([]RegisterRequest, error) {
	rows, err := s.db.Query(`SELECT id, invite_code, username, sm2_public_key, device_name, COALESCE(ip,''), status, created_at, COALESCE(reviewed_by,''), COALESCE(reviewed_at,0)
		FROM register_requests WHERE (? = '' OR status = ?) ORDER BY created_at DESC`, status, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RegisterRequest
	for rows.Next() {
		var r RegisterRequest
		if err := rows.Scan(&r.ID, &r.InviteCode, &r.Username, &r.SM2PublicKey, &r.DeviceName, &r.IP, &r.Status, &r.CreatedAt, &r.ReviewedBy, &r.ReviewedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *sqliteStore) UpdateRegisterRequest(id, status, reviewedBy string, reviewedAt int64) error {
	_, err := s.db.Exec(`UPDATE register_requests SET status = ?, reviewed_by = ?, reviewed_at = ? WHERE id = ?`, status, reviewedBy, reviewedAt, id)
	return err
}
