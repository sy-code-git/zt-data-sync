package api

import (
	"net/http"
	"testing"
	"time"

	"passbook/internal/proto"
	"passbook/server/store"
)

// fetchAudit 拉取全量审计（admin）并按 action 分桶。
func fetchAudit(t *testing.T, f *fixture) map[string][]proto.AuditEventOut {
	t.Helper()
	resp := decodeBody[proto.AuditResponse](t, f.do(t, http.MethodGet, "/admin/audit", f.adminToken, nil))
	out := map[string][]proto.AuditEventOut{}
	for i := range resp.Events {
		e := &resp.Events[i]
		out[e.Action] = append(out[e.Action], *e)
	}
	return out
}

// TestAuditUnauthenticatedEntryPoints 回归（§5.2/P1）：无认证分组的三个身份入口
// （bootstrap / device_register / register_request）审计必须真实落库——
// 旧实现 Record 取不到认证上下文静默丢弃，「历史登录 IP/设备」在管理端永远为空。
func TestAuditUnauthenticatedEntryPoints(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t) // 首启引导（本测试验证其审计落库）
	f.createMember(t, "zhangsan")

	buckets := fetchAudit(t, f)

	// bootstrap 落库，且带 IP 与设备名快照
	if len(buckets["bootstrap"]) != 1 {
		t.Fatalf("bootstrap 审计 = %d 条, want 1（旧实现静默空转）", len(buckets["bootstrap"]))
	}
	b := buckets["bootstrap"][0]
	if b.IP != "10.0.0.1" {
		t.Fatalf("bootstrap 审计 IP = %q, want 10.0.0.1（fixture RemoteAddr）", b.IP)
	}
	if b.DeviceName != "admin-pc" {
		t.Fatalf("bootstrap 审计设备名 = %q, want admin-pc", b.DeviceName)
	}
	if b.UserID != f.adminUser {
		t.Fatalf("bootstrap 审计 user = %q, want %q", b.UserID, f.adminUser)
	}

	// device_register 落库（含 IP/hostname；user 非空）
	drs := buckets["device_register"]
	if len(drs) == 0 {
		t.Fatal("device_register 审计 0 条（旧实现静默空转）")
	}
	found := false
	for _, e := range drs {
		if e.DeviceName == "zhangsan-pc" {
			found = true
			if e.IP != "10.0.0.1" {
				t.Fatalf("device_register IP = %q, want 10.0.0.1", e.IP)
			}
			if e.Hostname != "WIN-zhangsan" {
				t.Fatalf("device_register hostname = %q, want WIN-zhangsan", e.Hostname)
			}
			if e.UserID == "" {
				t.Fatal("device_register user_id 为空（应解析工号对应 user）")
			}
		}
	}
	if !found {
		t.Fatalf("device_register 审计缺 zhangsan-pc: %+v", drs)
	}
}

// TestAuditDeviceOnlineEvents device_refresh 记 device_online；heartbeat 不记
// （心跳 30s 一次是状态信号，写审计会灌水挤出安全事件，§3.6 方案 A）。
func TestAuditDeviceOnlineEvents(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)

	// heartbeat → 不记 device_online（last_seen 更新由设备 tab/离线监视器承载）
	rec := f.do(t, http.MethodPost, "/auth/heartbeat", f.adminToken, &proto.HeartbeatRequest{Hostname: "WIN-A"})
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat = %d", rec.Code)
	}
	// refresh → device_refresh + device_online（旧 token 即刻作废，最后刷新保持有效 token）
	old := f.adminToken
	nr := decodeBody[proto.TokenRefreshResponse](t, f.do(t, http.MethodPost, "/auth/refresh", old, nil))
	f.adminToken = nr.Token

	buckets := fetchAudit(t, f)
	if len(buckets["device_refresh"]) != 1 {
		t.Fatalf("device_refresh = %d 条, want 1", len(buckets["device_refresh"]))
	}
	onl := buckets["device_online"]
	if len(onl) != 1 {
		t.Fatalf("device_online = %d 条, want 1（仅 refresh，heartbeat 不记）", len(onl))
	}
	// online 应带 IP 与用户
	for _, e := range onl {
		if e.IP != "10.0.0.1" {
			t.Fatalf("device_online IP = %q, want 10.0.0.1", e.IP)
		}
		if e.UserID != f.adminUser {
			t.Fatalf("device_online user = %q", e.UserID)
		}
	}
	// 心跳功能本身未废：last_seen 应已被更新（>0 且为最近时间）
	devs, err := f.st.ListAllDevices()
	if err != nil {
		t.Fatal(err)
	}
	var lastSeen int64
	for _, d := range devs {
		if d.UserID == f.adminUser {
			lastSeen = d.LastSeen
		}
	}
	if lastSeen <= 0 {
		t.Fatalf("heartbeat 后 last_seen 未更新: %d", lastSeen)
	}
}

// TestAuditRegisterRequestUnauthenticated 注册申请（无认证面）审计落库（§6.3 方案 C）。
func TestAuditRegisterRequestUnauthenticated(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// 生成免审核邀请码 → 提交申请 → 自动开户三处审计都应落库
	inv := decodeBody[proto.InviteResponse](t, f.do(t, http.MethodPost, "/admin/invites", f.adminToken, &proto.CreateInviteRequest{Username: "lisi", AutoApprove: true}))
	rr := f.do(t, http.MethodPost, "/auth/register-request", "", &proto.RegisterRequestRequest{
		InviteCode: inv.Invite.Code, Username: "lisi",
		SM2PublicKey: "pub-lisi", DeviceName: "lisi-pc",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("register-request = %d, body=%s", rr.Code, rr.Body.String())
	}

	buckets := fetchAudit(t, f)
	if len(buckets["register_request"]) != 1 {
		t.Fatalf("register_request 审计 = %d 条, want 1（旧实现静默空转）", len(buckets["register_request"]))
	}
	re := buckets["register_request"][0]
	if re.IP != "10.0.0.1" || re.DeviceName != "lisi-pc" {
		t.Fatalf("register_request 审计元数据不全: ip=%q device=%q", re.IP, re.DeviceName)
	}
	// 免审核自动开户：create_user 也应落库（actor=新用户）
	if len(buckets["create_user"]) == 0 {
		t.Fatal("免审核自动开户 create_user 审计 0 条")
	}
}

// TestAuditCleanerRetention 保留期清理（§12.2 PB_AUDIT_RETENTION）：
// 超期删除、期内保留、幂等。
func TestAuditCleanerRetention(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)

	ctx := t.Context()
	// 造一条 200 天前的旧审计（直接 store 写入，绕过 handler 时间）
	nowT := time.Now()
	if f.now != nil {
		nowT = f.now()
	}
	old := nowT.Add(-200 * 24 * time.Hour).Unix()
	err := f.st.WithTx(ctx, func(tx store.Tx) error {
		return tx.Audit(&store.AuditEvent{TS: old, DeviceID: "d-old", UserID: "u-old", Action: "push", IP: "1.1.1.1"})
	})
	if err != nil {
		t.Fatal(err)
	}

	// 清理（保留 180 天）→ 旧记录删除，新记录（bootstrap 等）保留
	var n int64
	err = f.st.WithTx(ctx, func(tx store.Tx) error {
		var e error
		n, e = tx.DeleteOldAudit(nowT.Add(-180 * 24 * time.Hour).Unix())
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("保留期清理删除 = %d, want 1", n)
	}
	buckets := fetchAudit(t, f)
	if len(buckets["push"]) != 0 {
		t.Fatalf("超期 push 审计未删除: %d 条", len(buckets["push"]))
	}
	if len(buckets["bootstrap"]) != 1 {
		t.Fatalf("期内 bootstrap 审计误删: %d 条", len(buckets["bootstrap"]))
	}

	// 幂等：再清一次为 0
	err = f.st.WithTx(ctx, func(tx store.Tx) error {
		var e error
		n, e = tx.DeleteOldAudit(nowT.Add(-180 * 24 * time.Hour).Unix())
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("二次清理 = %d, want 0（幂等）", n)
	}
}
