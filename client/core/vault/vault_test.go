package vault

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"passbook/client/core/store"
	"passbook/internal/crypto"
	"passbook/internal/model"
)

func newTestVault(t *testing.T) (*Vault, string) {
	t.Helper()
	ls, err := store.OpenLocal(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ls.Close() })
	if err := ls.Migrate(); err != nil {
		t.Fatal(err)
	}
	v := New(ls)

	// 生成 keyfile
	priv, _ := crypto.GenerateSM2Key()
	privDER, _ := crypto.MarshalSM2PrivateKey(priv)
	kf, err := crypto.NewKeyfile(privDER, []byte("correct-password-123"))
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/test.key"
	if err := kf.SaveToFile(path); err != nil {
		t.Fatal(err)
	}
	return v, path
}

func setupDEK(t *testing.T, v *Vault, groupID string, kv int) []byte {
	t.Helper()
	dek, err := v.NewDEK()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SetDEK(groupID, kv, dek); err != nil {
		t.Fatal(err)
	}
	return dek
}

func TestImportKeyfileAndLock(t *testing.T) {
	v, path := newTestVault(t)
	if v.IsUnlocked() {
		t.Fatal("初始应锁定")
	}
	ds, err := v.ImportKeyfile(path, []byte("correct-password-123"))
	if err != nil {
		t.Fatalf("ImportKeyfile: %v", err)
	}
	if !v.IsUnlocked() {
		t.Fatal("导入后应解锁")
	}
	if ds != nil {
		t.Fatal("无设备状态时应为 nil")
	}
	// 错误口令
	if _, err := v.ImportKeyfile(path, []byte("wrong-password-123")); err == nil {
		t.Fatal("错误口令应失败")
	}
	// 锁定
	v.Lock()
	if v.IsUnlocked() {
		t.Fatal("锁定后应锁定")
	}
	if v.PrivateKey() != nil {
		t.Fatal("锁定后私钥应清空")
	}
}

func TestEntryEncryptDecrypt(t *testing.T) {
	v, path := newTestVault(t)
	_, _ = v.ImportKeyfile(path, []byte("correct-password-123"))
	gid := "g1"
	setupDEK(t, v, gid, 1)

	parent := "p1"
	entry := &model.Entry{
		SchemaVersion: 1, Type: model.TypeAccount, Title: "prod root", ParentID: &parent,
		Fields:       model.Fields{"user": jsonRaw(`"root"`), "password": jsonRaw(`"secret"`)},
		CustomFields: map[string]json.RawMessage{"机房": jsonRaw(`"深圳"`)},
	}
	plain, _ := entry.Marshal()

	ct, err := v.EncryptPlaintext(gid, "e1", plain, 1)
	if err != nil {
		t.Fatalf("EncryptPlaintext: %v", err)
	}
	got, err := v.DecryptPlaintext(gid, "e1", ct)
	if err != nil {
		t.Fatalf("DecryptPlaintext: %v", err)
	}
	var gotEntry model.Entry
	if err := json.Unmarshal(got, &gotEntry); err != nil {
		t.Fatalf("明文解析: %v", err)
	}
	if gotEntry.Title != "prod root" || gotEntry.Type != model.TypeAccount {
		t.Fatalf("解密结果: %+v", gotEntry)
	}
	if string(gotEntry.Fields["password"]) != `"secret"` {
		t.Fatal("password 字段丢失")
	}
}

func TestEntryTamperDetected(t *testing.T) {
	v, path := newTestVault(t)
	_, _ = v.ImportKeyfile(path, []byte("correct-password-123"))
	gid := "g1"
	setupDEK(t, v, gid, 1)

	entry := model.NewProject("proj")
	plain, _ := entry.Marshal()
	ct, _ := v.EncryptPlaintext(gid, "e1", plain, 1)
	// 篡改密文（改 ct 一个字节）
	pack, _ := model.ParseCiphertext([]byte(ct))
	pack.CT[0] ^= 0x01
	tampered, _ := model.MarshalCiphertext(pack)
	if _, err := v.DecryptPlaintext(gid, "e1", string(tampered)); err == nil {
		t.Fatal("篡改密文应 HMAC 失败")
	}
	// 篡改 HMAC
	pack2, _ := model.ParseCiphertext([]byte(ct))
	pack2.HMAC[0] ^= 0x01
	tampered2, _ := model.MarshalCiphertext(pack2)
	if _, err := v.DecryptPlaintext(gid, "e1", string(tampered2)); err == nil {
		t.Fatal("篡改 HMAC 应失败")
	}
}

func TestUnwrapDEKAndEncrypt(t *testing.T) {
	v, path := newTestVault(t)
	_, _ = v.ImportKeyfile(path, []byte("correct-password-123"))
	gid := "g1"

	// 生成 DEK 并包裹（模拟服务端信封）
	dek := make([]byte, 16)
	for i := range dek {
		dek[i] = byte(i)
	}
	priv := v.PrivateKey()
	pubDER, _ := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	env, err := v.WrapDEKFor(base64.StdEncoding.EncodeToString(pubDER), dek)
	if err != nil {
		t.Fatalf("WrapDEKFor: %v", err)
	}
	// 解包
	got, err := v.UnwrapDEK(gid, 1, env)
	if err != nil {
		t.Fatalf("UnwrapDEK: %v", err)
	}
	defer crypto.Wipe(got)
	for i := range dek {
		if got[i] != dek[i] {
			t.Fatal("DEK 解包不一致")
		}
	}
	// 解包后可用该 DEK 加密
	entry := model.NewProject("p")
	plain, _ := entry.Marshal()
	if _, err := v.EncryptPlaintext(gid, "e1", plain, 1); err != nil {
		t.Fatalf("解包后加密: %v", err)
	}
}

func TestLockWipesDEK(t *testing.T) {
	v, path := newTestVault(t)
	_, _ = v.ImportKeyfile(path, []byte("correct-password-123"))
	gid := "g1"
	dek := setupDEK(t, v, gid, 1)
	crypto.Wipe(dek) // 测试不再持有

	// 锁定后 GetDEK 应失败（本地缓存已清？锁定不清本地，但内存 DEK 清零）
	v.Lock()
	// 重新解锁后本地 key_cache 仍有加密 DEK，但需 UnwrapDEK 路径；
	// 直接 GetDEK 走本地解密 → 应能取回（解锁态）。此处验证锁定后内存已清
	if len(v.deks) != 0 {
		t.Fatal("锁定后内存 DEK 应清空")
	}
}

func jsonRaw(s string) []byte {
	return []byte(s)
}

func TestVaultLockedGuards(t *testing.T) {
	v, _ := newTestVault(t) // 未解锁
	// 未解锁时各操作应报错
	if _, err := v.encryptLocal("x", []byte("p")); err == nil {
		t.Fatal("未解锁 encryptLocal 应报错")
	}
	if _, err := v.decryptLocal("x", []byte("p")); err == nil {
		t.Fatal("未解锁 decryptLocal 应报错")
	}
	if _, err := v.PublicKeyB64(); err == nil {
		t.Fatal("未解锁 PublicKeyB64 应报错")
	}
	if _, err := v.SignChallenge("c"); err == nil {
		t.Fatal("未解锁 SignChallenge 应报错")
	}
	if _, err := v.GetDEK("g", 1); err == nil {
		t.Fatal("未解锁 GetDEK 应报错")
	}
	// 坏公钥 wrap
	if _, err := v.WrapDEKFor("not-base64", make([]byte, 16)); err == nil {
		t.Fatal("坏公钥 wrap 应报错")
	}
	// 空信封
	if _, err := v.UnwrapDEK("g", 1, "bad-envelope"); err == nil {
		t.Fatal("坏信封应报错")
	}
	// 无效 DEK 缓存操作（未解锁但本地无 DEK）
	ls, _ := store.OpenLocal(":memory:")
	defer ls.Close()
	_ = ls.Migrate()
	v2 := New(ls)
	if _, err := v2.GetDEK("g", 1); err == nil {
		t.Fatal("无 DEK 应报错")
	}
	// InvalidateGroupDEKs 空操作不 panic
	v2.InvalidateGroupDEKs("g")
}

func TestTokenEncryptDecrypt(t *testing.T) {
	v, path := newTestVault(t)
	_, _ = v.ImportKeyfile(path, []byte("correct-password-123"))
	enc, err := v.EncryptToken("raw-token")
	if err != nil {
		t.Fatal(err)
	}
	got, err := v.DecryptToken(enc)
	if err != nil || got != "raw-token" {
		t.Fatalf("token 往返: %q %v", got, err)
	}
}

// P0 回归：loadDeviceState 必须保持 TokenEnc 为密文，供调用方 DecryptToken 一次解密。
// 修复前 loadDeviceState 就地解密成明文，调用方（core.Unlock）再 DecryptToken 二次解密失败，
// 导致已保存 token 的设备每次 Unlock 必报"解密设备 token 失败"。
func TestLoadDeviceStateKeepsTokenCiphertext(t *testing.T) {
	v, path := newTestVault(t)
	if _, err := v.ImportKeyfile(path, []byte("correct-password-123")); err != nil {
		t.Fatal(err)
	}
	// 模拟 token 已加密落盘（bootstrap/register 后经 maybeRefreshToken 持久化）
	enc, err := v.EncryptToken("my-token-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := v.local.SetDeviceState(&store.DeviceState{DeviceID: "d1", TokenEnc: enc, ExpiresAt: 1}); err != nil {
		t.Fatal(err)
	}

	// 重新读（模拟 Unlock 流程 loadDeviceState）
	ds, err := v.loadDeviceState()
	if err != nil {
		t.Fatal(err)
	}
	if ds == nil || len(ds.TokenEnc) == 0 {
		t.Fatal("应读到 device_state")
	}
	// 关键断言：TokenEnc 仍是密文，可一次解密恢复原文
	got, err := v.DecryptToken(ds.TokenEnc)
	if err != nil {
		t.Fatalf("TokenEnc 应保持密文（一次解密即可恢复），实际解密失败: %v", err)
	}
	if got != "my-token-123" {
		t.Fatalf("token 恢复 = %q, want my-token-123", got)
	}
}

func TestDEKCacheRoundTrip(t *testing.T) {
	v, path := newTestVault(t)
	_, _ = v.ImportKeyfile(path, []byte("correct-password-123"))
	dek := make([]byte, 16)
	for i := range dek {
		dek[i] = byte(0xEE)
	}
	_ = v.SetDEK("g9", 3, dek)
	got, err := v.GetDEK("g9", 3)
	if err != nil {
		t.Fatal(err)
	}
	defer crypto.Wipe(got)
	for i := range dek {
		if got[i] != dek[i] {
			t.Fatal("DEK 缓存往返不一致")
		}
	}
}
