package sync

import (
	"context"
	"fmt"
	"passbook/internal/proto"
	"sync"
	"testing"
	"time"

	"passbook/server/store"
)

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(); err != nil {
		t.Fatal(err)
	}
	return s
}

func setupGroup(t *testing.T, s store.Store, userIDs ...string) (string, []string) {
	t.Helper()
	gid := "g-" + randID()
	if err := s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateGroup(&store.Group{ID: gid, Name: "G1", KeyVersion: 1, CreatedAt: 1})
	}); err != nil {
		t.Fatal(err)
	}
	for i, uid := range userIDs {
		if err := s.WithTx(context.Background(), func(tx store.Tx) error {
			if err := tx.CreateUser(&store.User{ID: uid, Name: "u" + randID(), SM2PublicKey: "p" + randID(),
				Attestation: "a", Role: store.RoleMember, Status: store.StatusActive, CreatedAt: int64(i)}); err != nil {
				return err
			}
			return tx.AddGroupMember(&store.GroupMember{GroupID: gid, UserID: uid, CreatedAt: 1})
		}); err != nil {
			t.Fatal(err)
		}
	}
	return gid, userIDs
}

func randID() string {
	b := make([]byte, 8)
	_, _ = time.Now().UnixNano(), b
	for i := range b {
		b[i] = byte('a' + time.Now().UnixNano()%26)
	}
	return string(b)
}

func TestPullColdStartMissingEnvelopes(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, users := setupGroup(t, s, "u1", "u2")

	resp, err := svc.Pull(context.Background(), "u1", 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 冷启动：无信封 → missing = 全部 active 成员
	found := false
	for _, g := range resp.Groups {
		if g.ID == gid {
			found = true
			if len(g.MissingEnvelopes) != len(users) {
				t.Fatalf("冷启动 missing = %v, want 全部成员 %v", g.MissingEnvelopes, users)
			}
			if len(g.ActiveMembers) != len(users) {
				t.Fatalf("冷启动 active_members = %v, want 全部成员 %v", g.ActiveMembers, users)
			}
		}
	}
	if !found {
		t.Fatal("未返回组状态")
	}
}

func TestPullArchivedNoMissing(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, _ := setupGroup(t, s, "u1")
	// 归档组
	_ = s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SetGroupArchived(gid, store.GroupArchived, 100)
	})
	resp, err := svc.Pull(context.Background(), "u1", 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range resp.Groups {
		if g.ID == gid {
			if !g.Archived {
				t.Fatal("应标记 archived")
			}
			if len(g.MissingEnvelopes) != 0 {
				t.Fatalf("归档组不应产生 missing_envelopes: %v", g.MissingEnvelopes)
			}
			if len(g.ActiveMembers) != 0 {
				t.Fatalf("归档组 active_members 应为空: %v", g.ActiveMembers)
			}
		}
	}
}

func TestPullGroupIDNotMember(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, _ := setupGroup(t, s, "u1")
	// u2 非成员指定组拉取 → 40302
	_, err := svc.Pull(context.Background(), "u2", 0, gid, nil)
	if CodeOf(err) != 40302 {
		t.Fatalf("非成员指定组拉取应 40302, got %v", err)
	}
}

func TestHubNotifyAndDisconnect(t *testing.T) {
	h := NewHub()
	ch1, cancel1 := h.Subscribe("u1")
	ch2, cancel2 := h.Subscribe("u1")
	defer cancel1()
	defer cancel2()

	// 通知：两个订阅都应收到（缓冲 1）
	h.Notify([]string{"g1"})
	select {
	case <-ch1:
	default:
		t.Fatal("ch1 未收到通知")
	}
	select {
	case <-ch2:
	default:
		t.Fatal("ch2 未收到通知")
	}

	// 断权联动：close 所有订阅（先 drain 通知，避免读到旧 token）
	h.DisconnectUser("u1")
	for _, ch := range []<-chan struct{}{ch1, ch2} {
		_, open := <-ch // 先读（可能读到已缓冲的通知）
		for open {
			_, open = <-ch
		}
		if open {
			t.Fatal("channel 应被关闭")
		}
	}
	// 关闭后 Notify 不得 panic（P2 竞态回归：send-on-closed）
	h.Notify([]string{"g1"})
}

func TestTombstoneCleanOnce(t *testing.T) {
	s := newTestStore(t)
	c := NewTombstoneCleaner(s, 90)
	// 直接验证 CleanOnce 可执行（无墓碑时幂等成功）
	if err := c.CleanOnce(context.Background(), time.Now()); err != nil {
		t.Fatalf("CleanOnce: %v", err)
	}
}

func TestTombstoneCleanerDefaults(t *testing.T) {
	c := NewTombstoneCleaner(newTestStore(t), 0) // days<=0 → 默认 90
	if c.days != TombstoneCleanDays {
		t.Fatalf("days = %d, want %d", c.days, TombstoneCleanDays)
	}
}

func TestTombstoneRunContextCancel(t *testing.T) {
	s := newTestStore(t)
	c := NewTombstoneCleaner(s, 90)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx, 3)
		close(done)
	}()
	cancel() // 立即取消
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run 未随 ctx 取消退出")
	}
}

func envUpload(userIDs ...string) *proto.KeysUploadRequest {
	req := &proto.KeysUploadRequest{KeyVersion: 1}
	for _, uid := range userIDs {
		req.Envelopes = append(req.Envelopes, proto.EnvelopeUpload{
			UserID: uid, WrappedDEK: `{"v":1,"alg":"SM2-C1C3C2","data":"aGk="}`,
		})
	}
	return req
}

func TestUploadKeysColdStartAppend(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, users := setupGroup(t, s, "u1", "u2")

	// 冷启动入伙追加：为全部 active 成员上传 kv=1 信封
	if err := svc.UploadKeys(context.Background(), "u1", gid, envUpload(users...)); err != nil {
		t.Fatalf("冷启动追加: %v", err)
	}
	// 覆盖已有 → 40904
	err := svc.UploadKeys(context.Background(), "u1", gid, envUpload("u1"))
	if CodeOf(err) != proto.ErrBadEnvelopes {
		t.Fatalf("覆盖信封应 40904, got %v", err)
	}
	// 非成员 → 40302
	if err := svc.UploadKeys(context.Background(), "nobody", gid, envUpload("u1")); CodeOf(err) != proto.ErrNotMember {
		t.Fatalf("非成员应 40302, got %v", err)
	}
	// 非 active 成员（伪造 user）→ 40904
	if err := svc.UploadKeys(context.Background(), "u1", gid, envUpload("ghost")); CodeOf(err) != proto.ErrBadEnvelopes {
		t.Fatalf("非 active 成员应 40904, got %v", err)
	}
	// kv 不合法（非当前/当前+1）→ 40904
	bad := envUpload(users...)
	bad.KeyVersion = 5
	if err := svc.UploadKeys(context.Background(), "u1", gid, bad); CodeOf(err) != proto.ErrBadEnvelopes {
		t.Fatalf("非法 kv 应 40904, got %v", err)
	}
}

func TestUploadKeysRekey(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, users := setupGroup(t, s, "u1", "u2")

	// 冷启动 kv=1
	if err := svc.UploadKeys(context.Background(), "u1", gid, envUpload(users...)); err != nil {
		t.Fatal(err)
	}
	// rekey kv=2（集合完整）
	req := envUpload(users...)
	req.KeyVersion = 2
	if err := svc.UploadKeys(context.Background(), "u1", gid, req); err != nil {
		t.Fatalf("rekey: %v", err)
	}
	g, _ := s.GetGroup(gid)
	if g.KeyVersion != 2 {
		t.Fatalf("kv = %d, want 2", g.KeyVersion)
	}
	if g.PendingRekey != store.RekeyDone {
		t.Fatal("无条目 rekey 后应清 pending_rekey")
	}
	// rekey 集合不完整 → 40904
	bad := envUpload("u1")
	bad.KeyVersion = 3
	if err := svc.UploadKeys(context.Background(), "u1", gid, bad); CodeOf(err) != proto.ErrBadEnvelopes {
		t.Fatalf("rekey 集合不完整应 40904, got %v", err)
	}
}

func TestUploadKeysArchived(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, users := setupGroup(t, s, "u1")
	_ = s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SetGroupArchived(gid, store.GroupArchived, 1)
	})
	if err := svc.UploadKeys(context.Background(), "u1", gid, envUpload(users...)); CodeOf(err) != proto.ErrGroupArchived {
		t.Fatalf("归档组信封提交应 40905, got %v", err)
	}
}

func TestPushDirect(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, users := setupGroup(t, s, "u1")
	// 建真实设备（entries.updated_by 外键，§5.2）
	_ = s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateDevice(&store.Device{ID: "dev1", UserID: "u1", Name: "pc", TokenHash: "h", Status: store.DeviceActive, CreatedAt: 1})
	})

	// push 新条目
	resp, err := svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e1", GroupID: gid, BaseSeq: 0, KeyVersion: 1, Ciphertext: `{"v":1}`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !resp.Results[0].OK {
		t.Fatalf("push 失败: %+v", resp.Results[0])
	}
	_ = users

	// 旧 base_seq → 40901
	resp2, _ := svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e1", GroupID: gid, BaseSeq: 0, KeyVersion: 1, Ciphertext: `{"v":1}`}},
	})
	if resp2.Results[0].Error != proto.ErrConflict {
		t.Fatalf("旧 base_seq 应 40901: %+v", resp2.Results[0])
	}
	if resp2.Results[0].Current == nil {
		t.Fatal("40901 应携带 current")
	}
	// 旧 kv → 40902
	resp3, _ := svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e2", GroupID: gid, BaseSeq: 0, KeyVersion: 9, Ciphertext: `{"v":1}`}},
	})
	if resp3.Results[0].Error != proto.ErrKeyVersionStale {
		t.Fatalf("旧 kv 应 40902: %+v", resp3.Results[0])
	}
	// 非成员 → 40302
	resp4, _ := svc.Push(context.Background(), "nobody", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e3", GroupID: gid, BaseSeq: 0, KeyVersion: 1, Ciphertext: `{"v":1}`}},
	})
	if resp4.Results[0].Error != proto.ErrNotMember {
		t.Fatalf("非成员应 40302: %+v", resp4.Results[0])
	}
	// 超限 → 41301
	big := make([]byte, 256*1024+1)
	resp5, _ := svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e4", GroupID: gid, BaseSeq: 0, KeyVersion: 1, Ciphertext: string(big)}},
	})
	if resp5.Results[0].Error != proto.ErrTooLarge {
		t.Fatalf("超限应 41301: %+v", resp5.Results[0])
	}
	// 不存在的组 → 40302（非成员）
	resp6, _ := svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e5", GroupID: "no-gid", BaseSeq: 0, KeyVersion: 1, Ciphertext: `{"v":1}`}},
	})
	if resp6.Results[0].Error != proto.ErrNotMember {
		t.Fatalf("不存在组应 40302: %+v", resp6.Results[0])
	}
}

// rekey 未收敛：组内有旧 kv 条目 → pending_rekey 保留（§6.3 收尾判定排除墓碑）。
func TestRekeyPendingUntilEntriesCaughtUp(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, users := setupGroup(t, s, "u1")
	// 建真实设备（push 外键）
	_ = s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateDevice(&store.Device{ID: "dev1", UserID: "u1", Name: "pc", TokenHash: "h", Status: store.DeviceActive, CreatedAt: 1})
	})
	// 冷启动 kv=1 信封 + push 一条 kv=1 条目
	if err := svc.UploadKeys(context.Background(), "u1", gid, envUpload(users...)); err != nil {
		t.Fatal(err)
	}
	resp, _ := svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e1", GroupID: gid, BaseSeq: 0, KeyVersion: 1, Ciphertext: `{"v":1}`}},
	})
	if !resp.Results[0].OK {
		t.Fatalf("push: %+v", resp.Results[0])
	}

	// admin 触发 rekey（置位 pending_rekey，§6.3）
	_ = s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SetGroupRekey(gid, store.RekeyPending)
	})
	// 成员 auto-rekey kv=2：有旧条目未到达 → pending_rekey 保留、kv 升
	req := envUpload(users...)
	req.KeyVersion = 2
	if err := svc.UploadKeys(context.Background(), "u1", gid, req); err != nil {
		t.Fatal(err)
	}
	g, _ := s.GetGroup(gid)
	if g.KeyVersion != 2 {
		t.Fatalf("kv = %d, want 2", g.KeyVersion)
	}
	if g.PendingRekey != store.RekeyPending {
		t.Fatal("有旧条目时 pending_rekey 应保留")
	}

	// 用 kv=2 重加密条目 push → 全部到达新 kv → 下一次 rekey 提交清除 pending_rekey
	resp2, _ := svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e1", GroupID: gid, BaseSeq: resp.Results[0].NewSeq, KeyVersion: 2, Ciphertext: `{"v":2}`}},
	})
	if !resp2.Results[0].OK {
		t.Fatalf("kv2 push: %+v", resp2.Results[0])
	}
	// 再次提交 kv=3 全集（模拟继续 rekey）→ 全部条目已到新 kv → 清除
	req3 := envUpload(users...)
	req3.KeyVersion = 3
	if err := svc.UploadKeys(context.Background(), "u1", gid, req3); err != nil {
		t.Fatal(err)
	}
	g3, _ := s.GetGroup(gid)
	if g3.PendingRekey != store.RekeyDone {
		t.Fatal("条目全部到达后应清 pending_rekey")
	}
}

func TestErrCodeError(t *testing.T) {
	e := &ErrCode{Code: 40901, Message: "conflict"}
	if e.Error() != "conflict" {
		t.Fatalf("Error() = %q", e.Error())
	}
	if CodeOf(errTest) != proto.ErrInternal {
		t.Fatalf("非 ErrCode 应 50001, got %d", CodeOf(errTest))
	}
}

type errT struct{}

func (e *errT) Error() string { return "t" }

var errTest = &errT{}

func TestSubscribeCancelTwice(t *testing.T) {
	h := NewHub()
	ch, cancel := h.Subscribe("u1")
	_ = ch
	cancel()
	cancel() // 二次 cancel 应幂等（once 分支）
	// 取消后用户订阅清空
	h.mu.Lock()
	_, ok := h.byUser["u1"]
	h.mu.Unlock()
	if ok {
		t.Fatal("取消后订阅应清空")
	}
}

func TestPullChangesAndEnvelopes(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, users := setupGroup(t, s, "u1")
	_ = s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateDevice(&store.Device{ID: "dev1", UserID: "u1", Name: "pc", TokenHash: "h", Status: store.DeviceActive, CreatedAt: 1})
	})

	// push 条目 + 上传信封
	_, _ = svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e1", GroupID: gid, BaseSeq: 0, KeyVersion: 1, Ciphertext: `{"v":1}`}},
	})
	_ = svc.UploadKeys(context.Background(), "u1", gid, envUpload(users...))

	// Pull：看到 changes + 自己的信封
	resp, err := svc.Pull(context.Background(), "u1", 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Changes) != 1 || resp.Changes[0].EntryID != "e1" {
		t.Fatalf("changes = %+v", resp.Changes)
	}
	if len(resp.KeyEnvelopes) != 1 {
		t.Fatalf("envelopes = %+v", resp.KeyEnvelopes)
	}
	// keyVersions 声明已持有 kv=1 信封 → 不再返回
	resp2, _ := svc.Pull(context.Background(), "u1", 0, "", map[string]int{gid: 1})
	if len(resp2.KeyEnvelopes) != 0 {
		t.Fatalf("声明已持有信封后仍返回: %+v", resp2.KeyEnvelopes)
	}
	// 指定组拉取
	resp3, err := svc.Pull(context.Background(), "u1", 0, gid, nil)
	if err != nil || len(resp3.Changes) != 1 {
		t.Fatalf("指定组拉取: err=%v changes=%+v", err, resp3.Changes)
	}
}

// A1：since 负数 clamp 为 0（等价全量，防御异常参数）。
func TestPullNegativeSince(t *testing.T) {
	s := newTestStore(t)
	svc := New(s, NewHub(), nil)
	gid, _ := setupGroup(t, s, "u1")
	_ = s.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateDevice(&store.Device{ID: "dev1", UserID: "u1", Name: "pc", TokenHash: "h", Status: store.DeviceActive, CreatedAt: 1})
	})
	_, _ = svc.Push(context.Background(), "u1", "dev1", &proto.PushRequest{
		Mutations: []proto.Mutation{{EntryID: "e1", GroupID: gid, BaseSeq: 0, KeyVersion: 1, Ciphertext: `{"v":1}`}},
	})
	neg, err := svc.Pull(context.Background(), "u1", -100, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	zero, err := svc.Pull(context.Background(), "u1", 0, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(neg.Changes) != len(zero.Changes) || len(neg.Changes) == 0 {
		t.Fatalf("负数 since 应等价 0 且非空: neg=%d zero=%d", len(neg.Changes), len(zero.Changes))
	}
}

// 并发压力：多 goroutine Subscribe/Notify/Disconnect 同时跑——验证无 panic、无死锁。
// 建议 CI 用 `go test -race` 复跑（本机无 gcc）。
func TestHubConcurrent(t *testing.T) {
	h := NewHub()
	var wg sync.WaitGroup
	// 20 个订阅者
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			ch, cancel := h.Subscribe(u)
			for j := 0; j < 50; j++ {
				_ = ch
				h.Notify([]string{"g1"})
			}
			cancel()
		}(fmt.Sprintf("u%d", i))
	}
	// 并发 Notify
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			h.Notify([]string{"g1", "g2"})
		}
	}()
	// 并发 Disconnect（覆盖 cancel 的 close 路径）
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			h.DisconnectUser(fmt.Sprintf("u%d", i%20))
		}
	}()
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("并发 Hub 操作超时（疑似死锁）")
	}
}
