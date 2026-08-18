package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"passbook/internal/crypto"
)

// 测试用固定口令注入，避免终端交互。
func injectPass(pass string) func() {
	orig := readPass
	readPass = func(string) ([]byte, error) { return []byte(pass), nil }
	return func() { readPass = orig }
}

func TestGenUserFlow(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	restore := injectPass("correct-password-123")
	defer restore()

	dir := t.TempDir()
	prefix := filepath.Join(dir, "zhangsan")
	if err := runGenUser([]string{"--name", "zhangsan", "--out", prefix}); err != nil {
		t.Fatalf("genuser: %v", err)
	}

	// keyfile 存在且可解密
	kf, err := crypto.LoadKeyfile(prefix + ".key")
	if err != nil {
		t.Fatal(err)
	}
	privDER, err := kf.DecryptPrivateKey([]byte("correct-password-123"))
	if err != nil {
		t.Fatalf("解密 keyfile: %v", err)
	}
	priv, err := crypto.UnmarshalSM2PrivateKey(privDER)
	if err != nil {
		t.Fatal(err)
	}

	// pub.json 内容
	data, err := os.ReadFile(prefix + ".pub.json")
	if err != nil {
		t.Fatal(err)
	}
	var pub pubJSON
	if err := json.Unmarshal(data, &pub); err != nil {
		t.Fatal(err)
	}
	if pub.Name != "zhangsan" {
		t.Fatalf("name = %q", pub.Name)
	}
	// pub.json 公钥 == keyfile 私钥对应公钥
	wantPub, _ := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	if pub.SM2PublicKey != base64.StdEncoding.EncodeToString(wantPub) {
		t.Fatal("pub.json 公钥与 keyfile 不一致")
	}
	// attestation 与服务端逻辑一致（HMAC-SM3(secret, prefix+name+pub)）
	wantAtt := base64.StdEncoding.EncodeToString(crypto.HMACSM3([]byte("test-secret"), []byte(attestationPrefix+"zhangsan"+pub.SM2PublicKey)))
	if pub.Attestation != wantAtt {
		t.Fatal("attestation 与服务端计算不一致")
	}
}

func TestGenUserRequiresRegSecret(t *testing.T) {
	restore := injectPass("correct-password-123")
	defer restore()
	// 未设置 PB_REG_SECRET → 报错
	t.Setenv("PB_REG_SECRET", "")
	if err := runGenUser([]string{"--name", "x", "--out", filepath.Join(t.TempDir(), "x")}); err == nil {
		t.Fatal("缺 PB_REG_SECRET 应报错")
	}
}

func TestGenUserMissingName(t *testing.T) {
	if err := runGenUser([]string{}); err == nil {
		t.Fatal("缺 --name 应报错")
	}
}

func TestPubKeyFlow(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	restore := injectPass("correct-password-123")
	defer restore()

	dir := t.TempDir()
	prefix := filepath.Join(dir, "zhangsan")
	if err := runGenUser([]string{"--name", "zhangsan", "--out", prefix}); err != nil {
		t.Fatal(err)
	}

	// pubkey 导出 DER 文件
	derOut := filepath.Join(dir, "pub.der")
	if err := runPubKey([]string{"--key", prefix + ".key", "--out", derOut}); err != nil {
		t.Fatalf("pubkey: %v", err)
	}
	// 导出的 DER == pub.json 的公钥
	pubData, _ := os.ReadFile(prefix + ".pub.json")
	var pub pubJSON
	_ = json.Unmarshal(pubData, &pub)
	derData, _ := os.ReadFile(derOut)
	if base64.StdEncoding.EncodeToString(derData) != pub.SM2PublicKey {
		t.Fatal("导出的公钥与 pub.json 不一致")
	}
}

func TestPubKeyWrongPassword(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	restore := injectPass("correct-password-123")
	defer restore()
	dir := t.TempDir()
	prefix := filepath.Join(dir, "z")
	if err := runGenUser([]string{"--name", "z", "--out", prefix}); err != nil {
		t.Fatal(err)
	}
	// 错误口令
	restore2 := injectPass("wrong-password-123")
	defer restore2()
	if err := runPubKey([]string{"--key", prefix + ".key"}); err == nil {
		t.Fatal("错误口令应报错")
	}
}

func TestInspectFlow(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	restore := injectPass("correct-password-123")
	defer restore()
	dir := t.TempDir()
	prefix := filepath.Join(dir, "z")
	if err := runGenUser([]string{"--name", "z", "--out", prefix}); err != nil {
		t.Fatal(err)
	}
	if err := runInspect([]string{"--key", prefix + ".key"}); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	// 损坏文件
	badPath := filepath.Join(dir, "bad.key")
	_ = os.WriteFile(badPath, []byte("garbage"), 0o600)
	if err := runInspect([]string{"--key", badPath}); err == nil {
		t.Fatal("损坏 keyfile inspect 应报错")
	}
	if err := runInspect([]string{}); err == nil {
		t.Fatal("缺 --key 应报错")
	}
}

func TestKeytoolNoNetworkImports(t *testing.T) {
	// 静态检查：keytool 源码不得 import 网络包（§10 铁律）
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if f == "main_test.go" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{`"net/http"`, `"net"`, `golang.org/x/net`} {
			if containsStr(string(data), bad) {
				t.Fatalf("%s 包含网络 import: %s", f, bad)
			}
		}
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestPromptNewPasswordRetry(t *testing.T) {
	// 序列口令：第一次不一致 → 重试第二次一致
	passes := []string{"first-password-1", "first-password-2", "good-password-123", "good-password-123"}
	i := 0
	orig := readPass
	readPass = func(string) ([]byte, error) {
		p := passes[i]
		i++
		return []byte(p), nil
	}
	defer func() { readPass = orig }()

	got, err := promptNewPassword()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "good-password-123" {
		t.Fatalf("最终口令 = %q", got)
	}
	if i != 4 {
		t.Fatalf("读取次数 = %d, want 4（首次不一致重试）", i)
	}
}

// C1：genuser 覆盖保护——已存在拒绝，-force 备份后覆盖。
func TestGenUserOverwriteGuard(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	restore := injectPass("correct-password-123")
	defer restore()
	dir := t.TempDir()
	prefix := filepath.Join(dir, "z")
	if err := runGenUser([]string{"--name", "z", "--out", prefix}); err != nil {
		t.Fatal(err)
	}
	// 已存在 → 拒绝覆盖（原文件保留）
	if err := runGenUser([]string{"--name", "z", "--out", prefix}); err == nil {
		t.Fatal("已存在 keyfile 应拒绝覆盖")
	}
	if _, err := os.Stat(prefix + ".key"); err != nil {
		t.Fatal("原 keyfile 应保留")
	}
	// -force → 覆盖且产生 .bak 备份
	if err := runGenUser([]string{"--name", "z", "--out", prefix, "--force"}); err != nil {
		t.Fatalf("-force 覆盖: %v", err)
	}
	baks, _ := filepath.Glob(prefix + ".key.bak.*")
	if len(baks) == 0 {
		t.Fatal("-force 应产生 .bak 备份")
	}
}
