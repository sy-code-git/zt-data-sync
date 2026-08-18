package store

import (
	"context"
	"errors"
	"testing"
)

// ---- 1.5 扩展：groups/entries/envelopes CRUD（直接覆盖 store 层） ----

func TestGroupCRUD(t *testing.T) {
	s := newTestStore(t)
	gid := "g1"
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.CreateGroup(&Group{ID: gid, Name: "G1", KeyVersion: 1, PendingRekey: RekeyDone, Archived: GroupNotArchived, CreatedAt: 1})
	}); err != nil {
		t.Fatal(err)
	}
	g, err := s.GetGroup(gid)
	if err != nil || g.Name != "G1" || g.KeyVersion != 1 {
		t.Fatalf("GetGroup: %v %+v", err, g)
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.SetGroupRekey(gid, RekeyPending) })
	g, _ = s.GetGroup(gid)
	if g.PendingRekey != RekeyPending {
		t.Fatal("rekey 置位失败")
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.SetGroupKeyVersion(gid, 2) })
	g, _ = s.GetGroup(gid)
	if g.KeyVersion != 2 {
		t.Fatal("kv 升级失败")
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.SetGroupArchived(gid, GroupArchived, 100) })
	g, _ = s.GetGroup(gid)
	if g.Archived != GroupArchived || g.ArchivedAt != 100 {
		t.Fatal("归档失败")
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.SetGroupArchived(gid, GroupNotArchived, 0) })
	g, _ = s.GetGroup(gid)
	if g.Archived != GroupNotArchived || g.ArchivedAt != 0 {
		t.Fatal("重启失败")
	}
	groups, err := s.ListGroups()
	if err != nil || len(groups) != 1 {
		t.Fatalf("ListGroups: %v %d", err, len(groups))
	}
	if _, err := s.GetGroup("nope"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("不存在组应 ErrNoRows: %v", err)
	}
}

func TestGroupMembersCRUD(t *testing.T) {
	s := newTestStore(t)
	gid := "g1"
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateGroup(&Group{ID: gid, Name: "G1", KeyVersion: 1, CreatedAt: 1}) })
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.CreateUser(&User{ID: "u1", Name: "a", SM2PublicKey: "p", Attestation: "x", Role: RoleMember, Status: StatusActive, CreatedAt: 1})
	})
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.AddGroupMember(&GroupMember{GroupID: gid, UserID: "u1", CreatedAt: 1})
	})
	ok, _ := s.GetGroupMember(gid, "u1")
	if !ok {
		t.Fatal("成员应存在")
	}
	if ok, _ := s.GetGroupMember(gid, "nobody"); ok {
		t.Fatal("非成员应 false")
	}
	members, err := s.ListGroupMembers(gid)
	if err != nil || len(members) != 1 {
		t.Fatalf("ListGroupMembers: %v %d", err, len(members))
	}
	ugs, err := s.ListUserGroups("u1")
	if err != nil || len(ugs) != 1 || ugs[0].ID != gid {
		t.Fatalf("ListUserGroups: %v %+v", err, ugs)
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.RemoveGroupMember(gid, "u1") })
	if ok, _ := s.GetGroupMember(gid, "u1"); ok {
		t.Fatal("移除后成员不应存在")
	}
}

func TestEntryCRUDAndTombstone(t *testing.T) {
	s := newTestStore(t)
	gid := "g1"
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateGroup(&Group{ID: gid, Name: "G1", KeyVersion: 1, CreatedAt: 1}) })
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.CreateUser(&User{ID: "u1", Name: "a", SM2PublicKey: "p", Attestation: "x", Role: RoleMember, Status: StatusActive, CreatedAt: 1})
	})
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.CreateDevice(&Device{ID: "d1", UserID: "u1", Name: "pc", TokenHash: "h", Status: DeviceActive, CreatedAt: 1})
	})

	seq, err := s.(*sqliteStore).WithTxSeq(gid, "e1", 1, "ct1", false, "d1")
	if err != nil || seq != 1 {
		t.Fatalf("新增条目: seq=%d err=%v", seq, err)
	}
	ent, err := s.GetEntry("e1")
	if err != nil || ent.Seq != 1 || ent.KeyVersion != 1 || ent.Deleted {
		t.Fatalf("GetEntry: %v %+v", err, ent)
	}
	seq2, _ := s.(*sqliteStore).WithTxSeq(gid, "e1", 1, "ct2", false, "d1")
	if seq2 != 2 {
		t.Fatalf("更新 seq = %d, want 2", seq2)
	}
	_, _ = s.(*sqliteStore).WithTxSeq(gid, "e1", 1, "", true, "d1")
	ent, _ = s.GetEntry("e1")
	if !ent.Deleted {
		t.Fatal("墓碑标记失败")
	}
	// 同 id 只存最新版（§7.1：服务端只存一份密文），故 PullChanges 返回 1 行
	changes, err := s.PullChanges(0, 500)
	if err != nil || len(changes) != 1 {
		t.Fatalf("PullChanges: %v %d", err, len(changes))
	}
	below, _ := s.CountEntriesBelowKV(gid, 2)
	if below != 0 { // 唯一条目是墓碑（deleted=1，排除）
		t.Fatalf("CountEntriesBelowKV = %d, want 0", below)
	}
	n, _ := s.CountEntries(gid)
	if n != 1 {
		t.Fatalf("CountEntries = %d, want 1", n)
	}
	ss, _ := s.GetServerSeq()
	if ss != 3 {
		t.Fatalf("server_seq = %d, want 3", ss)
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.DeleteOldTombstones(0)
	})
	gc, err := s.PullGroupChanges(gid, 0, 500)
	if err != nil || len(gc) != 1 {
		t.Fatalf("PullGroupChanges: %v %d", err, len(gc))
	}
}

func TestEnvelopesCRUD(t *testing.T) {
	s := newTestStore(t)
	gid := "g1"
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateGroup(&Group{ID: gid, Name: "G1", KeyVersion: 1, CreatedAt: 1}) })
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.CreateUser(&User{ID: "u1", Name: "a", SM2PublicKey: "p", Attestation: "x", Role: RoleMember, Status: StatusActive, CreatedAt: 1})
	})
	env := &Envelope{GroupID: gid, KeyVersion: 1, UserID: "u1", WrappedDEK: "w1", UpdatedAt: 1}
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.UpsertEnvelope(env) }); err != nil {
		t.Fatal(err)
	}
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.UpsertEnvelope(env) }); !errors.Is(err, ErrConstraintUnique) {
		t.Fatalf("重复信封应 ErrConstraintUnique: %v", err)
	}
	has, _ := s.HasEnvelope(gid, 1, "u1")
	if !has {
		t.Fatal("信封应存在")
	}
	envs, err := s.GetGroupEnvelopes(gid, 1)
	if err != nil || len(envs) != 1 {
		t.Fatalf("GetGroupEnvelopes: %v %d", err, len(envs))
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.ReplaceEnvelopes(gid, 2, []Envelope{{GroupID: gid, KeyVersion: 2, UserID: "u1", WrappedDEK: "w2"}}, 2)
	})
	envs, _ = s.GetGroupEnvelopes(gid, 2)
	if len(envs) != 1 {
		t.Fatal("rekey 替换失败")
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.DeleteUserEnvelopes("u1") })
	envs, _ = s.GetUserEnvelopes("u1")
	if len(envs) != 0 {
		t.Fatal("DeleteUserEnvelopes 失败")
	}
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.UpsertEnvelope(env) })
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.DeleteGroupUserEnvelopes(gid, "u1") })
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.ReplaceEnvelopes(gid, 1, []Envelope{{GroupID: gid, KeyVersion: 1, UserID: "u1", WrappedDEK: "w"}}, 3)
	})
	_ = s.WithTx(context.Background(), func(tx Tx) error { return tx.DeleteOldKVEnvelopes(gid, 1) })
}

func TestQueryAuditAndListAllDevices(t *testing.T) {
	s := newTestStore(t)
	_ = s.WithTx(context.Background(), func(tx Tx) error {
		return tx.Audit(&AuditEvent{TS: 100, DeviceID: "d1", UserID: "u1", Action: "push", IP: "1.2.3.4"})
	})
	evs, err := s.QueryAudit(0, 0, "", "", 10)
	if err != nil || len(evs) != 1 || evs[0].Action != "push" {
		t.Fatalf("QueryAudit: %v %+v", err, evs)
	}
	evs, _ = s.QueryAudit(200, 0, "", "", 10)
	if len(evs) != 0 {
		t.Fatal("from 过滤失败")
	}
	devs, err := s.ListAllDevices()
	if err != nil || len(devs) != 0 {
		t.Fatalf("ListAllDevices: %v %d", err, len(devs))
	}
}

func TestGetUserCountClosedDB(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Migrate()
	_ = s.Close()
	if _, err := s.GetUserCount(); err == nil {
		t.Fatal("关闭后 GetUserCount 应报错")
	}
	if _, err := s.GetUserByID("x"); err == nil {
		t.Fatal("关闭后 GetUserByID 应报错")
	}
	if _, err := s.PullChanges(0, 10); err == nil {
		t.Fatal("关闭后 PullChanges 应报错")
	}
}
