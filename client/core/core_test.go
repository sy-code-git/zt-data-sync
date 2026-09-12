package core

import (
	"encoding/json"
	"strings"
	"testing"

	"passbook/client/core/api"
	"passbook/client/core/store"
	"passbook/client/core/vault"
	"passbook/internal/crypto"
)

// projPlain 构造一条 project 明文 JSON（core 纯管道：明文由调用方序列化）。
func projPlain(title string) []byte {
	b, _ := json.Marshal(map[string]any{
		"schema_version": 1, "type": "project", "title": title,
		"fields": map[string]any{}, "custom_fields": map[string]any{},
	})
	return b
}

// plainTitle 从明文 JSON 字节取 title（测试断言用）。
func plainTitle(b []byte) string {
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	s, _ := m["title"].(string)
	return s
}

// newTestCore 构造已解锁的 Core（不启动 engine，纯本地操作）。
func newTestCore(t *testing.T) (*Core, *vault.Vault) {
	t.Helper()
	ls, err := store.OpenLocal(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ls.Close() })
	if err := ls.Migrate(); err != nil {
		t.Fatal(err)
	}
	c := New(ls, "https://example.test")

	// 生成密钥对并解锁（方案 A：GenerateKeypair 内部存 identity + 解锁）
	if _, err := c.GenerateKeypair("u1", "member", []byte("correct-password-123")); err != nil {
		t.Fatal(err)
	}
	return c, c.vault
}

// ClearIdentity 必须同时复位「内存身份」与「本地库」：
// Role()/Username() 读的是内存字段，只清库会让同一进程内仍被视为已有身份 →
// 注册失败回滚后前端预检拦下重试，用户必须重启客户端（实测复现）。
func TestClearIdentityResetsMemoryAndStore(t *testing.T) {
	c, _ := newTestCore(t)
	if c.Role() != "member" || c.Username() != "u1" {
		t.Fatalf("前置身份 = role %q / username %q, want member / u1", c.Role(), c.Username())
	}
	if err := c.ClearIdentity(); err != nil {
		t.Fatal(err)
	}
	if c.Role() != "" {
		t.Fatalf("ClearIdentity 后 Role() = %q, want 空（内存身份残留会导致注册无法重试）", c.Role())
	}
	if c.Username() != "" {
		t.Fatalf("ClearIdentity 后 Username() = %q, want 空", c.Username())
	}
	if c.IsUnlocked() {
		t.Fatal("ClearIdentity 后应处于锁定态（内存私钥已清）")
	}
	if id, err := c.local.GetIdentity(); err == nil && id != nil {
		t.Fatalf("本地 identity 应已删除, got %+v", id)
	}
}

func TestCoreUnlockLock(t *testing.T) {
	c, _ := newTestCore(t)
	if !c.IsUnlocked() {
		t.Fatal("应已解锁")
	}
	c.Lock()
	if c.IsUnlocked() {
		t.Fatal("Lock 后应锁定")
	}
	// 锁定态操作报错
	if err := c.PutEntry(&api.PutEntryRequest{GroupID: "g1", Plaintext: projPlain("x")}); err == nil {
		t.Fatal("锁定态 PutEntry 应报错")
	}
	if _, err := c.ListEntries(); err == nil {
		t.Fatal("锁定态 ListEntries 应报错")
	}
}

func TestCorePutListGet(t *testing.T) {
	c, v := newTestCore(t)
	gid := "g1"
	// 组状态（kv=1）+ DEK
	dek, _ := v.NewDEK()
	if err := v.SetDEK(gid, 1, dek); err != nil {
		t.Fatal(err)
	}
	crypto.Wipe(dek)
	_ = c.local.SetGroupState(&store.GroupState{GroupID: gid, KeyVersion: 1})

	// Put 新建
	err := c.PutEntry(&api.PutEntryRequest{
		GroupID: gid, Plaintext: projPlain("测试项目"),
	})
	if err != nil {
		t.Fatalf("PutEntry: %v", err)
	}
	// List 应包含
	entries, err := c.ListEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || plainTitle(entries[0].Plaintext) != "测试项目" {
		t.Fatalf("ListEntries: %+v", entries)
	}
	// Get 单条
	ev, err := c.GetEntry(entries[0].ID)
	if err != nil || plainTitle(ev.Plaintext) != "测试项目" {
		t.Fatalf("GetEntry: %v %+v", err, ev)
	}
	// 无明文条目（无 plaintext_cache）跳过
	_ = c.local.UpsertLocalEntry(&store.LocalEntry{ID: "pending-1", GroupID: gid, Seq: 9, KeyVersion: 1, Ciphertext: "ct", UpdatedAt: 1})
	entries2, _ := c.ListEntries()
	if len(entries2) != 1 {
		t.Fatalf("无明文条目应跳过: %d", len(entries2))
	}
}

func TestCoreDeleteEntry(t *testing.T) {
	c, v := newTestCore(t)
	gid := "g1"
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)
	_ = c.local.SetGroupState(&store.GroupState{GroupID: gid, KeyVersion: 1})

	_ = c.PutEntry(&api.PutEntryRequest{GroupID: gid, Plaintext: projPlain("待删")})
	entries, _ := c.ListEntries()
	id := entries[0].ID

	if err := c.DeleteEntry(id); err != nil {
		t.Fatalf("DeleteEntry: %v", err)
	}
	// 本地墓碑：deleted=true + dirty=true + 明文缓存清空
	le, err := c.local.GetLocalEntry(id)
	if err != nil {
		t.Fatal(err)
	}
	if !le.Deleted || !le.Dirty {
		t.Fatalf("墓碑标记: deleted=%v dirty=%v", le.Deleted, le.Dirty)
	}
	// List 不再展示（无明文缓存 → 跳过）
	entries2, _ := c.ListEntries()
	if len(entries2) != 0 {
		t.Fatalf("删除后不应展示: %d", len(entries2))
	}
}

func TestCoreGeneratePassword(t *testing.T) {
	c, _ := newTestCore(t)
	pw, err := c.GeneratePassword(20, true, true, true, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 20 {
		t.Fatalf("len = %d", len(pw))
	}
	// excludeAmbiguous：无 0/O/1/l/I
	pw2, _ := c.GeneratePassword(50, true, true, true, false, true)
	if strings.ContainsAny(pw2, "0Oo1lI|") {
		t.Fatalf("应排除易混淆字符: %q", pw2)
	}
	// 全不选字符集 → 报错
	if _, err := c.GeneratePassword(8, false, false, false, false, false); err == nil {
		t.Fatal("无字符集应报错")
	}
}
