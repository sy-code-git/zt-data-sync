package crypto

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// 补充错误路径与边界覆盖（提升 crypto 覆盖率至 ≥90%）。

func TestVerifyHMACSM3WrongTagLen(t *testing.T) {
	key := []byte("k")
	msg := []byte("m")
	tag := HMACSM3(key, msg)
	// tag 长度错误
	if VerifyHMACSM3(key, msg, tag[:16]) {
		t.Fatal("截断 tag 应校验失败")
	}
	if VerifyHMACSM3(key, msg, append(tag, 0)) {
		t.Fatal("加长 tag 应校验失败")
	}
}

func TestDeriveKEKDefaultIter(t *testing.T) {
	salt := bytes.Repeat([]byte{0x77}, SaltSize)
	k := DeriveKEK([]byte("pw"), salt, 0) // iter<=0 走默认
	if len(k) != KEKSize {
		t.Fatalf("len=%d, want %d", len(k), KEKSize)
	}
}

func TestKeyfileValidateBranches(t *testing.T) {
	mk := func(mut func(*Keyfile)) *Keyfile {
		k := &Keyfile{
			V:     1,
			KDF:   KeyfileKDF{Alg: "PBKDF2-SM3", Salt: make([]byte, SaltSize), Iter: PBKDF2Iter},
			Nonce: make([]byte, SM4NonceSize),
			CT:    []byte("cipher"),
		}
		mut(k)
		return k
	}

	if err := mk(func(k *Keyfile) { k.KDF.Alg = "SHA256" }).Validate(); err == nil {
		t.Fatal("错误 KDF alg 应失败")
	}
	if err := mk(func(k *Keyfile) { k.KDF.Salt = make([]byte, 8) }).Validate(); err == nil {
		t.Fatal("错误 salt 长度应失败")
	}
	if err := mk(func(k *Keyfile) { k.KDF.Iter = 0 }).Validate(); err == nil {
		t.Fatal("无效 iter 应失败")
	}
	if err := mk(func(k *Keyfile) { k.Nonce = make([]byte, 4) }).Validate(); err == nil {
		t.Fatal("错误 nonce 长度应失败")
	}
	if err := mk(func(k *Keyfile) { k.CT = nil }).Validate(); err == nil {
		t.Fatal("空 ct 应失败")
	}
	if err := mk(func(k *Keyfile) { k.V = 3 }).Validate(); err == nil {
		t.Fatal("错误版本应失败")
	}
}

func TestKeyfileFileErrors(t *testing.T) {
	// 保存到不存在目录
	kf := &Keyfile{V: 1, KDF: KeyfileKDF{Alg: "PBKDF2-SM3", Salt: make([]byte, 16), Iter: 1}, Nonce: make([]byte, 12), CT: []byte("x")}
	if err := kf.SaveToFile("/nonexistent-dir-xyz/abc.key"); err == nil {
		t.Fatal("保存到不存在目录应失败")
	}
	// 读取不存在文件
	if _, err := LoadKeyfile("/nonexistent-dir-xyz/abc.key"); err == nil {
		t.Fatal("读取不存在文件应失败")
	}
	// 文件内容非法
	path := t.TempDir() + "/bad.key"
	if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadKeyfile(path); err == nil {
		t.Fatal("非法文件内容应解析失败")
	}
}

func TestSM2SerializeErrors(t *testing.T) {
	// nil 公钥
	if _, err := MarshalSM2PublicKey(nil); err == nil {
		t.Fatal("nil 公钥序列化应失败")
	}
	if _, err := MarshalSM2PrivateKey(nil); err == nil {
		t.Fatal("nil 私钥序列化应失败")
	}
	// 垃圾 DER
	if _, err := UnmarshalSM2PublicKey([]byte("garbage")); err == nil {
		t.Fatal("垃圾公钥 DER 应失败")
	}
	if _, err := UnmarshalSM2PrivateKey([]byte("garbage")); err == nil {
		t.Fatal("垃圾私钥 DER 应失败")
	}
}

func TestGenerateSM2Key(t *testing.T) {
	priv, err := GenerateSM2Key()
	if err != nil {
		t.Fatal(err)
	}
	if priv.D == nil || priv.X == nil || priv.Y == nil {
		t.Fatal("生成的密钥不完整")
	}
}

func TestSM2WrapNilGuard(t *testing.T) {
	priv, _ := GenerateSM2Key()
	dek := bytes.Repeat([]byte{0xAB}, SM4KeySize)
	wrapped, err := SM2WrapDEK(&priv.PublicKey, dek)
	if err != nil {
		t.Fatalf("包裹数据: %v", err)
	}
	got, err := SM2UnwrapDEK(priv, wrapped)
	if err != nil {
		t.Fatalf("解包数据: %v", err)
	}
	if !bytes.Equal(got, dek) {
		t.Fatalf("解包结果与原文不一致")
	}
	// 过短密文（封装层防护，避免 gmsm 畸形输入异常路径）
	if _, err := SM2UnwrapDEK(priv, []byte("short")); err == nil {
		t.Fatal("过短密文应解包失败")
	}
	// 96 字节边界：gmsm 合法密文带 0x04 前缀，最短 97B；
	// 96B 输入若放行会让 gmsm 走负数 make 路径（panic），必须拒绝
	if _, err := SM2UnwrapDEK(priv, make([]byte, 96)); err == nil {
		t.Fatal("96 字节畸形密文应解包失败")
	}
}

func TestKeyfileErrorStrings(t *testing.T) {
	// 确保错误信息含包前缀，便于排查
	if _, err := ParseKeyfile([]byte("@")); err == nil || !strings.Contains(err.Error(), "keyfile") {
		t.Fatalf("解析错误应带 keyfile 前缀，实际: %v", err)
	}
	if _, err := SM4GCMOpen([]byte("short"), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "SM4") {
		t.Fatalf("SM4 错误应带 SM4 提示，实际: %v", err)
	}
}

func TestKeyfileValidatePropagation(t *testing.T) {
	// JSON 合法但内容非法 → ParseKeyfile 走 Validate 分支
	bad := []byte(`{"v":9,"kdf":{"alg":"PBKDF2-SM3","salt":"AAAAAAAAAAAAAAAAAAAAAA==","iter":1},"nonce":"AAAAAAAAAAAAAAAA","ct":"eA=="}`)
	if _, err := ParseKeyfile(bad); err == nil {
		t.Fatal("内容非法 keyfile 应解析失败")
	}
	// 结构合法但 Validate 失败 → DecryptPrivateKey 前置拦截
	kf := &Keyfile{V: 2, KDF: KeyfileKDF{Alg: "PBKDF2-SM3", Salt: make([]byte, 16), Iter: 1}, Nonce: make([]byte, 12), CT: []byte("x")}
	if _, err := kf.DecryptPrivateKey([]byte("pw")); err == nil {
		t.Fatal("非法 keyfile 解密应被 Validate 拦截")
	}
}

// 构造不在曲线上的 SM2 公钥 DER（点 (0,0) 不在 sm2p256v1 曲线上），
// 验证 UnmarshalSM2PublicKey 拒绝畸形公钥（P1 网络输入面）。
func TestUnmarshalSM2RejectsOffCurve(t *testing.T) {
	// SPKI: SEQUENCE { SEQUENCE { OID ecPublicKey, OID sm2p256v1 }, BIT STRING 0x04||64B }
	// 点 (0,0)：0x04 || 32B零 || 32B零
	bitString := append([]byte{0x04}, make([]byte, 64)...)
	der := buildSM2SPKI(bitString)
	if _, err := UnmarshalSM2PublicKey(der); err == nil {
		t.Fatal("不在曲线上的公钥应被拒绝")
	}

	// 合法公钥应通过
	priv, _ := GenerateSM2Key()
	okDER, _ := MarshalSM2PublicKey(&priv.PublicKey)
	if _, err := UnmarshalSM2PublicKey(okDER); err != nil {
		t.Fatalf("合法公钥应通过: %v", err)
	}
}

func TestRandomNegative(t *testing.T) {
	if _, err := Random(-1); err == nil {
		t.Fatal("负数长度应报错")
	}
	if _, err := Random(0); err != nil {
		t.Fatalf("0 长度应合法: %v", err)
	}
}
