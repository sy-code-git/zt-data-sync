package sync

import (
	"context"
	"testing"
	"time"

	"passbook/server/store"
)

// fakeAuditRecorder 收集 RecordActor 调用（离线监视器测试用）。
type fakeAuditRecorder struct {
	calls []struct {
		deviceID, userID, action, detail string
	}
}

func (f *fakeAuditRecorder) RecordActor(_ context.Context, deviceID, userID, action, _, _, _, _, detail string) {
	f.calls = append(f.calls, struct {
		deviceID, userID, action, detail string
	}{deviceID, userID, action, detail})
}

// seedDevice 直插一条设备（绕过 authn，last_seen 可控）。先建宿主用户满足外键。
func seedDevice(t *testing.T, st store.Store, id, userID string, lastSeen int64, status string) {
	t.Helper()
	if err := st.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateUser(&store.User{ID: userID, Name: userID, Role: store.RoleMember, Status: store.StatusActive, CreatedAt: lastSeen})
	}); err != nil {
		t.Fatal(err)
	}
	if status == "" {
		status = store.DeviceActive
	}
	// token_hash 有唯一约束：用 device id 派生唯一假值（CreateDevice 落库 last_seen=NULL）
	if err := st.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateDevice(&store.Device{
			ID: id, UserID: userID, Name: id + "-name", Hostname: "H-" + id,
			TokenHash: "th-" + id, Status: status, CreatedAt: lastSeen, LastSeen: 0,
		})
	}); err != nil {
		t.Fatal(err)
	}
	// CreateDevice 不写 last_seen，用 UpdateDeviceSeen 补
	if err := st.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.UpdateDeviceSeen(id, "H-"+id, lastSeen)
	}); err != nil {
		t.Fatal(err)
	}
}

// TestOfflineMonitorTransition 离线监视器核心语义（§3.6/§5.2 device_offline）：
// 首趟建基线 → 在线设备超时后恰好记一次 offline → 重新活跃后再超时记第二次。
func TestOfflineMonitorTransition(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	// 用可变时间源：now 由测试推进
	var cur time.Time = now
	monitor := NewDeviceOfflineMonitor(st, nil, func() time.Time { return cur })

	// 设备 A：3 秒前活跃（在线）；设备 B：5 分钟前活跃（离线，从未被监视器见过）
	seedDevice(t, st, "devA", "u1", cur.Add(-3*time.Second).Unix(), "")
	seedDevice(t, st, "devB", "u2", cur.Add(-5*time.Minute).Unix(), "")

	// 第一趟：建立基线（A 在线、B 离线），不记任何 offline
	rec := &fakeAuditRecorder{}
	monitor.audit = rec
	n, err := monitor.SweepOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("首趟应只建基线不记离线, got %d", n)
	}

	// 时间推进 2 分钟：A 的 last_seen 已超阈值（90s）
	cur = now.Add(2 * time.Minute)
	n, err = monitor.SweepOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("A 应记一次离线, got %d", n)
	}
	if len(rec.calls) != 1 || rec.calls[0].deviceID != "devA" || rec.calls[0].action != "device_offline" {
		t.Fatalf("离线审计记录错误: %+v", rec.calls)
	}
	if rec.calls[0].userID != "u1" {
		t.Fatalf("离线审计 user = %q, want u1", rec.calls[0].userID)
	}

	// 再扫一趟：A 已不在标记内，不重复记
	n, err = monitor.SweepOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(rec.calls) != 1 {
		t.Fatalf("离线不应重复记录: n=%d calls=%d", n, len(rec.calls))
	}

	// A 重新活跃（心跳刷新 last_seen）→ 再超时 → 记第二次离线
	if err := st.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.UpdateDeviceSeen("devA", "H-devA", cur.Unix())
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := monitor.SweepOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	cur = cur.Add(2 * time.Minute)
	n, err = monitor.SweepOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || len(rec.calls) != 2 {
		t.Fatalf("重新上线后再离线应再记一次: n=%d calls=%d", n, len(rec.calls))
	}
}

// TestOfflineMonitorDisabledDevice 禁用设备不记离线（下线由 disable_device 审计承载）。
func TestOfflineMonitorDisabledDevice(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	var cur = time.Now()
	monitor := NewDeviceOfflineMonitor(st, nil, func() time.Time { return cur })
	seedDevice(t, st, "devD", "u1", cur.Add(-3*time.Second).Unix(), "")
	if err := st.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.DisableDevice("devD")
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := monitor.SweepOnce(context.Background()); err != nil { // 基线
		t.Fatal(err)
	}
	cur = cur.Add(2 * time.Minute)
	rec := &fakeAuditRecorder{}
	monitor.audit = rec
	n, err := monitor.SweepOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 || len(rec.calls) != 0 {
		t.Fatalf("禁用设备不应记离线: n=%d calls=%d", n, len(rec.calls))
	}
}

// TestAuditCleanerDays 构造回退：负数回默认 180，正数保留原值。
func TestAuditCleanerDays(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	if d := NewAuditCleaner(st, -1).Days(); d != auditRetentionDefault {
		t.Fatalf("负数应回退默认 180, got %d", d)
	}
	if d := NewAuditCleaner(st, 30).Days(); d != 30 {
		t.Fatalf("正数应保留, got %d", d)
	}
	if d := NewAuditCleaner(st, 0).Days(); d != 0 {
		t.Fatalf("0（永久）应透传由 main 决定不启动, got %d", d)
	}
}

// TestAuditCleanerCleanOnce 清理只删超期记录（端到端 store 校验）。
func TestAuditCleanerCleanOnce(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	oldTS := now.Add(-200 * 24 * time.Hour).Unix()
	freshTS := now.Add(-1 * time.Hour).Unix()
	for _, ts := range []int64{oldTS, oldTS + 1, freshTS} {
		if err := st.WithTx(context.Background(), func(tx store.Tx) error {
			return tx.Audit(&store.AuditEvent{TS: ts, DeviceID: "d", UserID: "u", Action: "push"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	c := NewAuditCleaner(st, 180)
	n, err := c.CleanOnce(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("清理 = %d, want 2（两条 200 天前）", n)
	}
	// 只剩 1 条期内记录
	events, err := st.QueryAudit(0, 0, "", "push", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].TS != freshTS {
		t.Fatalf("清理后残留错误: %+v", events)
	}
}
