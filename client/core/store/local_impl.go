package store

import (
	"database/sql"
	"errors"
)

// ---- 实现 LocalStore 接口 ----

func (s *sqliteLocal) GetDeviceState() (*DeviceState, error) {
	var d DeviceState
	err := s.db.QueryRow(`SELECT device_id, token_enc, expires_at FROM device_state WHERE id = 1`).
		Scan(&d.DeviceID, &d.TokenEnc, &d.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *sqliteLocal) SetDeviceState(d *DeviceState) error {
	_, err := s.db.Exec(`INSERT INTO device_state (id, device_id, token_enc, expires_at) VALUES (1, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET device_id = excluded.device_id, token_enc = excluded.token_enc, expires_at = excluded.expires_at`,
		d.DeviceID, d.TokenEnc, d.ExpiresAt)
	return err
}

func (s *sqliteLocal) GetIdentity() (*Identity, error) {
	var i Identity
	err := s.db.QueryRow(`SELECT username, COALESCE(role,''), keyfile_blob, public_key FROM identity WHERE id = 1`).
		Scan(&i.Username, &i.Role, &i.KeyfileBlob, &i.PublicKey)
	if err == sql.ErrNoRows {
		return nil, ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	return &i, nil
}

func (s *sqliteLocal) SetIdentity(i *Identity) error {
	_, err := s.db.Exec(`INSERT INTO identity (id, username, role, keyfile_blob, public_key) VALUES (1, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET username = excluded.username, role = excluded.role, keyfile_blob = excluded.keyfile_blob, public_key = excluded.public_key`,
		i.Username, i.Role, i.KeyfileBlob, i.PublicKey)
	return err
}

func (s *sqliteLocal) GetLastSeq() (int64, error) {
	var v int64
	if err := s.db.QueryRow(`SELECT last_seq FROM sync_state WHERE id = 1`).Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	return v, nil
}

func (s *sqliteLocal) SetLastSeq(seq int64) error {
	_, err := s.db.Exec(`INSERT INTO sync_state (id, last_seq, last_pull_at) VALUES (1, ?, unixepoch())
		ON CONFLICT(id) DO UPDATE SET last_seq = excluded.last_seq, last_pull_at = excluded.last_pull_at`, seq)
	return err
}

func (s *sqliteLocal) GetGroupState(gid string) (*GroupState, error) {
	var g GroupState
	var archived int
	err := s.db.QueryRow(`SELECT group_id, COALESCE(name,''), archived, COALESCE(key_version,1), COALESCE(initialized_at,0) FROM group_state WHERE group_id = ?`, gid).
		Scan(&g.GroupID, &g.Name, &archived, &g.KeyVersion, &g.InitializedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	g.Archived = archived != 0
	return &g, nil
}

func (s *sqliteLocal) SetGroupState(g *GroupState) error {
	archived := 0
	if g.Archived {
		archived = 1
	}
	_, err := s.db.Exec(`INSERT INTO group_state (group_id, name, archived, key_version, initialized_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(group_id) DO UPDATE SET name = excluded.name, archived = excluded.archived, key_version = excluded.key_version, initialized_at = excluded.initialized_at`,
		g.GroupID, g.Name, archived, g.KeyVersion, g.InitializedAt)
	return err
}

func (s *sqliteLocal) ListGroupStates() ([]GroupState, error) {
	rows, err := s.db.Query(`SELECT group_id, COALESCE(name,''), archived, COALESCE(key_version,1), COALESCE(initialized_at,0) FROM group_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []GroupState
	for rows.Next() {
		var g GroupState
		var archived int
		if err := rows.Scan(&g.GroupID, &g.Name, &archived, &g.KeyVersion, &g.InitializedAt); err != nil {
			return nil, err
		}
		g.Archived = archived != 0
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *sqliteLocal) UpsertLocalEntry(e *LocalEntry) error {
	dirty := 0
	if e.Dirty {
		dirty = 1
	}
	deleted := 0
	if e.Deleted {
		deleted = 1
	}
	_, err := s.db.Exec(`INSERT INTO local_entries (entry_id, group_id, seq, key_version, ciphertext, plaintext_cache_enc, base_enc, dirty, deleted, conflict_of, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(entry_id) DO UPDATE SET group_id = excluded.group_id, seq = excluded.seq,
			key_version = excluded.key_version, ciphertext = excluded.ciphertext,
			plaintext_cache_enc = excluded.plaintext_cache_enc, base_enc = excluded.base_enc,
			dirty = excluded.dirty, deleted = excluded.deleted, conflict_of = excluded.conflict_of, updated_at = excluded.updated_at`,
		e.ID, e.GroupID, e.Seq, e.KeyVersion, e.Ciphertext, e.PlaintextCache, e.BaseEnc, dirty, deleted,
		nullStr(e.ConflictOf), e.UpdatedAt)
	return err
}

func (s *sqliteLocal) GetLocalEntry(id string) (*LocalEntry, error) {
	var e LocalEntry
	var dirty, deleted int
	err := s.db.QueryRow(`SELECT entry_id, group_id, seq, key_version, ciphertext, plaintext_cache_enc, base_enc, dirty, COALESCE(deleted,0), COALESCE(conflict_of,''), updated_at
		FROM local_entries WHERE entry_id = ?`, id).
		Scan(&e.ID, &e.GroupID, &e.Seq, &e.KeyVersion, &e.Ciphertext, &e.PlaintextCache, &e.BaseEnc, &dirty, &deleted, &e.ConflictOf, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNoRows
	}
	if err != nil {
		return nil, err
	}
	e.Dirty = dirty != 0
	e.Deleted = deleted != 0
	return &e, nil
}

func (s *sqliteLocal) ListLocalEntries() ([]LocalEntry, error) {
	rows, err := s.db.Query(`SELECT entry_id, group_id, seq, key_version, ciphertext, plaintext_cache_enc, base_enc, dirty, COALESCE(deleted,0), COALESCE(conflict_of,''), updated_at FROM local_entries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

func (s *sqliteLocal) ListDirtyEntries() ([]LocalEntry, error) {
	rows, err := s.db.Query(`SELECT entry_id, group_id, seq, key_version, ciphertext, plaintext_cache_enc, base_enc, dirty, COALESCE(deleted,0), COALESCE(conflict_of,''), updated_at FROM local_entries WHERE dirty = 1`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEntries(rows)
}

func scanEntries(rows *sql.Rows) ([]LocalEntry, error) {
	var out []LocalEntry
	for rows.Next() {
		var e LocalEntry
		var dirty, deleted int
		if err := rows.Scan(&e.ID, &e.GroupID, &e.Seq, &e.KeyVersion, &e.Ciphertext, &e.PlaintextCache, &e.BaseEnc, &dirty, &deleted, &e.ConflictOf, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Dirty = dirty != 0
		e.Deleted = deleted != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *sqliteLocal) SetDirty(id string, dirty bool) error {
	v := 0
	if dirty {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE local_entries SET dirty = ? WHERE entry_id = ?`, v, id)
	return err
}

func (s *sqliteLocal) SetBaseEnc(id string, baseEnc []byte) error {
	_, err := s.db.Exec(`UPDATE local_entries SET base_enc = ? WHERE entry_id = ?`, baseEnc, id)
	return err
}

func (s *sqliteLocal) SetPlaintextCache(id string, cache []byte) error {
	_, err := s.db.Exec(`UPDATE local_entries SET plaintext_cache_enc = ? WHERE entry_id = ?`, cache, id)
	return err
}

func (s *sqliteLocal) SetConflict(id string, conflictOf string) error {
	_, err := s.db.Exec(`UPDATE local_entries SET conflict_of = ? WHERE entry_id = ?`, nullStr(conflictOf), id)
	return err
}

func (s *sqliteLocal) RemoveLocalEntry(id string) error {
	_, err := s.db.Exec(`DELETE FROM local_entries WHERE entry_id = ?`, id)
	return err
}

func (s *sqliteLocal) PutPendingEntry(e *PendingEntry) error {
	_, err := s.db.Exec(`INSERT INTO pending_entries (entry_id, group_id, seq, key_version, ciphertext, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(entry_id) DO UPDATE SET group_id = excluded.group_id, seq = excluded.seq,
			key_version = excluded.key_version, ciphertext = excluded.ciphertext, updated_at = excluded.updated_at`,
		e.ID, e.GroupID, e.Seq, e.KeyVersion, e.Ciphertext, e.UpdatedAt)
	return err
}

func (s *sqliteLocal) GetPendingEntry(id string) (*PendingEntry, error) {
	var e PendingEntry
	err := s.db.QueryRow(`SELECT entry_id, group_id, seq, key_version, ciphertext, updated_at FROM pending_entries WHERE entry_id = ?`, id).
		Scan(&e.ID, &e.GroupID, &e.Seq, &e.KeyVersion, &e.Ciphertext, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNoRows
	}
	return &e, err
}

func (s *sqliteLocal) ListPendingEntries() ([]PendingEntry, error) {
	rows, err := s.db.Query(`SELECT entry_id, group_id, seq, key_version, ciphertext, updated_at FROM pending_entries`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingEntry
	for rows.Next() {
		var e PendingEntry
		if err := rows.Scan(&e.ID, &e.GroupID, &e.Seq, &e.KeyVersion, &e.Ciphertext, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *sqliteLocal) DeletePendingEntry(id string) error {
	_, err := s.db.Exec(`DELETE FROM pending_entries WHERE entry_id = ?`, id)
	return err
}

func (s *sqliteLocal) PutDEK(groupID string, kv int, dekEnc []byte) error {
	_, err := s.db.Exec(`INSERT INTO key_cache (group_id, key_version, dek_enc) VALUES (?, ?, ?)
		ON CONFLICT(group_id, key_version) DO UPDATE SET dek_enc = excluded.dek_enc`, groupID, kv, dekEnc)
	return err
}

func (s *sqliteLocal) GetDEK(groupID string, kv int) ([]byte, error) {
	var enc []byte
	err := s.db.QueryRow(`SELECT dek_enc FROM key_cache WHERE group_id = ? AND key_version = ?`, groupID, kv).Scan(&enc)
	if err == sql.ErrNoRows {
		return nil, ErrNoRows
	}
	return enc, err
}

func (s *sqliteLocal) DeleteGroupDEKs(groupID string) error {
	_, err := s.db.Exec(`DELETE FROM key_cache WHERE group_id = ?`, groupID)
	return err
}

func (s *sqliteLocal) ListDEKGroupIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT group_id FROM key_cache`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *sqliteLocal) MarkBadSeq(seq int64) error {
	_, err := s.db.Exec(`INSERT INTO bad_seq (seq, fail_count) VALUES (?, 1) ON CONFLICT(seq) DO NOTHING`, seq)
	return err
}

func (s *sqliteLocal) IncrementBadSeq(seq int64) error {
	_, err := s.db.Exec(`INSERT INTO bad_seq (seq, fail_count) VALUES (?, 1)
		ON CONFLICT(seq) DO UPDATE SET fail_count = fail_count + 1`, seq)
	return err
}

func (s *sqliteLocal) ListBadSeqs() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT seq, fail_count FROM bad_seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var seq int64
		var n int
		if err := rows.Scan(&seq, &n); err != nil {
			return nil, err
		}
		out[seq] = n
	}
	return out, rows.Err()
}

func (s *sqliteLocal) ClearBadSeq(seq int64) error {
	_, err := s.db.Exec(`DELETE FROM bad_seq WHERE seq = ?`, seq)
	return err
}

func (s *sqliteLocal) PutRecycleBin(id string, ciphertext string, deletedAt int64) error {
	_, err := s.db.Exec(`INSERT INTO recycle_bin (entry_id, ciphertext, deleted_at) VALUES (?, ?, ?)
		ON CONFLICT(entry_id) DO UPDATE SET ciphertext = excluded.ciphertext, deleted_at = excluded.deleted_at`,
		id, ciphertext, deletedAt)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// ErrNoRows 无结果哨兵。
var ErrNoRows = errors.New("local store: 无结果")

func (s *sqliteLocal) ListDEKVersions() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT group_id, MAX(key_version) FROM key_cache GROUP BY group_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var g string
		var kv int
		if err := rows.Scan(&g, &kv); err != nil {
			return nil, err
		}
		out[g] = kv
	}
	return out, rows.Err()
}

func (s *sqliteLocal) GetServerURL() (string, error) {
	var url string
	err := s.db.QueryRow(`SELECT COALESCE(server_url,'') FROM app_config WHERE id = 1`).Scan(&url)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return url, err
}

func (s *sqliteLocal) SetServerURL(url string) error {
	_, err := s.db.Exec(`INSERT INTO app_config (id, server_url) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET server_url = excluded.server_url`, url)
	return err
}

func (s *sqliteLocal) GetCA() (string, error) {
	var path string
	err := s.db.QueryRow(`SELECT COALESCE(ca_path,'') FROM app_config WHERE id = 1`).Scan(&path)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return path, err
}

func (s *sqliteLocal) SetCA(path string) error {
	_, err := s.db.Exec(`INSERT INTO app_config (id, ca_path) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET ca_path = excluded.ca_path`, path)
	return err
}

func (s *sqliteLocal) GetRegSecretEnc() ([]byte, error) {
	var enc []byte
	err := s.db.QueryRow(`SELECT reg_secret_enc FROM app_config WHERE id = 1`).Scan(&enc)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return enc, nil
}

func (s *sqliteLocal) SetRegSecretEnc(enc []byte) error {
	_, err := s.db.Exec(`INSERT INTO app_config (id, reg_secret_enc) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET reg_secret_enc = excluded.reg_secret_enc`, enc)
	return err
}

func (s *sqliteLocal) GetSyncMode() (string, error) {
	var mode string
	err := s.db.QueryRow(`SELECT COALESCE(sync_mode,'auto') FROM app_config WHERE id = 1`).Scan(&mode)
	if err == sql.ErrNoRows {
		return "auto", nil
	}
	if err != nil {
		return "auto", err
	}
	return mode, nil
}

func (s *sqliteLocal) SetSyncMode(mode string) error {
	_, err := s.db.Exec(`INSERT INTO app_config (id, sync_mode) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET sync_mode = excluded.sync_mode`, mode)
	return err
}

func (s *sqliteLocal) GetAutoUnlock() (*AutoUnlockConfig, error) {
	c := &AutoUnlockConfig{}
	var enabled int
	var path string
	var kek []byte
	err := s.db.QueryRow(`SELECT COALESCE(keyfile_path,''), COALESCE(autounlock_enabled,0), autounlock_kek
		FROM app_config WHERE id = 1`).
		Scan(&path, &enabled, &kek)
	if err == sql.ErrNoRows {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	c.KeyfilePath = path
	c.Enabled = enabled != 0
	c.KEKBlob = kek
	return c, nil
}

func (s *sqliteLocal) SetAutoUnlock(c *AutoUnlockConfig) error {
	enabled := 0
	if c.Enabled {
		enabled = 1
	}
	var kek any
	if len(c.KEKBlob) > 0 {
		kek = c.KEKBlob
	}
	_, err := s.db.Exec(`INSERT INTO app_config (id, server_url, keyfile_path, autounlock_enabled, autounlock_kek)
		VALUES (1, '', ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET keyfile_path = excluded.keyfile_path,
			autounlock_enabled = excluded.autounlock_enabled, autounlock_kek = excluded.autounlock_kek`,
		c.KeyfilePath, enabled, kek)
	return err
}
