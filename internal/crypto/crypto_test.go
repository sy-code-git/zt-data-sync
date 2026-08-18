package crypto

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRandomLength(t *testing.T) {
	b, err := Random(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) != 16 {
		t.Fatalf("len = %d, want 16", len(b))
	}
}

func TestRandomUniqueness(t *testing.T) {
	a, _ := Random(32)
	b, _ := Random(32)
	if bytes.Equal(a, b) {
		t.Fatal("两次随机数相同")
	}
}

func TestWipe(t *testing.T) {
	b := []byte{1, 2, 3, 4, 5}
	Wipe(b)
	for i, v := range b {
		if v != 0 {
			t.Fatalf("b[%d] = %d, want 0", i, v)
		}
	}
	WipeAll([]byte{1}, []byte{2})
}

func TestLengthPrefixed(t *testing.T) {
	got := LengthPrefixed([]byte("ab"), []byte("c"))
	// 期望输出：uint32(2)||"ab"||uint32(1)||"c"，共 11 字节
	want := []byte{0, 0, 0, 2, 'a', 'b', 0, 0, 0, 1, 'c'}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// 无字段
	if len(LengthPrefixed()) != 0 {
		t.Fatal("empty input should be empty output")
	}
}

// ---- SM4-GCM ----

func TestSM4GCMRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, SM4KeySize)
	aad := LengthPrefixed([]byte("entry-1"), []byte("group-1"))
	pt := []byte(`{"title":"prod-web root"}`)

	nonce, ct, err := SM4GCMSeal(key, aad, pt)
	if err != nil {
		t.Fatal(err)
	}
	if len(nonce) != SM4NonceSize {
		t.Fatalf("nonce len = %d, want %d", len(nonce), SM4NonceSize)
	}
	out, err := SM4GCMOpen(key, nonce, aad, ct)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, pt) {
		t.Fatalf("roundtrip mismatch: %q != %q", out, pt)
	}
}

func TestSM4GCMTamperFails(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, SM4KeySize)
	aad := LengthPrefixed([]byte("e1"), []byte("g1"))
	pt := []byte("secret data")
	nonce, ct, _ := SM4GCMSeal(key, aad, pt)

	// ct 末尾翻一位
	badCT := make([]byte, len(ct))
	copy(badCT, ct)
	badCT[len(badCT)-1] ^= 0x01
	if _, err := SM4GCMOpen(key, nonce, aad, badCT); err == nil {
		t.Fatal("篡改 ct 应失败")
	}
	// nonce 翻一位
	badNonce := make([]byte, len(nonce))
	copy(badNonce, nonce)
	badNonce[0] ^= 0x01
	if _, err := SM4GCMOpen(key, badNonce, aad, ct); err == nil {
		t.Fatal("篡改 nonce 应失败")
	}
	// aad 篡改
	badAAD := LengthPrefixed([]byte("e2"), []byte("g1"))
	if _, err := SM4GCMOpen(key, nonce, badAAD, ct); err == nil {
		t.Fatal("篡改 aad 应失败")
	}
	// 错误密钥
	badKey := bytes.Repeat([]byte{0x22}, SM4KeySize)
	if _, err := SM4GCMOpen(badKey, nonce, aad, ct); err == nil {
		t.Fatal("错误密钥应失败")
	}
}

func TestSM4GCMNonceUniqueness(t *testing.T) {
	key := bytes.Repeat([]byte{0x77}, SM4KeySize)
	pt := []byte("same plaintext")
	aad := []byte("aad")
	n1, c1, _ := SM4GCMSeal(key, aad, pt)
	n2, c2, _ := SM4GCMSeal(key, aad, pt)
	if bytes.Equal(n1, n2) {
		t.Fatal("同明文两次加密 nonce 不应相同")
	}
	if bytes.Equal(c1, c2) {
		t.Fatal("同明文两次加密密文不应相同")
	}
}

func TestSM4GCMBadKey(t *testing.T) {
	if _, _, err := SM4GCMSeal([]byte("short"), nil, []byte("x")); err == nil {
		t.Fatal("短密钥应报错")
	}
	if _, err := SM4GCMOpen([]byte("short"), make([]byte, SM4NonceSize), nil, []byte("x")); err == nil {
		t.Fatal("短密钥应报错")
	}
	if _, err := SM4GCMOpen(bytes.Repeat([]byte{1}, SM4KeySize), []byte("badnonce"), nil, []byte("x")); err == nil {
		t.Fatal("错误 nonce 长度应报错")
	}
}

// ---- HMAC-SM3 ----

func TestHMACSM3(t *testing.T) {
	key := []byte("k")
	msg := []byte("msg")
	tag := HMACSM3(key, msg)
	if len(tag) != 32 {
		t.Fatalf("hmac len = %d, want 32", len(tag))
	}
	if !VerifyHMACSM3(key, msg, tag) {
		t.Fatal("正确 tag 应通过校验")
	}
	if VerifyHMACSM3(key, msg, append([]byte{0}, tag[1:]...)) {
		t.Fatal("篡改 tag 应失败")
	}
	if VerifyHMACSM3([]byte("wrong"), msg, tag) {
		t.Fatal("错误密钥应失败")
	}
	if VerifyHMACSM3(key, []byte("wrong"), tag) {
		t.Fatal("错误消息应失败")
	}
}

func TestEntryHMACKeyDerivation(t *testing.T) {
	dek := bytes.Repeat([]byte{0x99}, SM4KeySize)
	k1 := EntryHMACKey(dek)
	k2 := EntryHMACKey(dek)
	if len(k1) != 16 {
		t.Fatalf("entry hmac key len = %d, want 16", len(k1))
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("同一 DEK 派生应一致")
	}
	if bytes.Equal(k1, EntryHMACKey(bytes.Repeat([]byte{0x98}, SM4KeySize))) {
		t.Fatal("不同 DEK 派生不应相同")
	}
}

func TestDeriveKEKSubkey(t *testing.T) {
	kek := bytes.Repeat([]byte{0xAA}, SM4KeySize)
	a := DeriveKEKSubkey(kek, "plaintext-cache")
	b := DeriveKEKSubkey(kek, "device-token")
	if bytes.Equal(a, b) {
		t.Fatal("不同标签派生应不同")
	}
	if !bytes.Equal(a, DeriveKEKSubkey(kek, "plaintext-cache")) {
		t.Fatal("同标签派生应一致")
	}
	if len(a) != 16 {
		t.Fatalf("len = %d, want 16", len(a))
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !ConstantTimeEqual([]byte("abc"), []byte("abc")) {
		t.Fatal("相等应 true")
	}
	if ConstantTimeEqual([]byte("abc"), []byte("abd")) {
		t.Fatal("不等应 false")
	}
	if ConstantTimeEqual([]byte("a"), []byte("ab")) {
		t.Fatal("不同长度应 false")
	}
}

// ---- KDF ----

func TestDeriveKEK(t *testing.T) {
	salt := bytes.Repeat([]byte{0x5A}, SaltSize)
	k1 := DeriveKEK([]byte("p@ss"), salt, 1000)
	k2 := DeriveKEK([]byte("p@ss"), salt, 1000)
	if len(k1) != 16 {
		t.Fatalf("KEK len = %d, want 16", len(k1))
	}
	if !bytes.Equal(k1, k2) {
		t.Fatal("同口令同盐派生应一致")
	}
	if bytes.Equal(k1, DeriveKEK([]byte("p@ss"), bytes.Repeat([]byte{0x5B}, SaltSize), 1000)) {
		t.Fatal("不同盐应不同")
	}
	if bytes.Equal(k1, DeriveKEK([]byte("other"), salt, 1000)) {
		t.Fatal("不同口令应不同")
	}
}

func TestEncodeUint64(t *testing.T) {
	got := EncodeUint64(1)
	want := []byte{0, 0, 0, 0, 0, 0, 0, 1}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// ---- SM2 ----

func TestSM2RoundTrip(t *testing.T) {
	priv, err := GenerateSM2Key()
	if err != nil {
		t.Fatal(err)
	}
	dek := bytes.Repeat([]byte{0xCD}, 16)

	wrapped, err := SM2WrapDEK(&priv.PublicKey, dek)
	if err != nil {
		t.Fatal(err)
	}
	unwrapped, err := SM2UnwrapDEK(priv, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unwrapped, dek) {
		t.Fatal("SM2 包裹往返不一致")
	}
}

func TestSM2WrongKeyFails(t *testing.T) {
	priv1, _ := GenerateSM2Key()
	priv2, _ := GenerateSM2Key()
	dek := bytes.Repeat([]byte{0x01}, 16)

	wrapped, _ := SM2WrapDEK(&priv1.PublicKey, dek)
	if _, err := SM2UnwrapDEK(priv2, wrapped); err == nil {
		t.Fatal("错误私钥应解包失败")
	}
}

func TestSM2SignVerify(t *testing.T) {
	priv, _ := GenerateSM2Key()
	msg := []byte("challenge-bytes")
	sig, err := SM2SignChallenge(priv, msg)
	if err != nil {
		t.Fatal(err)
	}
	if !SM2VerifyChallenge(&priv.PublicKey, msg, sig) {
		t.Fatal("正确签名应通过")
	}
	if SM2VerifyChallenge(&priv.PublicKey, []byte("wrong"), sig) {
		t.Fatal("错误消息应验签失败")
	}

	priv2, _ := GenerateSM2Key()
	if SM2VerifyChallenge(&priv2.PublicKey, msg, sig) {
		t.Fatal("错误公钥应验签失败")
	}
	// 篡改签名
	bad := append([]byte{0}, sig[1:]...)
	if SM2VerifyChallenge(&priv.PublicKey, msg, bad) {
		t.Fatal("篡改签名应失败")
	}
}

func TestSM2KeySerialize(t *testing.T) {
	priv, _ := GenerateSM2Key()

	// 私钥 DER
	privDER, err := MarshalSM2PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	priv2, err := UnmarshalSM2PrivateKey(privDER)
	if err != nil {
		t.Fatal(err)
	}
	if priv2.D.Cmp(priv.D) != 0 {
		t.Fatal("私钥序列化往返后 D 不一致")
	}

	// 公钥 DER
	pubDER, err := MarshalSM2PublicKey(&priv.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	pub2, err := UnmarshalSM2PublicKey(pubDER)
	if err != nil {
		t.Fatal(err)
	}
	if pub2.X.Cmp(priv.X) != 0 || pub2.Y.Cmp(priv.Y) != 0 {
		t.Fatal("公钥序列化往返后坐标不一致")
	}
}

func TestSM2NilGuard(t *testing.T) {
	if _, err := SM2WrapDEK(nil, []byte("x")); err == nil {
		t.Fatal("nil 公钥应报错")
	}
	if _, err := SM2UnwrapDEK(nil, []byte("x")); err == nil {
		t.Fatal("nil 私钥应报错")
	}
	if _, err := SM2SignChallenge(nil, []byte("x")); err == nil {
		t.Fatal("nil 私钥应报错")
	}
	if SM2VerifyChallenge(nil, []byte("x"), []byte("s")) {
		t.Fatal("nil 公钥验签应 false")
	}
}

// ---- keyfile ----

func TestKeyfileRoundTrip(t *testing.T) {
	priv, _ := GenerateSM2Key()
	privDER, _ := MarshalSM2PrivateKey(priv)

	kf, err := NewKeyfile(privDER, []byte("correct-p@ss"))
	if err != nil {
		t.Fatal(err)
	}
	gotDER, err := kf.DecryptPrivateKey([]byte("correct-p@ss"))
	if err != nil {
		t.Fatal(err)
	}
	defer Wipe(gotDER)
	if !bytes.Equal(gotDER, privDER) {
		t.Fatal("keyfile 往返私钥不一致")
	}
}

func TestKeyfileWrongPass(t *testing.T) {
	priv, _ := GenerateSM2Key()
	privDER, _ := MarshalSM2PrivateKey(priv)
	kf, _ := NewKeyfile(privDER, []byte("right"))

	if _, err := kf.DecryptPrivateKey([]byte("wrong")); err == nil {
		t.Fatal("错误口令应解不出私钥")
	}
}

func TestKeyfileSerialize(t *testing.T) {
	priv, _ := GenerateSM2Key()
	privDER, _ := MarshalSM2PrivateKey(priv)
	kf, _ := NewKeyfile(privDER, []byte("pass"))

	data, err := kf.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseKeyfile(data)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := parsed.DecryptPrivateKey([]byte("pass"))
	if !bytes.Equal(got, privDER) {
		t.Fatal("JSON 往返后私钥不一致")
	}

	// wire format 字段检查
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if m["v"].(float64) != 1 {
		t.Fatalf("v = %v, want 1", m["v"])
	}
	if m["kdf"].(map[string]any)["alg"] != "PBKDF2-SM3" {
		t.Fatal("kdf.alg 应为 PBKDF2-SM3")
	}
}

func TestKeyfileValidate(t *testing.T) {
	kf := &Keyfile{}
	if err := kf.Validate(); err == nil {
		t.Fatal("空 keyfile 应校验失败")
	}
	var nilKF *Keyfile
	if err := nilKF.Validate(); err == nil {
		t.Fatal("nil keyfile 应校验失败")
	}
	// 版本错误
	kf = &Keyfile{V: 2, KDF: KeyfileKDF{Alg: "PBKDF2-SM3", Salt: make([]byte, 16), Iter: 1}, Nonce: make([]byte, 12), CT: []byte("x")}
	if err := kf.Validate(); err == nil {
		t.Fatal("错误版本应校验失败")
	}
	// 解析垃圾数据
	if _, err := ParseKeyfile([]byte("not json")); err == nil {
		t.Fatal("解析垃圾数据应失败")
	}
}

func TestKeyfileFileIO(t *testing.T) {
	priv, _ := GenerateSM2Key()
	privDER, _ := MarshalSM2PrivateKey(priv)
	kf, _ := NewKeyfile(privDER, []byte("pass"))

	path := t.TempDir() + "/test.key"
	if err := kf.SaveToFile(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadKeyfile(path)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := loaded.DecryptPrivateKey([]byte("pass"))
	if !bytes.Equal(got, privDER) {
		t.Fatal("文件 IO 往返不一致")
	}
}

func TestKeyfileTamper(t *testing.T) {
	priv, _ := GenerateSM2Key()
	privDER, _ := MarshalSM2PrivateKey(priv)
	kf, _ := NewKeyfile(privDER, []byte("pass"))

	// 篡改 salt
	tampered := *kf
	tampered.KDF.Salt = append([]byte{0}, kf.KDF.Salt[1:]...)
	if _, err := tampered.DecryptPrivateKey([]byte("pass")); err == nil {
		t.Fatal("篡改 salt 应失败")
	}
	// 篡改 ct
	tampered2 := *kf
	tampered2.CT = append([]byte{0}, kf.CT[1:]...)
	if _, err := tampered2.DecryptPrivateKey([]byte("pass")); err == nil {
		t.Fatal("篡改 ct 应失败")
	}
}

func TestNewKeyfileUniqueSalt(t *testing.T) {
	priv, _ := GenerateSM2Key()
	privDER, _ := MarshalSM2PrivateKey(priv)
	k1, _ := NewKeyfile(privDER, []byte("pass"))
	k2, _ := NewKeyfile(privDER, []byte("pass"))
	if bytes.Equal(k1.KDF.Salt, k2.KDF.Salt) {
		t.Fatal("两次生成 salt 不应相同")
	}
}

// ---- EntryHMAC（§4.3 跨端一致性核心） ----

func TestEntryHMACDeterministic(t *testing.T) {
	dek := bytes.Repeat([]byte{0xDE}, SM4KeySize)
	nonce := bytes.Repeat([]byte{0x01}, SM4NonceSize)
	ct := []byte("ciphertext-bytes")

	h1 := EntryHMAC(dek, "entry-1", "group-1", 3, nonce, ct)
	h2 := EntryHMAC(dek, "entry-1", "group-1", 3, nonce, ct)
	if len(h1) != 32 {
		t.Fatalf("hmac len = %d, want 32", len(h1))
	}
	if !bytes.Equal(h1, h2) {
		t.Fatal("同输入 HMAC 应确定")
	}
}

func TestEntryHMACTamperSensitive(t *testing.T) {
	dek := bytes.Repeat([]byte{0xDE}, SM4KeySize)
	nonce := bytes.Repeat([]byte{0x01}, SM4NonceSize)
	ct := []byte("ciphertext-bytes")
	base := EntryHMAC(dek, "entry-1", "group-1", 3, nonce, ct)

	cases := [][]byte{
		EntryHMAC(dek, "entry-2", "group-1", 3, nonce, ct),        // entry_id 变
		EntryHMAC(dek, "entry-1", "group-2", 3, nonce, ct),        // group_id 变
		EntryHMAC(dek, "entry-1", "group-1", 4, nonce, ct),        // kv 变
		EntryHMAC(dek, "entry-1", "group-1", 3, append(nonce, 0), ct), // nonce 变
		EntryHMAC(dek, "entry-1", "group-1", 3, nonce, append(ct, 0)), // ct 变
		EntryHMAC(bytes.Repeat([]byte{0xDF}, SM4KeySize), "entry-1", "group-1", 3, nonce, ct), // dek 变
	}
	for i, c := range cases {
		if bytes.Equal(base, c) {
			t.Fatalf("case %d: 篡改后 HMAC 不应相同", i)
		}
	}
}

func TestEntryHMACMatchesManualAssemble(t *testing.T) {
	// 与手工组装（长度前缀 + EncodeUint64）一致，验证封装没有改语义
	dek := bytes.Repeat([]byte{0xAA}, SM4KeySize)
	entryID := "e1"
	groupID := "g1"
	kv := 2
	nonce := []byte("n")
	ct := []byte("c")

	want := HMACSM3(
		EntryHMACKey(dek),
		LengthPrefixed([]byte(entryID), []byte(groupID), EncodeUint64(uint64(kv)), nonce, ct),
	)
	got := EntryHMAC(dek, entryID, groupID, kv, nonce, ct)
	if !bytes.Equal(got, want) {
		t.Fatal("EntryHMAC 与手工组装结果不一致")
	}
}
