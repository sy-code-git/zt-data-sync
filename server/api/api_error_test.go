package api

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"passbook/internal/crypto"
	"passbook/internal/proto"
)

// api 错误分支集中测试（提升 handler 覆盖率至 ≥80%）。

func TestBadJSONRequests(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)

	cases := []struct {
		method string
		path   string
		token  string
		body   string
	}{
		{http.MethodPost, "/auth/bootstrap", "", "not-json"},
		{http.MethodPost, "/auth/device-challenge", "", "not-json"},
		{http.MethodPost, "/auth/device", "", "not-json"},
		{http.MethodPost, "/sync/push", f.adminToken, "not-json"},
		{http.MethodPost, "/admin/users", f.adminToken, "not-json"},
		{http.MethodPost, "/admin/groups", f.adminToken, "not-json"},
		{http.MethodPost, "/admin/users/x/revoke", f.adminToken, "not-json"},
	}
	for _, c := range cases {
		rec := rawDo(f.router, c.method, c.path, c.token, c.body)
		var eb proto.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &eb); err != nil {
			t.Fatalf("%s %s 响应非错误体: %s", c.method, c.path, rec.Body.String())
		}
		if eb.Code != proto.ErrBadRequest {
			t.Fatalf("%s %s 坏 JSON 应 40001, got %+v", c.method, c.path, eb)
		}
	}
}

// P2 回归：authn 接口（bootstrap/challenge/device）的业务错误码不得被 handleErr
// 误映射为 50001。修复前 handleErr 只用 sync.CodeOf，识别不了 authn.Error。
func TestAuthErrorCodeMapping(t *testing.T) {
	f := newFixture(t)
	// bootstrap 坏 token → 40105（而非 50001）
	priv, _ := crypto.GenerateSM2Key()
	rec := f.do(t, http.MethodPost, "/auth/bootstrap", "", &proto.BootstrapRequest{
		BootstrapToken: "wrong", Name: "x", DeviceName: "d", SM2PublicKey: genPubB64(t, priv),
	})
	if eb := decodeBody[proto.ErrorBody](t, rec); eb.Code != proto.ErrBadBootstrap {
		t.Fatalf("bootstrap 坏 token 应 40105, got %d", eb.Code)
	}
	// device-challenge 用户不存在 → 40001（而非 50001）
	rec = f.do(t, http.MethodPost, "/auth/device-challenge", "", &proto.DeviceChallengeRequest{Username: "nope"})
	if eb := decodeBody[proto.ErrorBody](t, rec); eb.Code != proto.ErrBadRequest {
		t.Fatalf("challenge 用户不存在应 40001, got %d", eb.Code)
	}
}

func TestAdminConfirmMismatch(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, _, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", memID)

	// revoke 确认名不匹配
	rec := f.do(t, http.MethodPost, "/admin/users/"+memID+"/revoke", f.adminToken, &proto.RevokeRequest{ConfirmName: "wrong"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("revoke 确认名不匹配应 400: %d", rec.Code)
	}
	// archive 确认组名不匹配
	rec2 := f.do(t, http.MethodPost, "/admin/groups/"+gid+"/archive", f.adminToken, &proto.ArchiveRequest{ConfirmName: "wrong"})
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("archive 确认名不匹配应 400: %d", rec2.Code)
	}
	// disable 设备确认名不匹配
	devs := decodeBody[proto.DevicesResponse](t, f.do(t, http.MethodGet, "/admin/devices", f.adminToken, nil))
	rec3 := f.do(t, http.MethodPost, "/admin/devices/"+devs.Devices[0].DeviceID+"/disable", f.adminToken, &proto.DisableDeviceRequest{ConfirmName: "wrong"})
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("disable 确认名不匹配应 400: %d", rec3.Code)
	}
}

func TestAdminResourceNotFound(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)

	// revoke 不存在用户
	rec := f.do(t, http.MethodPost, "/admin/users/nope/revoke", f.adminToken, &proto.RevokeRequest{ConfirmName: "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("revoke 不存在用户应 400: %d", rec.Code)
	}
	// rekey 不存在组
	rec2 := f.do(t, http.MethodPost, "/admin/groups/nope/rekey", f.adminToken, nil)
	if rec2.Code != http.StatusBadRequest {
		t.Fatalf("rekey 不存在组应 400: %d", rec2.Code)
	}
	// archive 不存在组
	rec3 := f.do(t, http.MethodPost, "/admin/groups/nope/archive", f.adminToken, &proto.ArchiveRequest{ConfirmName: "x"})
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("archive 不存在组应 400: %d", rec3.Code)
	}
	// keyfile-reset 不存在用户
	rec4 := f.do(t, http.MethodPost, "/admin/users/nope/keyfile-reset", f.adminToken, &proto.KeyfileResetRequest{SM2PublicKey: "x", Attestation: "y"})
	if rec4.Code != http.StatusBadRequest {
		t.Fatalf("keyfile-reset 不存在用户应 400: %d", rec4.Code)
	}
}

func TestSyncBadParams(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// since 非法
	rec := f.do(t, http.MethodGet, "/sync?since=abc", f.adminToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("since 非法应 400: %d", rec.Code)
	}
	// 指定组但非成员 → 40302
	rec2 := f.do(t, http.MethodGet, "/sync?since=0&group_id=no-group", f.adminToken, nil)
	if rec2.Code != http.StatusForbidden {
		t.Fatalf("非成员指定组应 403: %d", rec2.Code)
	}
}

func TestCreateUserValidation(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// 字段缺失
	rec := f.do(t, http.MethodPost, "/admin/users", f.adminToken, &proto.CreateUserRequest{Name: ""})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("字段缺失应 400: %d", rec.Code)
	}
	// 重复公钥
	priv, _ := genKey(t)
	pub := pubOf(t, priv)
	att := attestation("dup", pub)
	rec2 := f.do(t, http.MethodPost, "/admin/users", f.adminToken, &proto.CreateUserRequest{Username: "dup", Name: "dup", SM2PublicKey: pub, Attestation: att})
	if rec2.Code != http.StatusOK {
		t.Fatalf("首建应成功: %d %s", rec2.Code, rec2.Body.String())
	}
	rec3 := f.do(t, http.MethodPost, "/admin/users", f.adminToken, &proto.CreateUserRequest{Username: "dup2", Name: "dup2", SM2PublicKey: pub, Attestation: attestation("dup2", pub)})
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("重复公钥应 400: %d %s", rec3.Code, rec3.Body.String())
	}
}

func TestKeyfileResetRevokedUser(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, _, _ := f.createMember(t, "zhangsan")
	// 吊销
	_ = f.do(t, http.MethodPost, "/admin/users/"+memID+"/revoke", f.adminToken, &proto.RevokeRequest{ConfirmName: "zhangsan"})
	// keyfile-reset 已吊销用户 → 400
	rec := f.do(t, http.MethodPost, "/admin/users/"+memID+"/keyfile-reset", f.adminToken, &proto.KeyfileResetRequest{SM2PublicKey: "x", Attestation: "y"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("已吊销用户 keyfile-reset 应 400: %d", rec.Code)
	}
}

// ---- helpers ----

// rawDo 发送原始 body（坏 JSON 测试用）。
func rawDo(router http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "10.0.0.1:1000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// ---- 0% handler 补充 ----

func TestKeysMineAndListUsers(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	f.createGroup(t, "G1", memID)

	// GET /keys/mine
	rec := f.do(t, http.MethodGet, "/keys/mine", memToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("keys/mine = %d", rec.Code)
	}
	var km struct {
		Envelopes []proto.KeyEnvelopeInfo `json:"envelopes"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &km)
	if len(km.Envelopes) != 0 {
		t.Fatalf("初始信封应为空: %+v", km.Envelopes)
	}
	// GET /users
	rec2 := f.do(t, http.MethodGet, "/users", memToken, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("users = %d", rec2.Code)
	}
	var us proto.UsersResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &us)
	if len(us.Users) != 2 { // admin + member
		t.Fatalf("users 数 = %d, want 2", len(us.Users))
	}
}

func TestAdminRemoveMemberAndAudit(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, _, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", memID)

	// DELETE members/:uid
	rec := f.do(t, http.MethodDelete, "/admin/groups/"+gid+"/members/"+memID, f.adminToken, &proto.MemberRemoveRequest{ConfirmName: "zhangsan"})
	if rec.Code != http.StatusOK {
		t.Fatalf("remove member = %d, body=%s", rec.Code, rec.Body.String())
	}
	var mr proto.MemberRemoveResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &mr)
	if !mr.Removed {
		t.Fatal("remove 应返回 removed=true")
	}
	// GET /admin/audit（含 from/to 解析）
	rec2 := f.do(t, http.MethodGet, "/admin/audit?from=2026-01-01T00:00:00Z&to=2027-01-01T00:00:00Z", f.adminToken, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("audit = %d, body=%s", rec2.Code, rec2.Body.String())
	}
	var ar proto.AuditResponse
	_ = json.Unmarshal(rec2.Body.Bytes(), &ar)
	// 应有 create_user/add_member/remove_member 等审计
	if len(ar.Events) == 0 {
		t.Fatal("审计应非空")
	}
	// 坏时间参数 → 400（A5：不再静默空结果）
	rec3 := f.do(t, http.MethodGet, "/admin/audit?from=bad-time", f.adminToken, nil)
	if rec3.Code != http.StatusBadRequest {
		t.Fatalf("audit bad from 应 400, got %d", rec3.Code)
	}
}

func TestParseKeyVersions(t *testing.T) {
	m := parseKeyVersions("g1:1,g2:2, bad, g3:abc")
	if m["g1"] != 1 || m["g2"] != 2 {
		t.Fatalf("parse = %v", m)
	}
	if _, ok := m["bad"]; ok {
		t.Fatal("bad 不应解析")
	}
	if _, ok := m["g3"]; ok {
		t.Fatal("g3:abc 不应解析")
	}
	if len(parseKeyVersions("")) != 0 {
		t.Fatal("空串应返回空 map")
	}
}

func TestStreamSlotLimits(t *testing.T) {
	s := &Server{slots: &streamSlots{count: map[string]int{}}}
	// 上限 4：前 4 次成功，第 5 次拒绝
	for i := 0; i < maxStreamPerToken; i++ {
		if !s.acquireStreamSlot("dev1") {
			t.Fatalf("第 %d 次应成功", i+1)
		}
	}
	if s.acquireStreamSlot("dev1") {
		t.Fatal("第 5 次应拒绝")
	}
	// 释放后恢复
	s.releaseStreamSlot("dev1")
	if !s.acquireStreamSlot("dev1") {
		t.Fatal("释放后应可再获取")
	}
	// 释放到 0 时清 key
	for i := 0; i < maxStreamPerToken; i++ {
		s.releaseStreamSlot("dev1")
	}
	s.slots.mu.Lock()
	_, ok := s.slots.count["dev1"]
	s.slots.mu.Unlock()
	if ok {
		t.Fatal("全部释放后 key 应删除")
	}
}

func TestStreamSSE(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// 用真实 HTTP server（SSE 长连接走网络流，避免测试并发读写 ResponseRecorder 的 data race）
	ts := httptest.NewServer(f.router)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/sync/stream", nil)
	req.Header.Set("Authorization", "Bearer "+f.adminToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE 请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE 应 200: %d", resp.StatusCode)
	}
	// 读初始注释（: connected）
	br := bufio.NewReader(resp.Body)
	deadline := time.After(3 * time.Second)
	lineCh := make(chan string, 1)
	go func() {
		l, _ := br.ReadString('\n')
		lineCh <- l
	}()
	select {
	case line := <-lineCh:
		if !strings.Contains(line, ": connected") {
			t.Fatalf("SSE 初始注释未写出: %q", line)
		}
	case <-deadline:
		t.Fatal("SSE 初始注释未写出（超时）")
	}
	// 关闭 body → handler 退出（SSE 随连接断开返回）
	_ = resp.Body.Close()
}

func TestAdminAddMemberValidation(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, _, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1")

	// 不存在用户
	rec := f.do(t, http.MethodPut, "/admin/groups/"+gid+"/members", f.adminToken, &proto.MemberAddRequest{UserID: "nope"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("不存在用户应 400: %d", rec.Code)
	}
	// 正常加入
	rec2 := f.do(t, http.MethodPut, "/admin/groups/"+gid+"/members", f.adminToken, &proto.MemberAddRequest{UserID: memID})
	if rec2.Code != http.StatusOK {
		t.Fatalf("加入应成功: %d %s", rec2.Code, rec2.Body.String())
	}
	// 幂等：已是成员 → 200
	rec3 := f.do(t, http.MethodPut, "/admin/groups/"+gid+"/members", f.adminToken, &proto.MemberAddRequest{UserID: memID})
	if rec3.Code != http.StatusOK {
		t.Fatalf("幂等加入应 200: %d", rec3.Code)
	}
	// 已吊销用户加入
	_ = f.do(t, http.MethodPost, "/admin/users/"+memID+"/revoke", f.adminToken, &proto.RevokeRequest{ConfirmName: "zhangsan"})
	rec4 := f.do(t, http.MethodPut, "/admin/groups/"+gid+"/members", f.adminToken, &proto.MemberAddRequest{UserID: memID})
	if rec4.Code != http.StatusBadRequest {
		t.Fatalf("已吊销用户加入应 400: %d", rec4.Code)
	}
}

// §8.3：SSE 断开后 2s 内不允许重连（防抖动风暴）。
func TestStreamReconnectGuard(t *testing.T) {
	s := &Server{reconnect: &streamReconnect{lastEnd: map[string]time.Time{}}}
	if !s.allowStreamReconnect("dev1") {
		t.Fatal("首次连接应允许")
	}
	s.markStreamDisconnect("dev1")
	if s.allowStreamReconnect("dev1") {
		t.Fatal("断开后立即重连应被拒（2s 内）")
	}
	// 2s 后可重连
	s.reconnect.mu.Lock()
	s.reconnect.lastEnd["dev1"] = time.Now().Add(-3 * time.Second)
	s.reconnect.mu.Unlock()
	if !s.allowStreamReconnect("dev1") {
		t.Fatal("超过 2s 应允许重连")
	}
}
