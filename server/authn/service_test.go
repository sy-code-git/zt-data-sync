package authn

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"
	"time"

	"github.com/tjfoc/gmsm/sm2"

	"passbook/internal/crypto"
	"passbook/internal/proto"
	"passbook/server/store"
)

type fixture struct {
	svc  *Service
	st   store.Store
	priv *sm2.PrivateKey
	now  time.Time
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
	now := time.Unix(1700000000, 0)
	svc := New(st, Options{BootstrapCode: "boot-token-123", Now: func() time.Time { return now }})
	priv, err := crypto.GenerateSM2Key()
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{svc: svc, st: st, priv: priv, now: now}
}

func (f *fixture) pubKeyB64(t *testing.T) string {
	t.Helper()
	der, err := crypto.MarshalSM2PublicKey(&f.priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(der)
}

func (f *fixture) bootstrap(t *testing.T) *proto.BootstrapResponse {
	t.Helper()
	resp, err := f.svc.Bootstrap(context.Background(), &proto.BootstrapRequest{
		BootstrapToken: "boot-token-123",
		Username:       "admin01",
		Name:           "admin01",
		DeviceName:     "admin-pc",
		SM2PublicKey:   f.pubKeyB64(t),
	})
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return resp
}

func (f *fixture) registerDevice(t *testing.T, username string) (*proto.DeviceRegisterResponse, string) {
	t.Helper()
	ch, err := f.svc.CreateChallenge(context.Background(), username)
	if err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}
	sig, err := crypto.SM2SignChallenge(f.priv, []byte(ch.Challenge))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username:   username,
		DeviceName: "mbp",
		Hostname:   "WIN-ABC",
		Challenge:  ch.Challenge,
		Signature:  base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	return resp, ch.Challenge
}

// ---- Bootstrap ----

func TestBootstrapSuccess(t *testing.T) {
	f := newFixture(t)
	resp := f.bootstrap(t)
	if resp.Role != store.RoleAdmin {
		t.Fatalf("role = %q, want admin", resp.Role)
	}
	if resp.Token == "" || resp.UserID == "" || resp.DeviceID == "" {
		t.Fatal("bootstrap 响应字段缺失")
	}
	if resp.ExpiresIn != TokenTTL {
		t.Fatalf("ExpiresIn = %d, want %d", resp.ExpiresIn, TokenTTL)
	}
	// 库中不应有 token 明文
	dev, err := f.st.GetDeviceByID(resp.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if dev.TokenHash == resp.Token {
		t.Fatal("库中不应存 token 明文")
	}
	if dev.TokenHash != HashToken(resp.Token) {
		t.Fatal("token_hash 应等于 SM3(token)")
	}
	// 用户为 admin
	u, _ := f.st.GetUserByID(resp.UserID)
	if u.Role != store.RoleAdmin || u.Status != store.StatusActive {
		t.Fatal("bootstrap 用户应为 active admin")
	}
}

func TestBootstrapTokenReuse(t *testing.T) {
	f := newFixture(t)
	f.bootstrap(t)
	// 重复使用同一 token → 40105
	_, err := f.svc.Bootstrap(context.Background(), &proto.BootstrapRequest{
		BootstrapToken: "boot-token-123", Name: "x", SM2PublicKey: f.pubKeyB64(t),
	})
	if !isErr(err, proto.ErrBadBootstrap) {
		t.Fatalf("重复 bootstrap 应 40105, got %v", err)
	}
}

func TestBootstrapWrongToken(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.Bootstrap(context.Background(), &proto.BootstrapRequest{
		BootstrapToken: "wrong", Name: "x", SM2PublicKey: f.pubKeyB64(t),
	})
	if !isErr(err, proto.ErrBadBootstrap) {
		t.Fatalf("错误 token 应 40105, got %v", err)
	}
}

// ---- Device challenge / register ----

func TestDeviceRegisterFlow(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)
	resp, _ := f.registerDevice(t, admin.Username)

	dev, err := f.st.GetDeviceByID(resp.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	if dev.UserID != admin.UserID {
		t.Fatalf("设备 user_id = %q, want %q", dev.UserID, admin.UserID)
	}
	if dev.Hostname != "WIN-ABC" {
		t.Fatalf("hostname = %q", dev.Hostname)
	}
	if dev.TokenHash == resp.Token {
		t.Fatal("token 明文不应落库")
	}
}

func TestRegisterBadSignature(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)
	ch, _ := f.svc.CreateChallenge(context.Background(), admin.Username)
	_, err := f.svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username: admin.Username, DeviceName: "d", Challenge: ch.Challenge,
		Signature: base64.StdEncoding.EncodeToString([]byte("garbage-sig")),
	})
	if !isErr(err, proto.ErrBadSignature) {
		t.Fatalf("无效签名应 40104, got %v", err)
	}
}

func TestRegisterChallengeReuse(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)
	_, ch := f.registerDevice(t, admin.Username)
	// 同一 challenge 二次使用 → 40106（一次性核销）
	sig, _ := crypto.SM2SignChallenge(f.priv, []byte(ch))
	_, err := f.svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username: admin.Username, DeviceName: "d2", Challenge: ch,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	if !isErr(err, proto.ErrBadChallenge) {
		t.Fatalf("challenge 复用应 40106, got %v", err)
	}
}

func TestRegisterChallengeExpired(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)
	ch, _ := f.svc.CreateChallenge(context.Background(), admin.Username)
	// 时间推进超过 5 分钟
	f.now = f.now.Add(ChallengeTTL*time.Second + time.Second)
	f.svc.now = func() time.Time { return f.now }
	sig, _ := crypto.SM2SignChallenge(f.priv, []byte(ch.Challenge))
	_, err := f.svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username: admin.Username, DeviceName: "d", Challenge: ch.Challenge,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	if !isErr(err, proto.ErrBadChallenge) {
		t.Fatalf("过期 challenge 应 40106, got %v", err)
	}
}

func TestRegisterChallengeWrongUser(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)
	// 用 admin 的公钥但伪造一个不存在用户拿到的 challenge 来签名——这里验证 challenge 绑定 user_id：
	// 为 u-other 生成挑战，再用 admin 的 user_id 注册 → challenge 不匹配
	_, _ = f.svc.CreateChallenge(context.Background(), "u-other")
	// 生成一个新挑战给 admin（覆盖上一条）
	ch, _ := f.svc.CreateChallenge(context.Background(), admin.Username)
	sig, _ := crypto.SM2SignChallenge(f.priv, []byte(ch.Challenge+"x")) // 篡改挑战
	_, err := f.svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username: admin.Username, DeviceName: "d", Challenge: ch.Challenge,
		Signature: base64.StdEncoding.EncodeToString(sig),
	})
	if !isErr(err, proto.ErrBadSignature) {
		t.Fatalf("篡改挑战签名应 40104, got %v", err)
	}
}

func TestCreateChallengeForRevokedUser(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)
	// 吊销 admin
	_ = f.st.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.SetUserRevoked(admin.UserID, f.now.Unix())
	})
	_, err := f.svc.CreateChallenge(context.Background(), admin.Username)
	if !isErr(err, proto.ErrUserRevoked) {
		t.Fatalf("吊销用户取挑战应 40301, got %v", err)
	}
}

func isErr(err error, code int) bool {
	return CodeOf(err) == code
}

// ---- 补充覆盖：util / 错误路径 ----

func TestErrorString(t *testing.T) {
	e := &Error{Code: 40001, Message: "bad"}
	if e.Error() != "bad" {
		t.Fatalf("Error() = %q", e.Error())
	}
}

func TestCodeOfNonError(t *testing.T) {
	if CodeOf(errTest) != 50001 {
		t.Fatalf("非 Error 应 50001, got %d", CodeOf(errTest))
	}
}

var errTest = &testErr{}

type testErr struct{}

func (e *testErr) Error() string { return "test" }

func TestParseBadPublicKey(t *testing.T) {
	if _, err := parseSM2PublicKey("not-base64!!!"); err == nil {
		t.Fatal("坏 base64 公钥应报错")
	}
	// base64 合法但 DER 非法
	if _, err := parseSM2PublicKey(base64.StdEncoding.EncodeToString([]byte("garbage-der"))); err == nil {
		t.Fatal("坏 DER 公钥应报错")
	}
}

func TestNewDefaultNow(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	svc := New(st, Options{}) // Now 为 nil → 用默认
	if svc.now == nil {
		t.Fatal("默认 now 未设置")
	}
}

func TestRegisterDeviceUnknownUser(t *testing.T) {
	f := newFixture(t)
	// 无 bootstrap → 未知用户取挑战应 40001
	_, err := f.svc.CreateChallenge(context.Background(), "no-user")
	if !isErr(err, proto.ErrBadRequest) {
		t.Fatalf("未知用户取挑战应 40001, got %v", err)
	}
	// 对未知用户注册（挑战不存在）→ 40106
	_, err = f.svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username: "no-user", DeviceName: "d", Challenge: "x", Signature: "x",
	})
	if !isErr(err, proto.ErrBadChallenge) {
		t.Fatalf("未知用户注册应 40106, got %v", err)
	}
}

func TestRegisterBadChallengeFormat(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)
	ch, _ := f.svc.CreateChallenge(context.Background(), admin.Username)
	// 签名不是 base64 → 40001
	_, err := f.svc.RegisterDevice(context.Background(), &proto.DeviceRegisterRequest{
		Username: admin.Username, DeviceName: "d", Challenge: ch.Challenge,
		Signature: "!!!not-base64!!!",
	})
	if !isErr(err, proto.ErrBadRequest) {
		t.Fatalf("坏签名 base64 应 40001, got %v", err)
	}
}

// 惰性清理：超过阈值时过期挑战被清除（P2 DoS 面防护）。
func TestChallengeLazyCleanup(t *testing.T) {
	f := newFixture(t)
	admin := f.bootstrap(t)

	// 塞入 1001 个过期挑战（直接操作内部 map）
	f.svc.challengesMu.Lock()
	for i := 0; i < 1001; i++ {
		f.svc.challenges[fmt.Sprintf("u-%d", i)] = pendingChallenge{
			challenge: "expired", expiresAt: f.now.Unix() - 1,
		}
	}
	f.svc.challengesMu.Unlock()

	// 再次 CreateChallenge 触发惰性清理（len > 1000）
	if _, err := f.svc.CreateChallenge(context.Background(), admin.Username); err != nil {
		t.Fatalf("CreateChallenge: %v", err)
	}
	f.svc.challengesMu.Lock()
	defer f.svc.challengesMu.Unlock()
	// 过期条目应被清理（旧 1001 个全过期 + 新增 1 个有效）
	if len(f.svc.challenges) > 2 {
		t.Fatalf("惰性清理后仍有 %d 条", len(f.svc.challenges))
	}
}
