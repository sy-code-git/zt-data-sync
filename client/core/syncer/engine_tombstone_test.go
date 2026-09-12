package syncer

import (
	"testing"

	"passbook/client/core/store"
	"passbook/client/core/vault"
	"passbook/internal/crypto"
	"passbook/internal/model"
	"passbook/internal/proto"
)

// 墓碑（Deleted）回归用例。
//
// 背景（实测 P0）：删除条目后紧接着同步，服务端会「把本端刚推上去的那一版又发回来」
// （推送成功后 last_seq 只在拉取时推进）。旧实现落库时构造 LocalEntry **不带 Deleted**
// （Go 零值 false），于是本地墓碑被合并/冲突流程抹掉：删除永远推不出去、服务端与他人
// 仍持有密文，界面还给出「已删除」的成功提示。
//
// 不变量：只要本地处于「已删除待推送」，任何拉取到的内容都不得覆盖该意图。

// tombstoneEnv 造好服务端视角的 DEK + 信封 + 密文条目，并返回可重复使用的拉取响应片段。
func tombstoneEnv(t *testing.T, e *Engine, v *vault.Vault, gid string) (*proto.SyncResponse, proto.Change) {
	t.Helper()
	dek, _ := v.NewDEK()
	_ = v.SetDEK(gid, 1, dek)
	crypto.Wipe(dek)
	wrapped := makeWrappedDEK(t, v, getDEKForTest(t, v, gid))
	ct, err := encEntry(v, gid, "e1", model.NewProject("proj"), 1)
	if err != nil {
		t.Fatal(err)
	}
	change := proto.Change{EntryID: "e1", GroupID: gid, Seq: 4, KeyVersion: 1, Ciphertext: ct}
	resp := &proto.SyncResponse{
		ServerSeq:    4,
		Changes:      []proto.Change{change},
		KeyEnvelopes: []proto.KeyEnvelopeInfo{{GroupID: gid, KeyVersion: 1, WrappedDEK: wrapped}},
		Groups:       []proto.GroupState{{ID: gid, Name: "G1", KeyVersion: 1}},
	}
	return resp, change
}

// markTombstone 按 Core.DeleteEntry 的落库语义写墓碑：空密文 + Deleted + Dirty。
func markTombstone(t *testing.T, e *Engine, gid string) {
	t.Helper()
	if err := e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: "e1", GroupID: gid, Seq: 4, KeyVersion: 1,
		Ciphertext: "", Dirty: true, Deleted: true,
	}); err != nil {
		t.Fatal(err)
	}
}

// TestTombstoneSurvivesPull：本地墓碑在「拉取到同条目的服务端版本」后必须原样保留。
func TestTombstoneSurvivesPull(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	pull, change := tombstoneEnv(t, e, v, gid)

	mc.pullResp = pull
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{}}
	if err := e.SyncNow(); err != nil {
		t.Fatalf("首次 SyncNow: %v", err)
	}
	if _, err := e.local.GetLocalEntry("e1"); err != nil {
		t.Fatalf("首次同步后本地应有该条目: %v", err)
	}

	// 用户删除 → 墓碑待推送
	markTombstone(t, e, gid)

	// 再收到同一条目的服务端版本（seq 与墓碑基准相同，即"自己刚推上去又拉回来"的情形）
	if err := e.applyOneChange(&change); err != nil {
		t.Fatalf("applyOneChange: %v", err)
	}

	le, err := e.local.GetLocalEntry("e1")
	if err != nil {
		t.Fatalf("墓碑应保留（等待推送），却被移除: %v", err)
	}
	if !le.Deleted {
		t.Fatal("拉取后墓碑被抹掉：删除意图丢失（P0 回归）")
	}
	if !le.Dirty {
		t.Fatal("墓碑应保持待推送（Dirty=true）")
	}
	if len(le.Ciphertext) != 0 {
		t.Fatalf("墓碑密文应为空，got %d 字节", len(le.Ciphertext))
	}
	if le.ConflictOf != "" {
		t.Fatalf("墓碑不应被标为冲突，got %q", le.ConflictOf)
	}
}

// TestTombstoneIsPushed：墓碑必须真的推给服务端（mutation 带 deleted=true + 空密文），
// 且推送成功后本地条目按设计移除（§7.2 4d）。
func TestTombstoneIsPushed(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	pull, change := tombstoneEnv(t, e, v, gid)

	mc.pullResp = pull
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{}}
	if err := e.SyncNow(); err != nil {
		t.Fatalf("首次 SyncNow: %v", err)
	}

	markTombstone(t, e, gid)

	// 下一轮：拉取仍会把该条目发回来（推送后 last_seq 只在拉取时推进，这是真实情形），
	// 同时服务端尚未删除 → 本端必须仍把 deleted=true 推上去。
	mc.pullResp = &proto.SyncResponse{
		ServerSeq:    5,
		Changes:      []proto.Change{change},
		KeyEnvelopes: pull.KeyEnvelopes,
		Groups:       pull.Groups,
	}
	mc.pushResp = &proto.PushResponse{
		Results: []proto.PushResult{{EntryID: "e1", OK: true, NewSeq: 5}},
	}
	if err := e.SyncNow(); err != nil {
		t.Fatalf("第二轮 SyncNow: %v", err)
	}

	var sent []proto.Mutation
	for _, batch := range mc.pushes {
		for _, m := range batch {
			if m.EntryID == "e1" {
				sent = append(sent, m)
			}
		}
	}
	if len(sent) == 0 {
		t.Fatal("墓碑未进入推送：删除只改了本地，服务端与他人仍持有密文（P0 回归）")
	}
	last := sent[len(sent)-1]
	if !last.Deleted {
		t.Fatalf("推送的变更未标 deleted=true（mutation=%+v）", last)
	}
	if len(last.Ciphertext) != 0 {
		t.Fatalf("墓碑推送应带空密文，got %d 字节", len(last.Ciphertext))
	}
	if _, err := e.local.GetLocalEntry("e1"); err == nil {
		t.Fatal("墓碑推送成功后本地条目应被移除（§7.2 4d）")
	}
}

// TestTombstoneKeptOnPushConflict：推送被服务端判为冲突（对端已改）时，
// 墓碑不得被改写成"带内容的冲突行"，应保持删除意图并把基准推进到服务端当前版本。
func TestTombstoneKeptOnPushConflict(t *testing.T) {
	mc := &mockClient{}
	e, v, _ := newTestEngine(t, mc)
	gid := "g1"
	pull, change := tombstoneEnv(t, e, v, gid)

	mc.pullResp = pull
	mc.pushResp = &proto.PushResponse{Results: []proto.PushResult{}}
	if err := e.SyncNow(); err != nil {
		t.Fatalf("首次 SyncNow: %v", err)
	}
	markTombstone(t, e, gid)

	// 服务端返回 40901（携带当前版本）
	if err := e.handlePushConflict("e1", &change); err != nil {
		t.Fatalf("handlePushConflict: %v", err)
	}

	le, err := e.local.GetLocalEntry("e1")
	if err != nil {
		t.Fatal(err)
	}
	if !le.Deleted || !le.Dirty {
		t.Fatalf("冲突后墓碑应保留待推送，got deleted=%v dirty=%v", le.Deleted, le.Dirty)
	}
	if len(le.Ciphertext) != 0 {
		t.Fatalf("墓碑不应携带密文，got %d 字节", len(le.Ciphertext))
	}
	if le.ConflictOf != "" {
		t.Fatalf("墓碑不应被标为冲突，got %q", le.ConflictOf)
	}
	if le.Seq != change.Seq {
		t.Fatalf("基准应推进到服务端当前版本 %d，got %d", change.Seq, le.Seq)
	}
}
