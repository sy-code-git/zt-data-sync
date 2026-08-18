package middleware

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"passbook/internal/crypto"
	"passbook/internal/proto"
	"passbook/server/authn"
	"passbook/server/store"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

// ---- 安全头 ----

func TestSecurityHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	SecurityHeaders(okHandler()).ServeHTTP(rec, req)

	for _, h := range []string{"Strict-Transport-Security", "X-Content-Type-Options", "Cache-Control", "Referrer-Policy"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("缺少安全头 %s", h)
		}
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", rec.Header().Get("X-Content-Type-Options"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", rec.Header().Get("Cache-Control"))
	}
}

// ---- 限流 ----

func TestFixedWindowLimiter(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cur := now
	l := newFixedWindowLimiter(3, time.Minute, func() time.Time { return cur })

	for i := 0; i < 3; i++ {
		if !l.Allow("k") {
			t.Fatalf("第 %d 次应放行", i+1)
		}
	}
	if l.Allow("k") {
		t.Fatal("超限应拒绝")
	}
	// 下一窗口恢复
	cur = cur.Add(time.Minute)
	if !l.Allow("k") {
		t.Fatal("新窗口应放行")
	}
}

func TestLockoutTracker(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cur := now
	lt := newLockoutTracker(3, 10*time.Minute, func() time.Time { return cur })

	if lt.IsLocked("1.2.3.4") {
		t.Fatal("初始不应锁定")
	}
	lt.RecordFailure("1.2.3.4")
	lt.RecordFailure("1.2.3.4")
	if lt.IsLocked("1.2.3.4") {
		t.Fatal("未达阈值不应锁定")
	}
	lt.RecordFailure("1.2.3.4")
	if !lt.IsLocked("1.2.3.4") {
		t.Fatal("达阈值应锁定")
	}
	// 锁定期间 Reset 不生效？Reset 应清计数但锁定中……按设计 Reset 直接删除状态
	lt.Reset("1.2.3.4")
	if lt.IsLocked("1.2.3.4") {
		t.Fatal("Reset 后不应锁定")
	}
	// 锁定到期自动解锁
	lt.RecordFailure("5.6.7.8")
	lt.RecordFailure("5.6.7.8")
	lt.RecordFailure("5.6.7.8")
	if !lt.IsLocked("5.6.7.8") {
		t.Fatal("应锁定")
	}
	cur = cur.Add(10*time.Minute + time.Second)
	if lt.IsLocked("5.6.7.8") {
		t.Fatal("锁定到期应解锁")
	}
}

func TestClientIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.1.20:54321"
	if got := ClientIP(req); got != "10.0.1.20" {
		t.Fatalf("ClientIP = %q, want 10.0.1.20", got)
	}
}

// ---- 认证中间件 ----

func newAuthFixture(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	svc := authn.New(st, authn.Options{BootstrapCode: "boot"})
	priv, err := crypto.GenerateSM2Key()
	if err != nil {
		t.Fatal(err)
	}
	der, err := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := svc.Bootstrap(context.Background(), &proto.BootstrapRequest{
		BootstrapToken: "boot",
		Name:           "admin01",
		DeviceName:     "admin-pc",
		SM2PublicKey:   base64.StdEncoding.EncodeToString(der),
	})
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	_ = resp
	return st
}

func TestRequireAuthNoToken(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	st := newAuthFixture(t)

	h := RequireAuth(st, authn.HashToken)(okHandler())
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无 token 应 401, got %d", rec.Code)
	}
}

// TestRequireAuth 需要完整 bootstrap+注册流程，在 authn 测试已覆盖；
// 此处验证中间件对无效 token 拒绝。
func TestRequireAuthInvalidToken(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	st := newAuthFixture(t)

	h := RequireAuth(st, authn.HashToken)(okHandler())
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("无效 token 应 401, got %d", rec.Code)
	}
}

// ---- HTTP 层认证/授权/限流验收 ----

// setupAuthChain 完整注册一个设备并返回有效 token（供中间件 HTTP 层测试）。
func setupAuthChain(t *testing.T) (store.Store, string, *authn.Service) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	svc := authn.New(st, authn.Options{BootstrapCode: "boot"})
	priv, err := crypto.GenerateSM2Key()
	if err != nil {
		t.Fatal(err)
	}
	der, _ := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	bootResp, err := svc.Bootstrap(context.Background(), &proto.BootstrapRequest{
		BootstrapToken: "boot", Username: "admin01", Name: "admin01", DeviceName: "admin-pc",
		SM2PublicKey: base64.StdEncoding.EncodeToString(der),
	})
	if err != nil {
		t.Fatal(err)
	}
	ch, _ := svc.CreateChallenge(context.Background(), bootResp.Username)
	sig, _ := crypto.SM2SignChallenge(priv, []byte(ch.Challenge))
	regResp, err := svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username: bootResp.Username, DeviceName: "mbp", Hostname: "WIN-X",
		Challenge: ch.Challenge, Signature: base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		t.Fatal(err)
	}
	return st, regResp.Token, svc
}

func TestRequireAuthValidToken(t *testing.T) {
	st, token, _ := setupAuthChain(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.0.1.5:1234"

	var gotRole string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, ok := WithAuth(r.Context())
		if !ok {
			t.Error("context 无认证信息")
		}
		gotRole = ac.User.Role
		w.WriteHeader(http.StatusOK)
	})
	RequireAuth(st, authn.HashToken)(next).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("有效 token 应 200, got %d", rec.Code)
	}
	if gotRole != store.RoleAdmin {
		t.Fatalf("role = %q, want admin", gotRole)
	}
	// 中间件更新 last_ip
	ac, _ := WithAuth(req.Context()) // 注意：req 的 ctx 被包装，需从 ServeHTTP 内验证
	_ = ac
	// 直接验证库中 last_ip 已更新
	// （通过再发一次请求内验证不可行，改查设备表）
	dev, _ := st.GetDeviceByTokenHash(authn.HashToken(token))
	if dev.LastIP != "10.0.1.5" {
		t.Fatalf("last_ip = %q, want 10.0.1.5", dev.LastIP)
	}
}

func TestRequireAdmin(t *testing.T) {
	st, token, _ := setupAuthChain(t)
	// admin 通过
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	RequireAuth(st, authn.HashToken)(RequireAdmin(okHandler())).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin 应通过, got %d", rec.Code)
	}
}

func TestRequireAuthRevokedUser(t *testing.T) {
	st, token, _ := setupAuthChain(t)
	// 先查 token 对应 user，再吊销（模拟 revoke 的 status 变更）
	dev, err := st.GetDeviceByTokenHash(authn.HashToken(token))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SetUserRevoked(dev.UserID, time.Now().Unix())
	}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	RequireAuth(st, authn.HashToken)(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("吊销用户应 403, got %d", rec.Code)
	}
}

// ---- 补充覆盖：审计 / admin / 限流构造 ----

func TestAuditRecord(t *testing.T) {
	st, token, _ := setupAuthChain(t)
	aud := NewAudit(st, nil)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.RemoteAddr = "10.0.1.5:1234"

	// 通过 RequireAuth 注入 context 后调用 Record
	h := RequireAuth(st, authn.HashToken)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aud.Record(r, "push", "e1", `{"result":"ok"}`)
		w.WriteHeader(200)
	}))
	h.ServeHTTP(rec, req)
	// 无失败即通过；审计写入成功验证：无 panic
}

func TestAuditRecordNoAuth(t *testing.T) {
	st, _, _ := setupAuthChain(t)
	aud := NewAudit(st, nil)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	// 无认证 context → Record 静默跳过（不 panic）
	aud.Record(req, "push", "", "")
}

func TestRequireAdminUnauthenticated(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	RequireAdmin(okHandler()).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("未认证应 401, got %d", rec.Code)
	}
}

func TestRequireAdminNonAdmin(t *testing.T) {
	// 构造 member 用户的 AuthContext 注入 context
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), ctxKeyDevice, &AuthContext{
		Device: &store.Device{ID: "d", UserID: "u", Status: store.DeviceActive},
		User:   &store.User{ID: "u", Role: store.RoleMember, Status: store.StatusActive},
	})
	RequireAdmin(okHandler()).ServeHTTP(rec, req.WithContext(ctx))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("member 应 403, got %d", rec.Code)
	}
}

func TestIsErrCode(t *testing.T) {
	if !IsErrCode(authn.NewError(40101, "x"), 40101) {
		t.Fatal("应匹配 40101")
	}
	if IsErrCode(authn.NewError(40101, "x"), 40301) {
		t.Fatal("不应匹配 40301")
	}
	if IsErrCode(&errTest2{}, 40101) {
		t.Fatal("非 authn.Error 不应匹配")
	}
}

type errTest2 struct{}

func (e *errTest2) Error() string { return "t" }

func TestNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(RateConfig{Auth: 5, Sync: 120, Heartbeat: 30, Admin: 30, MaxFail: 10, LockoutFor: 10 * time.Minute}, nil)
	if rl.auth == nil || rl.sync == nil || rl.heart == nil || rl.admin == nil || rl.lockout == nil {
		t.Fatal("限流器构造不完整")
	}
	// nil 限流器放行（未配置）
	var nilLimiter *fixedWindowLimiter
	if !nilLimiter.Allow("k") {
		t.Fatal("nil limiter 应放行")
	}
	// 惰性清理分支：构造大窗口历史数据
	now := time.Unix(1700000000, 0)
	cur := now
	l := newFixedWindowLimiter(1, time.Minute, func() time.Time { return cur })
	l.counts["old1"] = &windowEntry{start: cur.Add(-2 * time.Minute).UnixMilli(), count: 1}
	l.counts["old2"] = &windowEntry{start: cur.Add(-2 * time.Minute).UnixMilli(), count: 1}
	l.counts["old3"] = &windowEntry{start: cur.Add(-2 * time.Minute).UnixMilli(), count: 1}
	for i := 0; i < 10001; i++ {
		l.counts[fmt.Sprintf("filler-%d", i)] = &windowEntry{start: cur.UnixMilli(), count: 1}
	}
	if !l.Allow("new") {
		t.Fatal("触发惰性清理后应放行")
	}
	if _, ok := l.counts["old1"]; ok {
		t.Fatal("惰性清理应删除过期条目")
	}
}

// HTTP 层限流：超频返回 42901（§8.3 验收）。
func TestRateLimitHTTP(t *testing.T) {
	rl := NewRateLimiter(RateConfig{Auth: 2, Sync: 120, Heartbeat: 30, Admin: 30, MaxFail: 10, LockoutFor: 10 * time.Minute}, nil)
	h := rl.Middleware(LimitAuth, ClientIP)(okHandler())

	// 2 次放行，第 3 次 429
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "1.2.3.4:1000"
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("第 %d 次应放行, got %d", i+1, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:1000"
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超频应 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Fatal("应带 Retry-After 头")
	}
	// 不同 IP 不受影响
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.RemoteAddr = "5.6.7.8:1000"
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("不同 IP 应放行, got %d", rec2.Code)
	}
}

func TestRateLimitNilMiddleware(t *testing.T) {
	// nil RateLimiter → 中间件直接放行
	var rl *RateLimiter
	h := rl.Middleware(LimitAuth, ClientIP)(okHandler())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("nil limiter 应放行, got %d", rec.Code)
	}
}

func TestTimeout(t *testing.T) {
	// 慢 handler 应被超时打断（ctx 取消）
	h := Timeout(50 * time.Millisecond)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			w.WriteHeader(http.StatusGatewayTimeout) // handler 感知超时
		case <-time.After(500 * time.Millisecond):
			w.WriteHeader(http.StatusOK)
		}
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("慢 handler 应被超时, got %d", rec.Code)
	}
}

// §9.3：CORS 白名单——允许来源返回 ACAO 头，未配置/非白名单不放宽。
func TestCORS(t *testing.T) {
	allowed := ParseOrigins("https://admin.example.com, https://ui.example.com")
	// 白名单来源：返回 ACAO + 预检 204
	h := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/", nil)
	req.Header.Set("Origin", "https://admin.example.com")
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("预检应 204, got %d", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "https://admin.example.com" {
		t.Fatal("应返回 ACAO 头")
	}
	// 实际请求也带 ACAO
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/", nil)
	req2.Header.Set("Origin", "https://ui.example.com")
	h.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK || rec2.Header().Get("Access-Control-Allow-Origin") != "https://ui.example.com" {
		t.Fatalf("实际请求: code=%d ACAO=%q", rec2.Code, rec2.Header().Get("Access-Control-Allow-Origin"))
	}
	// 非白名单来源：无 CORS 头（浏览器拦截）
	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodGet, "/", nil)
	req3.Header.Set("Origin", "https://evil.example.com")
	h.ServeHTTP(rec3, req3)
	if rec3.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("非白名单不应返回 ACAO（防 CSRF）")
	}
	// 空白名单 = 同源模式：任何 Origin 都不放行
	empty := CORS(map[string]struct{}{})(okHandler())
	rec4 := httptest.NewRecorder()
	req4 := httptest.NewRequest(http.MethodGet, "/", nil)
	req4.Header.Set("Origin", "https://x.example.com")
	empty.ServeHTTP(rec4, req4)
	if rec4.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("空白名单不应放行")
	}
}

func TestParseOrigins(t *testing.T) {
	m := ParseOrigins("a.com, b.com, ")
	if len(m) != 2 {
		t.Fatalf("origins = %v", m)
	}
	if len(ParseOrigins("")) != 0 {
		t.Fatal("空串应返回空集合")
	}
}
