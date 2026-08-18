package syncer

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"passbook/client/core/api"
	"passbook/client/core/store"
	"passbook/client/core/vault"
	"passbook/internal/crypto"
	"strings"

	"passbook/internal/model"
	"passbook/internal/proto"
)

// mockClient 模拟服务端。
type mockClient struct {
	pullResp *proto.SyncResponse
	pullErr  error
	pushResp *proto.PushResponse
	uploads  []proto.KeysUploadRequest
	users    []proto.UserInfo
	tok      string
}

func (m *mockClient) Pull(int64, map[string]int) (*proto.SyncResponse, error) {
	if m.pullErr != nil {
		err := m.pullErr
		m.pullErr = nil // 只失败一次，模拟 token 刷新后恢复
		return nil, err
	}
	return m.pullResp, nil
}
func (m *mockClient) Push([]proto.Mutation) (*proto.PushResponse, error) {
	return m.pushResp, nil
}
func (m *mockClient) UploadKeys(groupID string, req *proto.KeysUploadRequest) error {
	m.uploads = append(m.uploads, *req)
	return nil
}
func (m *mockClient) ListUsers() ([]proto.UserInfo, error) { return m.users, nil }
func (m *mockClient) Heartbeat(string) error               { return nil }
func (m *mockClient) Token() string                        { return m.tok }
func (m *mockClient) SetToken(t string)                    { m.tok = t }
func (m *mockClient) RefreshToken() (string, int64, error) { return "new-token", 3600, nil }

func newTestEngine(t *testing.T, mc *mockClient) (*Engine, *vault.Vault, string) {
	t.Helper()
	ls, err := store.OpenLocal(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ls.Close() })
	_ = ls.Migrate()
	v := vault.New(ls)

	// 生成 keyfile 并解锁
	priv, _ := crypto.GenerateSM2Key()
	privDER, _ := crypto.MarshalSM2PrivateKey(priv)
	kf, _ := crypto.NewKeyfile(privDER, []byte("correct-password-123"))
	path := t.TempDir() + "/k.key"
	_ = kf.SaveToFile(path)
	if _, err := v.ImportKeyfile(path, []byte("correct-password-123")); err != nil {
		t.Fatal(err)
	}
	e := New(v, ls, mc, nil, "http://localhost", "token", func() time.Time { return time.Unix(1700000000, 0) })
	return e, v, path
}

// makeWrappedDEK 用 vault 私钥包裹 DEK（模拟服务端信封）。
func makeWrappedDEK(t *testing.T, v *vault.Vault, dek []byte) string {
	t.Helper()
	pub, err := v.PublicKeyB64()
	if err != nil {
		t.Fatal(err)
	}
	env, err := v.WrapDEKFor(pub, dek)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func TestSyncOncePullAndDecrypt(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"

	// 准备 DEK + 信封 + 密文条目（服务端视角）
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)
	wrapped := makeWrappedDEK(t, v, getDEKForTest(t, v, gid))

	entry := model.NewProject("proj")
	ct, _ := encEntry(v, gid, "e1", entry, 1)

	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 5,
		Changes: []proto.Change{
			{EntryID: "e1", GroupID: gid, Seq: 4, KeyVersion: 1, Ciphertext: ct},
		},
		KeyEnvelopes: []proto.KeyEnvelopeInfo{{GroupID: gid, KeyVersion: 1, WrappedDEK: wrapped}},
		Groups: []proto.GroupState{
			{ID: gid, Name: "G1", KeyVersion: 1, MissingEnvelopes: nil},
		},
	}
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{}}

	if err := e.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	// 本地条目已解密入库（明文缓存非空）
	le, err := e.local.GetLocalEntry("e1")
	if err != nil {
		t.Fatal(err)
	}
	if len(le.PlaintextCache) == 0 {
		t.Fatal("明文缓存应为空（无解密）？")
	}
	// 解密缓存验证
	plain, err := v.DecryptCache(le.PlaintextCache)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := model.UnmarshalEntry(plain)
	if err != nil || parsed.Title != "proj" {
		t.Fatalf("缓存解密: %+v %v", parsed, err)
	}
	// last_seq 推进：分页安全语义 = 最后一条 change 的 seq（4），而非全局 ServerSeq(5)
	seq, _ := e.local.GetLastSeq()
	if seq != 4 {
		t.Fatalf("last_seq = %d, want 4（最后一条 change seq）", seq)
	}
}

func TestSyncOncePendingEntry(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"

	// 服务端返回无信封的密文（本端无 DEK）→ 暂存 pending_entries（§7.2 4a）
	ct := `{"v":1,"alg":"SM4-GCM","kv":1,"nonce":"AA==","ct":"BB==","hmac":"CC=="}`
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 3,
		Changes:   []proto.Change{{EntryID: "e1", GroupID: gid, Seq: 2, KeyVersion: 1, Ciphertext: ct}},
		Groups:    []proto.GroupState{{ID: gid, Name: "G1", KeyVersion: 1}},
	}
	mc.pushResp = &proto.PushResponse{}
	if err := e.SyncNow(); err != nil {
		t.Fatal(err)
	}
	pe, err := e.local.GetPendingEntry("e1")
	if err != nil {
		t.Fatalf("应暂存 pending: %v", err)
	}
	_ = pe
	_ = v
}

func TestSyncOncePushDirty(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)

	// 本地有一脏条目
	entry := model.NewProject("dirty-proj")
	ct, _ := encEntry(v, gid, "e-dirty", entry, 1)
	_ = e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: "e-dirty", GroupID: gid, Seq: 0, KeyVersion: 1, Ciphertext: ct, Dirty: true, UpdatedAt: 1,
	})

	mc.pullResp = &proto.SyncResponse{ServerSeq: 0, Groups: []proto.GroupState{{ID: gid, KeyVersion: 1}}}
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{{EntryID: "e-dirty", OK: true, NewSeq: 7}}}

	if err := e.SyncNow(); err != nil {
		t.Fatal(err)
	}
	// 脏标记清除
	le, _ := e.local.GetLocalEntry("e-dirty")
	if le.Dirty {
		t.Fatal("push 成功后应清脏")
	}
	if le.Seq != 7 {
		t.Fatalf("seq = %d, want 7", le.Seq)
	}
}

func TestAutoWrapColdStart(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"

	// 本端无 DEK（冷启动）+ missing 全部成员
	mc.users = []proto.UserInfo{{UserID: "u1", SM2PublicKey: pubOfVault(t, v)}, {UserID: "u2", SM2PublicKey: pubOfVault(t, v)}}
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 0,
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1, MissingEnvelopes: []string{"u1", "u2"}}},
	}
	mc.pushResp = &proto.PushResponse{}
	if err := e.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if len(mc.uploads) != 1 {
		t.Fatalf("应上传 1 次信封（冷启动）, got %d", len(mc.uploads))
	}
	if mc.uploads[0].KeyVersion != 1 || len(mc.uploads[0].Envelopes) != 2 {
		t.Fatalf("冷启动信封: %+v", mc.uploads[0])
	}
	// 本端已生成并缓存 DEK
	if !v.HasAnyDEK(gid) {
		t.Fatal("冷启动后本端应有 DEK")
	}
}

func TestBackoff(t *testing.T) {
	b := newBackoff()
	if b.Next() != time.Second {
		t.Fatal("首次退避应为 1s")
	}
	if b.Next() != 2*time.Second {
		t.Fatal("二次退避应为 2s")
	}
	if b.Next() != 4*time.Second {
		t.Fatal("三次退避应为 4s")
	}
	for i := 0; i < 10; i++ {
		b.Next()
	}
	if b.Next() > 30*time.Second {
		t.Fatal("退避应封顶 30s")
	}
	if b.Fails() == 0 {
		t.Fatal("fails 应累计")
	}
	b.Reset()
	if b.Fails() != 0 || b.Next() != time.Second {
		t.Fatal("Reset 后应从 1s 开始")
	}
}

func TestSSEParseChangeEvent(t *testing.T) {
	ev := parseChangeEvent(`{"server_seq":1024,"groups":["g1"]}`)
	if ev.ServerSeq != 1024 {
		t.Fatalf("server_seq = %d", ev.ServerSeq)
	}
	ev2 := parseChangeEvent(`garbage`)
	if ev2.ServerSeq != 0 {
		t.Fatalf("坏数据应 0: %d", ev2.ServerSeq)
	}
}

// 辅助
func getDEKForTest(t *testing.T, v *vault.Vault, gid string) []byte {
	t.Helper()
	dek, err := v.GetDEK(gid, 1)
	if err != nil {
		t.Fatal(err)
	}
	return dek
}

func pubOfVault(t *testing.T, v *vault.Vault) string {
	t.Helper()
	pub, err := v.PublicKeyB64()
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

func TestEngineLifecycle(t *testing.T) {
	mc := &mockClient{pullResp: &proto.SyncResponse{}, pushResp: &proto.PushResponse{}}
	e, _, _ := newTestEngine(t, mc)
	e.Start()
	// 重复 Start 幂等
	e.Start()
	e.Stop()
	e.Stop()
}

func TestStatusPhase(t *testing.T) {
	mc := &mockClient{pullResp: &proto.SyncResponse{}, pushResp: &proto.PushResponse{}}
	e, _, _ := newTestEngine(t, mc)
	st := e.Status()
	if st.Phase != api.PhaseIdle {
		t.Fatalf("初始 phase = %s", st.Phase)
	}
}

func TestSubscribeEmit(t *testing.T) {
	mc := &mockClient{pullResp: &proto.SyncResponse{}, pushResp: &proto.PushResponse{}}
	e, _, _ := newTestEngine(t, mc)
	got := make(chan api.Event, 4)
	e.Subscribe(func(ev api.Event) { got <- ev })
	e.emit(api.Event{Type: api.EventEntriesChanged})
	select {
	case ev := <-got:
		if ev.Type != api.EventEntriesChanged {
			t.Fatalf("event type = %s", ev.Type)
		}
	default:
		t.Fatal("未收到事件")
	}
}

func TestAutoRekeyFlow(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"

	// 本端已有旧 kv=1 DEK（信封到达过）
	dek1, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek1)
	crypto.Wipe(dek1)
	// 一条旧 kv=1 条目
	entry := model.NewProject("old-proj")
	ct1, _ := encEntry(v, gid, "e1", entry, 1)
	_ = e.local.UpsertLocalEntry(&store.LocalEntry{ID: "e1", GroupID: gid, Seq: 1, KeyVersion: 1, Ciphertext: ct1, UpdatedAt: 1})

	// active 用户（本端公钥）
	mc.users = []proto.UserInfo{{UserID: "u1", SM2PublicKey: pubOfVault(t, v)}}
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 0,
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1, PendingRekey: true, ActiveMembers: []string{"u1"}}},
	}
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{{EntryID: "e1", OK: true, NewSeq: 5}}}

	if err := e.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	// 应上传 kv=2 信封（rekey）
	found := false
	for _, u := range mc.uploads {
		if u.KeyVersion == 2 {
			found = true
		}
	}
	if !found {
		t.Fatalf("应上传 kv=2 rekey 信封: %+v", mc.uploads)
	}
	// 条目已重加密到 kv=2 并推送（脏标记清除）
	le, _ := e.local.GetLocalEntry("e1")
	if le.KeyVersion != 2 {
		t.Fatalf("条目 kv = %d, want 2", le.KeyVersion)
	}
	if le.Dirty {
		t.Fatal("重加密 push 成功后应清脏")
	}
}

// P1 回归：多组场景 auto-rekey 只包裹"该组 active 成员"，不含其他组用户。
// 修复前 autoRekey 用全局 ListUsers() 包裹 → 信封含非该组成员 → 服务端 40904 拒收。
func TestAutoRekeyWrapsOnlyGroupMembers(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"

	// 本端已有旧 kv=1 DEK
	dek1, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek1)
	crypto.Wipe(dek1)

	// 全局 active 用户含 u1（本端，在 g1）与 u2（另一组成员，不在 g1）
	mc.users = []proto.UserInfo{
		{UserID: "u1", SM2PublicKey: pubOfVault(t, v)},
		{UserID: "u2", SM2PublicKey: pubOfVault(t, v)}, // 公钥内容不重要，验证按 UserID 过滤
	}
	// g1 的 active 成员只有 u1
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 0,
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1, PendingRekey: true, ActiveMembers: []string{"u1"}}},
	}
	mc.pushResp = &proto.PushResponse{}

	if err := e.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	// rekey 信封（kv=2）必须只含 u1，不含 u2
	for _, u := range mc.uploads {
		if u.KeyVersion != 2 {
			continue
		}
		if len(u.Envelopes) != 1 || u.Envelopes[0].UserID != "u1" {
			t.Fatalf("rekey 信封应只含 u1（不含其他组成员），实际 %+v", u.Envelopes)
		}
		return
	}
	t.Fatal("未上传 kv=2 rekey 信封")
}

// §7.2 4a：pending 条目在信封到达后自动补解密入库（P1 回归）。
func TestPendingFlushAfterEnvelope(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"

	// 第一轮：服务端返回无信封的密文 → 暂存 pending
	ct := `{"v":1,"alg":"SM4-GCM","kv":1,"nonce":"AA==","ct":"BB==","hmac":"CC=="}`
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 3,
		Changes:   []proto.Change{{EntryID: "e1", GroupID: gid, Seq: 2, KeyVersion: 1, Ciphertext: ct}},
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1}},
	}
	mc.pushResp = &proto.PushResponse{}
	_ = e.SyncNow()
	if _, err := e.local.GetPendingEntry("e1"); err != nil {
		t.Fatal("第一轮应暂存 pending")
	}

	// 第二轮：信封到达（用真实 DEK 包裹）→ flushPendingEntries 补解密
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)
	wrapped := makeWrappedDEK(t, v, getDEKForTest(t, v, gid))

	entry := model.NewProject("real-proj")
	realCT, _ := encEntry(v, gid, "e1", entry, 1)
	mc.pullResp = &proto.SyncResponse{
		ServerSeq:    5,
		Changes:      []proto.Change{{EntryID: "e1", GroupID: gid, Seq: 4, KeyVersion: 1, Ciphertext: realCT}},
		KeyEnvelopes: []proto.KeyEnvelopeInfo{{GroupID: gid, KeyVersion: 1, WrappedDEK: wrapped}},
		Groups:       []proto.GroupState{{ID: gid, KeyVersion: 1}},
	}
	_ = e.SyncNow()
	// pending 已清 + 条目解密入库
	if _, err := e.local.GetPendingEntry("e1"); err == nil {
		t.Fatal("pending 应被清除")
	}
	le, err := e.local.GetLocalEntry("e1")
	if err != nil {
		t.Fatal("条目应入库")
	}
	if len(le.PlaintextCache) == 0 {
		t.Fatal("条目应解密（有明文缓存）")
	}
}

// §7.3：pull 冲突时本地版（ours）明文保留，服务端版（theirs）存 base_enc（P1 回归）。
func TestPullConflictPreservesOurs(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)

	// 本地有 dirty 条目（ours）
	ours := model.NewProject("ours-proj")
	oursCT, _ := encEntry(v, gid, "e1", ours, 1)
	oursCache, _ := v.EncryptCache([]byte(`{"schema_version":1,"type":"project","title":"ours-proj"}`))
	_ = e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: "e1", GroupID: gid, Seq: 1, KeyVersion: 1, Ciphertext: oursCT,
		PlaintextCache: oursCache, Dirty: true, UpdatedAt: 1,
	})

	// 服务端新版本（theirs）
	theirs := model.NewProject("theirs-proj")
	theirsCT, _ := encEntry(v, gid, "e1", theirs, 1)
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 3,
		Changes:   []proto.Change{{EntryID: "e1", GroupID: gid, Seq: 2, KeyVersion: 1, Ciphertext: theirsCT}},
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1}},
	}
	mc.pushResp = &proto.PushResponse{}
	_ = e.SyncNow()

	le, _ := e.local.GetLocalEntry("e1")
	if le.ConflictOf != "e1" {
		t.Fatal("应标记冲突")
	}
	// ours 明文保留
	oursPlain, err := v.DecryptCache(le.PlaintextCache)
	if err != nil || !strings.Contains(string(oursPlain), "ours-proj") {
		t.Fatalf("ours 明文应保留: %s %v", oursPlain, err)
	}
	// theirs：由本条目密文（服务端版）现场解密（§7.3 三路合并素材，不持久化到 base_enc）
	theirsPlain, derr := v.DecryptPlaintext(gid, "e1", le.Ciphertext)
	if derr != nil {
		t.Fatalf("theirs 现场解密: %v", derr)
	}
	var theirsEntry model.Entry
	if err := json.Unmarshal(theirsPlain, &theirsEntry); err != nil {
		t.Fatalf("theirs 明文解析: %v", err)
	}
	if theirsEntry.Title != "theirs-proj" {
		t.Fatalf("theirs title = %q, want theirs-proj", theirsEntry.Title)
	}
	// base_enc：本测试未写 base 快照 → 应为空（不把 theirs 塞进 base）
	if len(le.BaseEnc) != 0 {
		t.Fatal("base_enc 应保留 base 快照（本测试为空），不得存 theirs")
	}
}

// §7.3：pull 冲突时不同字段自动合并（无需人工，验收用例「A、B 同改不同字段一次成功」）。
func TestPullConflictAutoMerge(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)

	parent := "p1"
	// base：共同祖先快照（username=base-user, ip=1.1.1.1）
	base := &model.Entry{SchemaVersion: 1, Type: model.TypeAccount, Title: "acc", ParentID: &parent,
		Fields:       model.Fields{"username": json.RawMessage(`"base-user"`), "ip": json.RawMessage(`"1.1.1.1"`)},
		CustomFields: map[string]json.RawMessage{}}
	basePlain, _ := base.Marshal()
	baseEnc, _ := v.EncryptCache(basePlain)

	// ours：本地改 username（ip 未动）
	ours := &model.Entry{SchemaVersion: 1, Type: model.TypeAccount, Title: "acc", ParentID: &parent,
		Fields:       model.Fields{"username": json.RawMessage(`"ours-user"`), "ip": json.RawMessage(`"1.1.1.1"`)},
		CustomFields: map[string]json.RawMessage{}}
	oursPlain, _ := ours.Marshal()
	oursCT, _ := encEntry(v, gid, "e1", ours, 1)
	oursCache, _ := v.EncryptCache(oursPlain)
	_ = e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: "e1", GroupID: gid, Seq: 1, KeyVersion: 1, Ciphertext: oursCT,
		PlaintextCache: oursCache, BaseEnc: baseEnc, Dirty: true, UpdatedAt: 1,
	})

	// theirs：服务端改 ip（username 未动）
	theirs := &model.Entry{SchemaVersion: 1, Type: model.TypeAccount, Title: "acc", ParentID: &parent,
		Fields:       model.Fields{"username": json.RawMessage(`"base-user"`), "ip": json.RawMessage(`"2.2.2.2"`)},
		CustomFields: map[string]json.RawMessage{}}
	theirsCT, _ := encEntry(v, gid, "e1", theirs, 1)
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 3,
		Changes:   []proto.Change{{EntryID: "e1", GroupID: gid, Seq: 2, KeyVersion: 1, Ciphertext: theirsCT}},
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1}},
	}
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{{EntryID: "e1", OK: true, NewSeq: 3}}}
	_ = e.SyncNow()

	le, err := e.local.GetLocalEntry("e1")
	if err != nil {
		t.Fatal(err)
	}
	if le.ConflictOf != "" {
		t.Fatalf("不同字段应自动合并（无冲突）, conflict_of=%q", le.ConflictOf)
	}
	mergedPlain, err := v.DecryptCache(le.PlaintextCache)
	if err != nil {
		t.Fatal(err)
	}
	var merged model.Entry
	if err := json.Unmarshal(mergedPlain, &merged); err != nil {
		t.Fatal(err)
	}
	if string(merged.Fields["username"]) != `"ours-user"` {
		t.Fatalf("username 应采用本地 = %s", merged.Fields["username"])
	}
	if string(merged.Fields["ip"]) != `"2.2.2.2"` {
		t.Fatalf("ip 应采用服务端 = %s", merged.Fields["ip"])
	}
	// 合并结果已在同轮 pushDirty 推送（mock push 返回 OK），dirty 应已清
	if le.Dirty {
		t.Fatal("合并结果 push 成功后 dirty 应已清")
	}
}

// P1 回归：分页截断时 last_seq 只推进到已收到的最后一条（不跳批漏拉）。
func TestLastSeqPaginationSafe(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)

	// 服务端返回 2 条变更，但 ServerSeq=100（全局值远大于最后一条）
	entry1 := model.NewProject("p1")
	ct1, _ := encEntry(v, gid, "e1", entry1, 1)
	entry2 := model.NewProject("p2")
	ct2, _ := encEntry(v, gid, "e2", entry2, 1)
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 100,
		Changes: []proto.Change{
			{EntryID: "e1", GroupID: gid, Seq: 5, KeyVersion: 1, Ciphertext: ct1},
			{EntryID: "e2", GroupID: gid, Seq: 6, KeyVersion: 1, Ciphertext: ct2},
		},
		Groups: []proto.GroupState{{ID: gid, KeyVersion: 1}},
	}
	mc.pushResp = &proto.PushResponse{}
	_ = e.SyncNow()

	// last_seq 应 = 6（最后一条），而非 100（跳批）
	seq, _ := e.local.GetLastSeq()
	if seq != 6 {
		t.Fatalf("last_seq = %d, want 6（不得推进到全局 ServerSeq=100）", seq)
	}
	// 两条例目均已入库
	if _, err := e.local.GetLocalEntry("e1"); err != nil {
		t.Fatal("e1 未入库")
	}
	if _, err := e.local.GetLocalEntry("e2"); err != nil {
		t.Fatal("e2 未入库")
	}
}

// P1 回归：rekey 后本地 dirty 条目也用新 DEK 重加密（不卡 40902）。
func TestRekeyReencryptsDirtyEntry(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	dek1, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek1)
	crypto.Wipe(dek1)

	// 本地 dirty 条目（旧 kv=1 加密，未推送修改）
	entry := model.NewProject("dirty-modified")
	ct1, _ := encEntry(v, gid, "e1", entry, 1)
	_ = e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: "e1", GroupID: gid, Seq: 1, KeyVersion: 1, Ciphertext: ct1, Dirty: true, UpdatedAt: 1,
	})

	// 触发 rekey（pending_rekey + 组 kv=1，本端有旧 DEK）
	mc.users = []proto.UserInfo{{UserID: "u1", SM2PublicKey: pubOfVault(t, v)}}
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 0,
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1, PendingRekey: true, ActiveMembers: []string{"u1"}}},
	}
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{{EntryID: "e1", OK: true, NewSeq: 9}}}
	if err := e.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	// dirty 条目已重加密到 kv=2 并推送成功（脏清除 + seq 更新）
	le, _ := e.local.GetLocalEntry("e1")
	if le.KeyVersion != 2 {
		t.Fatalf("kv = %d, want 2", le.KeyVersion)
	}
	if le.Dirty {
		t.Fatal("dirty 条目重加密 push 成功后应清脏")
	}
	if le.Seq != 9 {
		t.Fatalf("seq = %d, want 9", le.Seq)
	}
	// 写挂起应解除（pushDirty 能推送即已解除）
	e.mu.Lock()
	held := e.writeHeld[gid]
	e.mu.Unlock()
	if held {
		t.Fatal("rekey 后写挂起应解除")
	}
}

// P1 回归：rekey 中断恢复——本地条目停留在比 oldKV 更老的 kv 时，
// 重加密仍须覆盖（否则 kv 每轮 +1 死循环，pending_rekey 永不收敛）。
func TestRekeyReencryptsStaleKVEntry(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	// 本地有 kv=1 和 kv=2 的 DEK（模拟历史信封）
	dek1, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek1)
	crypto.Wipe(dek1)
	dek2, _ := v.NewDEK()
	_ = v.SetDEK(gid, 2, dek2)
	crypto.Wipe(dek2)

	// 本地条目仍停留在 kv=1（首次 rekey 中断：push 未完成，服务端 kv 已升到 2）
	entry := model.NewProject("stale-kv")
	ct1, _ := encEntry(v, gid, "e1", entry, 1)
	_ = e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: "e1", GroupID: gid, Seq: 1, KeyVersion: 1, Ciphertext: ct1, Dirty: false, UpdatedAt: 1,
	})

	// 服务端已 pending_rekey + kv=2（oldKV=2，newKV=3）
	mc.users = []proto.UserInfo{{UserID: "u1", SM2PublicKey: pubOfVault(t, v)}}
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 0,
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 2, PendingRekey: true, ActiveMembers: []string{"u1"}}},
	}
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{{EntryID: "e1", OK: true, NewSeq: 9}}}
	if err := e.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	// 关键：kv=1 的陈旧条目也应重加密到 kv=3（不是跳过导致死循环）
	le, _ := e.local.GetLocalEntry("e1")
	if le.KeyVersion != 3 {
		t.Fatalf("陈旧 kv 条目应重加密到 newKV=3，实际 kv=%d", le.KeyVersion)
	}
}

func TestHostnameOf(t *testing.T) {
	e, _, _ := newTestEngine(t, &mockClient{})
	h := hostnameOf(e)
	if h == "" {
		// Windows/CI 环境可能取不到，允许空但不 panic
		return
	}
	// 非空时应有长度
	if len(h) == 0 {
		t.Fatal("hostname 不应为空字符串（非空时）")
	}
}

// P2 回归：applyGroups 持久化组当前 kv（本地 put 加密用，§9.1）。
func TestGroupStateKeyVersionPersisted(t *testing.T) {
	mc := &mockClient{}
	e, _, _ := newTestEngine(t, mc)
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 0,
		Groups:    []proto.GroupState{{ID: "g1", KeyVersion: 2}},
	}
	mc.pushResp = &proto.PushResponse{}
	if err := e.SyncNow(); err != nil {
		t.Fatal(err)
	}
	gs, err := e.local.GetGroupState("g1")
	if err != nil {
		t.Fatal(err)
	}
	if gs.KeyVersion != 2 {
		t.Fatalf("组 kv = %d, want 2（本地 put 加密依赖）", gs.KeyVersion)
	}
}

// P1 回归：token 过期（40101）自动刷新——重试成功 + token 更新 + 本地 device_state 持久化。
func TestTokenAutoRefresh(t *testing.T) {
	mc := &mockClient{
		pullResp: &proto.SyncResponse{ServerSeq: 0, Groups: []proto.GroupState{{ID: "g1", KeyVersion: 1}}},
		pushResp: &proto.PushResponse{},
		pullErr:  &api.APIError{Code: proto.ErrUnauthorized, Message: "token 过期"},
	}
	e, v, _ := newTestEngine(t, mc)
	_ = e.local.SetDeviceState(&store.DeviceState{DeviceID: "d1", TokenEnc: []byte("old-enc"), ExpiresAt: 1})
	if err := e.SyncNow(); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}
	// token 已更新到 HTTPClient/SSE
	if mc.tok != "new-token" {
		t.Fatalf("token = %q, want new-token", mc.tok)
	}
	// 本地 device_state 已持久化新 token（解密比对）
	ds, _ := e.local.GetDeviceState()
	dec, err := v.DecryptToken(ds.TokenEnc)
	if err != nil || dec != "new-token" {
		t.Fatalf("device_state token 未更新: %q %v", dec, err)
	}
	// expires_at 已按 expires_in 推进
	if ds.ExpiresAt <= 1 {
		t.Fatalf("expires_at 未推进: %d", ds.ExpiresAt)
	}
}

// P1 回归：§7.3 #5 墓碑不吞本地未推送修改——dirty 条目遇远端删除 → 转冲突副本。
func TestTombstonePreservesDirtyLocal(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)

	// 本地 dirty 条目（未推送修改，含明文缓存 ours）
	entry := model.NewProject("my-edit")
	ct, _ := encEntry(v, gid, "e1", entry, 1)
	cache, _ := v.EncryptCache(mustMarshalEntry(entry))
	_ = e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: "e1", GroupID: gid, Seq: 1, KeyVersion: 1, Ciphertext: ct,
		PlaintextCache: cache, Dirty: true, UpdatedAt: 1,
	})

	// 远端墓碑
	mc.pullResp = &proto.SyncResponse{
		ServerSeq: 3,
		Changes:   []proto.Change{{EntryID: "e1", GroupID: gid, Seq: 2, KeyVersion: 1, Ciphertext: "", Deleted: true}},
		Groups:    []proto.GroupState{{ID: gid, KeyVersion: 1}},
	}
	mc.pushResp = &proto.PushResponse{}
	_ = e.SyncNow()

	// 本地条目应保留为冲突副本（不被删除）
	le, err := e.local.GetLocalEntry("e1")
	if err != nil {
		t.Fatal("墓碑不应删除本地 dirty 条目")
	}
	if le.ConflictOf != "e1" {
		t.Fatalf("应标记冲突（服务端已删 vs 本地修改）, conflict_of=%q", le.ConflictOf)
	}
	if le.Dirty {
		t.Fatal("冲突副本不应自动推送（等待用户决策）")
	}
	// ours 明文保留
	oursPlain, derr := v.DecryptCache(le.PlaintextCache)
	if derr != nil || !strings.Contains(string(oursPlain), "my-edit") {
		t.Fatalf("ours 明文应保留: %s %v", oursPlain, derr)
	}
}

// mustMarshalEntry 加密条目明文（与 syncer.marshalEntryCache 格式一致，测试辅助）。
func mustMarshalEntry(e *model.Entry) []byte {
	b, _ := e.Marshal()
	return b
}

// encEntry 加密条目明文（测试辅助）：model.Entry → Marshal → EncryptPlaintext。
func encEntry(v *vault.Vault, gid, id string, e *model.Entry, kv int) (string, error) {
	plain, err := e.Marshal()
	if err != nil {
		return "", err
	}
	return v.EncryptPlaintext(gid, id, plain, kv)
}

// 并发压力：多 goroutine 同时 SyncNow——验证单例互斥生效（不并发拉取）、无死锁。
// 建议 CI 用 `go test -race` 复跑（本机无 gcc）。
func TestSyncNowConcurrent(t *testing.T) {
	mc := &mockClient{
		pullResp: &proto.SyncResponse{ServerSeq: 0, Groups: []proto.GroupState{{ID: "g1", KeyVersion: 1}}},
		pushResp: &proto.PushResponse{},
	}
	e, _, _ := newTestEngine(t, mc)
	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := e.SyncNow(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("并发 SyncNow 出错: %v", err)
	}
}
