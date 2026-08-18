package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tjfoc/gmsm/sm2"

	"passbook/internal/crypto"
	"passbook/internal/proto"
	"passbook/server/authn"
	"passbook/server/middleware"
	"passbook/server/store"
	"passbook/server/sync"
)

// regSecret 测试注册凭证密钥（与 keytool 一致，§4.4）。
const regSecret = "test-reg-secret"

// fixture 集成测试夹具。
type fixture struct {
	router http.Handler
	st     store.Store
	// admin 凭据
	adminToken string
	adminUser  string
	// 密钥
	adminPriv *sm2.PrivateKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	hub := sync.NewHub()
	svc := New(&Options{
		Store: st, Authn: authn.New(st, authn.Options{BootstrapCode: "boot"}),
		Sync: sync.New(st, hub, nil), Hub: hub,
		Limiter:   middleware.NewRateLimiter(middleware.RateConfig{Auth: 1000, Sync: 100000, Heartbeat: 1000, Admin: 100000, MaxFail: 10, LockoutFor: 0}, nil),
		Audit:     middleware.NewAudit(st, nil),
		RegSecret: []byte(regSecret),
	})
	return &fixture{router: svc.Router(), st: st}
}

func (f *fixture) do(t *testing.T, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.RemoteAddr = "10.0.0.1:1000"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("解析响应 %q: %v", rec.Body.String(), err)
	}
	return v
}

func genPubB64(t *testing.T, priv *sm2.PrivateKey) string {
	t.Helper()
	der, err := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func attestation(name, pubKey string) string {
	tag := crypto.HMACSM3([]byte(regSecret), []byte("passbook-attestation-v1"+name+pubKey))
	return base64.StdEncoding.EncodeToString(tag)
}

// bootstrap 建立 admin。
func (f *fixture) bootstrap(t *testing.T) {
	t.Helper()
	priv, err := crypto.GenerateSM2Key()
	if err != nil {
		t.Fatal(err)
	}
	f.adminPriv = priv
	rec := f.do(t, http.MethodPost, "/auth/bootstrap", "", &proto.BootstrapRequest{
		BootstrapToken: "boot", Username: "admin01", Name: "admin01", DeviceName: "admin-pc",
		SM2PublicKey: genPubB64(t, priv),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("bootstrap = %d, body=%s", rec.Code, rec.Body.String())
	}
	resp := decodeBody[proto.BootstrapResponse](t, rec)
	f.adminToken = resp.Token
	f.adminUser = resp.UserID
}

// createMember 建一个成员用户并注册设备。
func (f *fixture) createMember(t *testing.T, name string) (userID, token string, priv *sm2.PrivateKey) {
	t.Helper()
	priv, err := crypto.GenerateSM2Key()
	if err != nil {
		t.Fatal(err)
	}
	pub := genPubB64(t, priv)
	rec := f.do(t, http.MethodPost, "/admin/users", f.adminToken, &proto.CreateUserRequest{
		Username: name, Name: name, SM2PublicKey: pub, Attestation: attestation(name, pub),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("create user = %d, body=%s", rec.Code, rec.Body.String())
	}
	userID = decodeBody[proto.CreateUserResponse](t, rec).UserID

	// 注册设备（按工号 username）
	ch := decodeBody[proto.DeviceChallengeResponse](t, f.do(t, http.MethodPost, "/auth/device-challenge", "", &proto.DeviceChallengeRequest{Username: name}))
	sig, _ := crypto.SM2SignChallenge(priv, []byte(ch.Challenge))
	reg := decodeBody[proto.DeviceRegisterResponse](t, f.do(t, http.MethodPost, "/auth/device", "", &proto.DeviceRegisterRequest{
		Username: name, DeviceName: name + "-pc", Hostname: "WIN-" + name,
		Challenge: ch.Challenge, Signature: base64.StdEncoding.EncodeToString(sig),
	}))
	return userID, reg.Token, priv
}

// createGroup 建组并加入用户。
func (f *fixture) createGroup(t *testing.T, name string, userIDs ...string) string {
	t.Helper()
	g := decodeBody[proto.GroupCreateResponse](t, f.do(t, http.MethodPost, "/admin/groups", f.adminToken, &proto.GroupCreateRequest{Name: name}))
	for _, uid := range userIDs {
		rec := f.do(t, http.MethodPut, "/admin/groups/"+g.GroupID+"/members", f.adminToken, &proto.MemberAddRequest{UserID: uid})
		if rec.Code != http.StatusOK {
			t.Fatalf("add member = %d, body=%s", rec.Code, rec.Body.String())
		}
	}
	return g.GroupID
}

// push 推送一条变更。
func (f *fixture) push(t *testing.T, token, entryID, groupID string, baseSeq int64, kv int, ct string) *proto.PushResult {
	t.Helper()
	rec := f.do(t, http.MethodPost, "/sync/push", token, &proto.PushRequest{Mutations: []proto.Mutation{
		{EntryID: entryID, GroupID: groupID, BaseSeq: baseSeq, KeyVersion: kv, Ciphertext: ct},
	}})
	if rec.Code != http.StatusOK {
		t.Fatalf("push = %d, body=%s", rec.Code, rec.Body.String())
	}
	results := decodeBody[proto.PushResponse](t, rec).Results
	if len(results) != 1 {
		t.Fatalf("results = %d", len(results))
	}
	return &results[0]
}

func (f *fixture) sync(t *testing.T, token string, since int64) proto.SyncResponse {
	t.Helper()
	rec := f.do(t, http.MethodGet, fmt.Sprintf("/sync?since=%d", since), token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("sync = %d, body=%s", rec.Code, rec.Body.String())
	}
	return decodeBody[proto.SyncResponse](t, rec)
}

// ---- 测试 ----

func TestFullFlowBootstrapToSync(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)

	// 建成员 + 建组 + 加入
	memID, memToken, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	// 冷启动：sync 应报 missing_envelopes 包含 admin 与成员（无信封）
	s := f.sync(t, f.adminToken, 0)
	if len(s.Groups) != 1 || s.Groups[0].ID != gid {
		t.Fatalf("groups = %+v", s.Groups)
	}
	if s.Groups[0].KeyVersion != 1 {
		t.Fatalf("kv = %d, want 1", s.Groups[0].KeyVersion)
	}
	// 冷启动：无信封 → missing = 全部 active 成员
	missing := map[string]bool{}
	for _, uid := range s.Groups[0].MissingEnvelopes {
		missing[uid] = true
	}
	if !missing[f.adminUser] || !missing[memID] {
		t.Fatalf("冷启动 missing_envelopes 应含全部成员: %v", s.Groups[0].MissingEnvelopes)
	}

	// 成员 push 条目
	ct := `{"v":1,"alg":"SM4-GCM","kv":1,"nonce":"AA==","ct":"BB==","hmac":"CC=="}`
	res := f.push(t, memToken, "e1", gid, 0, 1, ct)
	if !res.OK {
		t.Fatalf("push 失败: %+v", res)
	}

	// admin pull 看到条目
	s2 := f.sync(t, f.adminToken, 0)
	found := false
	for _, c := range s2.Changes {
		if c.EntryID == "e1" && c.GroupID == gid {
			found = true
		}
	}
	if !found {
		t.Fatalf("pull 未看到条目: %+v", s2.Changes)
	}
	// server_seq 推进
	if s2.ServerSeq <= 0 {
		t.Fatalf("server_seq = %d", s2.ServerSeq)
	}
}

func TestPushConflictOldSeq(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	ct := `{"v":1}`
	res1 := f.push(t, memToken, "e1", gid, 0, 1, ct)
	if !res1.OK {
		t.Fatalf("首次 push: %+v", res1)
	}
	// 用旧 base_seq 再 push → 40901 携带当前版
	res2 := f.push(t, memToken, "e1", gid, 0, 1, ct)
	if res2.OK || res2.Error != proto.ErrConflict {
		t.Fatalf("旧 base_seq 应 40901: %+v", res2)
	}
	if res2.Current == nil || res2.Current.Seq != res1.NewSeq {
		t.Fatalf("40901 应携带当前版: %+v", res2.Current)
	}
	// 用正确 base_seq push → 成功
	res3 := f.push(t, memToken, "e1", gid, res1.NewSeq, 1, ct)
	if !res3.OK {
		t.Fatalf("正确 base_seq push: %+v", res3)
	}
}

func TestPushStaleKV(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	res := f.push(t, memToken, "e1", gid, 0, 5, `{"v":1}`) // kv=5 != 组 kv=1
	if res.OK || res.Error != proto.ErrKeyVersionStale {
		t.Fatalf("旧 kv 应 40902: %+v", res)
	}
}

func TestPushNotMember(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// 两个成员，只有 u1 在组
	u1, t1, _ := f.createMember(t, "u1")
	_, t2, _ := f.createMember(t, "u2")
	gid := f.createGroup(t, "G1", f.adminUser, u1)

	res := f.push(t, t2, "e1", gid, 0, 1, `{"v":1}`)
	if res.OK || res.Error != proto.ErrNotMember {
		t.Fatalf("非成员 push 应 40302: %+v", res)
	}
	_ = t1
}

func TestPushTooLarge(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	big := strings.Repeat("x", 256*1024+1)
	res := f.push(t, memToken, "e1", gid, 0, 1, big)
	if res.OK || res.Error != proto.ErrTooLarge {
		t.Fatalf("超限应 41301: %+v", res)
	}
}

func TestAdminOnly(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	_, memToken, _ := f.createMember(t, "zhangsan")

	rec := f.do(t, http.MethodGet, "/admin/groups", memToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非 admin 访问 admin 应 403: %d", rec.Code)
	}
}

func TestRevokeFlow(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	f.createGroup(t, "G1", f.adminUser, memID)

	// 吊销（成员名二次确认）
	rec := f.do(t, http.MethodPost, "/admin/users/"+memID+"/revoke", f.adminToken, &proto.RevokeRequest{ConfirmName: "zhangsan"})
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d, body=%s", rec.Code, rec.Body.String())
	}
	// 被吊销者 sync → 断权（401 token 失效 或 403 用户吊销，二者均符合 §6.3 断权语义）
	srec := f.do(t, http.MethodGet, "/sync?since=0", memToken, nil)
	if srec.Code != http.StatusUnauthorized && srec.Code != http.StatusForbidden {
		t.Fatalf("吊销后 sync 应断权(401/403): %d", srec.Code)
	}
	// 组 pending_rekey 置位
	g := decodeBody[proto.GroupsResponse](t, f.do(t, http.MethodGet, "/admin/groups", f.adminToken, nil))
	if !g.Groups[0].PendingRekey {
		t.Fatal("revoke 后组应 pending_rekey")
	}
}

func TestArchiveBlocksPush(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	// 归档（组名二次确认）
	rec := f.do(t, http.MethodPost, "/admin/groups/"+gid+"/archive", f.adminToken, &proto.ArchiveRequest{ConfirmName: "G1"})
	if rec.Code != http.StatusOK {
		t.Fatalf("archive = %d", rec.Code)
	}
	// push → 40905
	res := f.push(t, memToken, "e1", gid, 0, 1, `{"v":1}`)
	if res.OK || res.Error != proto.ErrGroupArchived {
		t.Fatalf("归档组 push 应 40905: %+v", res)
	}
	// 信封上传 → 40905
	urec := f.do(t, http.MethodPost, "/groups/"+gid+"/keys", memToken, &proto.KeysUploadRequest{KeyVersion: 1, Envelopes: []proto.EnvelopeUpload{}})
	var errBody proto.ErrorBody
	_ = json.Unmarshal(urec.Body.Bytes(), &errBody)
	if errBody.Code != proto.ErrGroupArchived {
		t.Fatalf("归档组 keys 应 40905: %+v", errBody)
	}
	// 重启 → 恢复
	rec2 := f.do(t, http.MethodPost, "/admin/groups/"+gid+"/unarchive", f.adminToken, nil)
	if rec2.Code != http.StatusOK {
		t.Fatalf("unarchive = %d", rec2.Code)
	}
	res2 := f.push(t, memToken, "e1", gid, 0, 1, `{"v":1}`)
	if !res2.OK {
		t.Fatalf("重启后 push 应成功: %+v", res2)
	}
}

func TestAttestationRejected(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// 伪造 attestation（错误 secret 计算）
	priv, _ := crypto.GenerateSM2Key()
	pub := genPubB64(t, priv)
	badAtt := base64.StdEncoding.EncodeToString(crypto.HMACSM3([]byte("wrong-secret"), []byte("passbook-attestation-v1zhangsan"+pub)))
	rec := f.do(t, http.MethodPost, "/admin/users", f.adminToken, &proto.CreateUserRequest{
		Name: "zhangsan", SM2PublicKey: pub, Attestation: badAtt,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("伪造 attestation 应 40001: %d, body=%s", rec.Code, rec.Body.String())
	}
}

func TestKeyfileReset(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, _, _ := f.createMember(t, "zhangsan")
	f.createGroup(t, "G1", f.adminUser, memID)

	// keyfile-reset：换新公钥
	newPriv, _ := crypto.GenerateSM2Key()
	newPub := genPubB64(t, newPriv)
	rec := f.do(t, http.MethodPost, "/admin/users/"+memID+"/keyfile-reset", f.adminToken, &proto.KeyfileResetRequest{
		SM2PublicKey: newPub, Attestation: attestation("zhangsan", newPub),
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("keyfile-reset = %d, body=%s", rec.Code, rec.Body.String())
	}
	// 组 pending_rekey 置位（数据重加密挂起）
	g := decodeBody[proto.GroupsResponse](t, f.do(t, http.MethodGet, "/admin/groups", f.adminToken, nil))
	if !g.Groups[0].PendingRekey {
		t.Fatal("keyfile-reset 后组应 pending_rekey")
	}
}

func TestEnvelopesUpload(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	// 冷启动入伙追加：为全部 active 成员包裹 kv=1 信封
	rec := f.do(t, http.MethodPost, "/groups/"+gid+"/keys", memToken, &proto.KeysUploadRequest{
		KeyVersion: 1,
		Envelopes: []proto.EnvelopeUpload{
			{UserID: f.adminUser, WrappedDEK: `{"v":1,"alg":"SM2-C1C3C2","data":"aGk="}`},
			{UserID: memID, WrappedDEK: `{"v":1,"alg":"SM2-C1C3C2","data":"aGk="}`},
		},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("信封上传 = %d, body=%s", rec.Code, rec.Body.String())
	}
	// 覆盖已有信封 → 40904
	rec2 := f.do(t, http.MethodPost, "/groups/"+gid+"/keys", memToken, &proto.KeysUploadRequest{
		KeyVersion: 1,
		Envelopes: []proto.EnvelopeUpload{
			{UserID: f.adminUser, WrappedDEK: `{"v":1,"alg":"SM2-C1C3C2","data":"bmV3"}`},
		},
	})
	var errBody proto.ErrorBody
	_ = json.Unmarshal(rec2.Body.Bytes(), &errBody)
	if errBody.Code != proto.ErrBadEnvelopes {
		t.Fatalf("覆盖信封应 40904: %+v", errBody)
	}
	// sync 后 missing_envelopes 应为空
	s := f.sync(t, memToken, 0)
	for _, gs := range s.Groups {
		if gs.ID == gid && len(gs.MissingEnvelopes) != 0 {
			t.Fatalf("信封齐全后 missing 应为空: %v", gs.MissingEnvelopes)
		}
	}
}

func TestRekeyTriggerAndCommit(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, memToken, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	// 管理端触发 rekey → pending_rekey 置位
	rec := f.do(t, http.MethodPost, "/admin/groups/"+gid+"/rekey", f.adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rekey = %d", rec.Code)
	}
	// 成员在线 auto-rekey：提交 kv=2 信封集合（全部 active 成员）→ 升 kv
	urec := f.do(t, http.MethodPost, "/groups/"+gid+"/keys", memToken, &proto.KeysUploadRequest{
		KeyVersion: 2,
		Envelopes: []proto.EnvelopeUpload{
			{UserID: f.adminUser, WrappedDEK: `{"v":1,"alg":"SM2-C1C3C2","data":"bg=="}`},
			{UserID: memID, WrappedDEK: `{"v":1,"alg":"SM2-C1C3C2","data":"bg=="}`},
		},
	})
	if urec.Code != http.StatusOK {
		t.Fatalf("rekey 信封 = %d, body=%s", urec.Code, urec.Body.String())
	}
	// kv 应升为 2；无条目 → 全部到达新 kv → pending_rekey 清除
	g := decodeBody[proto.GroupsResponse](t, f.do(t, http.MethodGet, "/admin/groups", f.adminToken, nil))
	if g.Groups[0].KeyVersion != 2 {
		t.Fatalf("kv = %d, want 2", g.Groups[0].KeyVersion)
	}
	if g.Groups[0].PendingRekey {
		t.Fatal("无条目 rekey 后应清 pending_rekey")
	}
	// rekey 集合不完整 → 40904
	urec2 := f.do(t, http.MethodPost, "/groups/"+gid+"/keys", memToken, &proto.KeysUploadRequest{
		KeyVersion: 3,
		Envelopes: []proto.EnvelopeUpload{
			{UserID: memID, WrappedDEK: `{"v":1,"alg":"SM2-C1C3C2","data":"bg=="}`},
		},
	})
	var errBody proto.ErrorBody
	_ = json.Unmarshal(urec2.Body.Bytes(), &errBody)
	if errBody.Code != proto.ErrBadEnvelopes {
		t.Fatalf("rekey 集合不完整应 40904: %+v", errBody)
	}
}

func TestGroupMembersAndDevices(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	memID, _, _ := f.createMember(t, "zhangsan")
	gid := f.createGroup(t, "G1", f.adminUser, memID)

	// 组成员清单
	m := decodeBody[proto.GroupMembersResponse](t, f.do(t, http.MethodGet, "/admin/groups/"+gid+"/members", f.adminToken, nil))
	if len(m.Members) != 2 {
		t.Fatalf("成员数 = %d, want 2", len(m.Members))
	}
	// 设备列表
	d := decodeBody[proto.DevicesResponse](t, f.do(t, http.MethodGet, "/admin/devices", f.adminToken, nil))
	if len(d.Devices) < 2 {
		t.Fatalf("设备数 = %d", len(d.Devices))
	}
	// 禁用设备（设备名确认）
	if d.Devices[0].DeviceID == "" {
		t.Fatal("设备 id 为空")
	}
	drec := f.do(t, http.MethodPost, "/admin/devices/"+d.Devices[0].DeviceID+"/disable", f.adminToken, &proto.DisableDeviceRequest{ConfirmName: d.Devices[0].Name})
	if drec.Code != http.StatusOK {
		t.Fatalf("disable = %d, body=%s", drec.Code, drec.Body.String())
	}
	_ = memID
}

func TestRateLimitHTTP429(t *testing.T) {
	// 独立构造低阈值限流器
	st, _ := store.Open(":memory:")
	defer st.Close()
	_ = st.Migrate()
	hub := sync.NewHub()
	svc := New(&Options{
		Store: st, Authn: authn.New(st, authn.Options{BootstrapCode: "boot"}),
		Sync: sync.New(st, hub, nil), Hub: hub,
		Limiter:   middleware.NewRateLimiter(middleware.RateConfig{Auth: 2, Sync: 100, Heartbeat: 100, Admin: 100, MaxFail: 10, LockoutFor: 0}, nil),
		Audit:     middleware.NewAudit(st, nil),
		RegSecret: []byte(regSecret),
	})
	router := svc.Router()

	// bootstrap 限流 2/min/IP：3 次调用第 3 次 429
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{}`))
		req.RemoteAddr = "9.9.9.9:1"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
	}
	req := httptest.NewRequest(http.MethodPost, "/auth/bootstrap", strings.NewReader(`{}`))
	req.RemoteAddr = "9.9.9.9:1"
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("超频应 429, got %d", rec.Code)
	}
}

func TestRefreshToken(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// refresh 用当前 token → 新 token
	rec := f.do(t, http.MethodPost, "/auth/refresh", f.adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh = %d", rec.Code)
	}
	newTok := decodeBody[proto.TokenRefreshResponse](t, rec).Token
	if newTok == "" || newTok == f.adminToken {
		t.Fatal("refresh 应返回新 token")
	}
	// 旧 token 即刻作废
	oldRec := f.do(t, http.MethodGet, "/sync?since=0", f.adminToken, nil)
	if oldRec.Code != http.StatusUnauthorized {
		t.Fatalf("旧 token 应 401: %d", oldRec.Code)
	}
}

func TestHeartbeat(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	rec := f.do(t, http.MethodPost, "/auth/heartbeat", f.adminToken, &proto.HeartbeatRequest{Hostname: "WIN-NEW"})
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat = %d", rec.Code)
	}
}

// genKey/pubOf 错误分支测试辅助。
func genKey(t *testing.T) (*sm2.PrivateKey, error) {
	t.Helper()
	return crypto.GenerateSM2Key()
}

func pubOf(t *testing.T, priv *sm2.PrivateKey) string {
	t.Helper()
	return genPubB64(t, priv)
}
