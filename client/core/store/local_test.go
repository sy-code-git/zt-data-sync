package store

import (
	"testing"
	"time"
)

func TestLocalStoreRoundTrip(t *testing.T) {
	s, err := OpenLocal(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}

	// 设备状态
	ds := &DeviceState{DeviceID: "d1", TokenEnc: []byte("enc-token"), ExpiresAt: time.Now().Unix() + 3600}
	if err := s.SetDeviceState(ds); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetDeviceState()
	if err != nil || got.DeviceID != "d1" || string(got.TokenEnc) != "enc-token" {
		t.Fatalf("device state: %v %+v", err, got)
	}

	// 同步状态
	if err := s.SetLastSeq(42); err != nil {
		t.Fatal(err)
	}
	seq, _ := s.GetLastSeq()
	if seq != 42 {
		t.Fatalf("last_seq = %d", seq)
	}

	// 组状态（含 key_version 持久化，P2 fix）
	gs := &GroupState{GroupID: "g1", Archived: true, KeyVersion: 2, InitializedAt: 100}
	_ = s.SetGroupState(gs)
	g, _ := s.GetGroupState("g1")
	if g == nil || !g.Archived || g.KeyVersion != 2 || g.InitializedAt != 100 {
		t.Fatalf("group state: %+v", g)
	}
	states, _ := s.ListGroupStates()
	if len(states) != 1 {
		t.Fatal("group states = 1")
	}

	// 本地条目
	le := &LocalEntry{ID: "e1", GroupID: "g1", Seq: 1, KeyVersion: 1, Ciphertext: "ct", PlaintextCache: []byte("pc"), Dirty: true, UpdatedAt: 10}
	if err := s.UpsertLocalEntry(le); err != nil {
		t.Fatal(err)
	}
	e, _ := s.GetLocalEntry("e1")
	if e == nil || !e.Dirty || e.Seq != 1 {
		t.Fatalf("entry: %+v", e)
	}
	_ = s.SetDirty("e1", false)
	e, _ = s.GetLocalEntry("e1")
	if e.Dirty {
		t.Fatal("dirty 应清除")
	}
	_ = s.SetBaseEnc("e1", []byte("base"))
	e, _ = s.GetLocalEntry("e1")
	if string(e.BaseEnc) != "base" {
		t.Fatal("base_enc 未设置")
	}
	dirtyList, _ := s.ListDirtyEntries()
	if len(dirtyList) != 0 {
		t.Fatal("dirty 列表应空")
	}
	all, _ := s.ListLocalEntries()
	if len(all) != 1 {
		t.Fatal("entries = 1")
	}

	// pending
	pe := &PendingEntry{ID: "pe1", GroupID: "g1", Seq: 2, KeyVersion: 1, Ciphertext: "pct", UpdatedAt: 20}
	_ = s.PutPendingEntry(pe)
	p, _ := s.GetPendingEntry("pe1")
	if p == nil || p.Ciphertext != "pct" {
		t.Fatalf("pending: %+v", p)
	}
	pends, _ := s.ListPendingEntries()
	if len(pends) != 1 {
		t.Fatal("pending = 1")
	}
	_ = s.DeletePendingEntry("pe1")
	if _, err := s.GetPendingEntry("pe1"); err == nil {
		t.Fatal("pending 应删除")
	}

	// DEK 缓存
	_ = s.PutDEK("g1", 1, []byte("dek-enc"))
	dek, _ := s.GetDEK("g1", 1)
	if string(dek) != "dek-enc" {
		t.Fatal("dek 缓存错误")
	}
	_ = s.DeleteGroupDEKs("g1")
	if _, err := s.GetDEK("g1", 1); err == nil {
		t.Fatal("dek 应删除")
	}

	// bad_seq
	_ = s.MarkBadSeq(5)
	_ = s.IncrementBadSeq(5)
	bad, _ := s.ListBadSeqs()
	if bad[5] != 2 {
		t.Fatalf("bad_seq fail_count = %d, want 2", bad[5])
	}
	_ = s.ClearBadSeq(5)
	bad, _ = s.ListBadSeqs()
	if len(bad) != 0 {
		t.Fatal("bad_seq 应清空")
	}

	// 回收站
	_ = s.PutRecycleBin("e1", "ct", 100)
	// 无读取接口，写入成功即可

	// 移除条目
	_ = s.RemoveLocalEntry("e1")
	if _, err := s.GetLocalEntry("e1"); err == nil {
		t.Fatal("条目应移除")
	}
}

func TestServerURLConfig(t *testing.T) {
	s, err := OpenLocal(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	// 未配置 → 空串
	u, _ := s.GetServerURL()
	if u != "" {
		t.Fatalf("初始 server_url = %q, want 空", u)
	}
	// 写入 + 覆盖
	if err := s.SetServerURL("https://host:8443"); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetServerURL()
	if u != "https://host:8443" {
		t.Fatalf("server_url = %q", u)
	}
	if err := s.SetServerURL("https://host2:9443"); err != nil {
		t.Fatal(err)
	}
	u, _ = s.GetServerURL()
	if u != "https://host2:9443" {
		t.Fatalf("覆盖后 server_url = %q", u)
	}
}
