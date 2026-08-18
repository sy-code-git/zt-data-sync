package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/term"

	"passbook/internal/crypto"
)

// pubJSON 注册凭证文件（§4.4 / §10：pub.json，管理端导入用）。
type pubJSON struct {
	Name         string `json:"name"`
	SM2PublicKey string `json:"sm2_public_key"` // base64(DER)
	Attestation  string `json:"attestation"`    // base64(HMAC-SM3)
}

// attestationPrefix 注册凭证计算前缀（与 server/api 一致，§4.4）。
const attestationPrefix = "passbook-attestation-v1"

// runGenUser keytool genuser --name N [--out PREFIX]（§10）。
// 生成 SM2 密钥对 → 交互式设置 keyfile 口令 → 输出 .key + .pub.json。
func runGenUser(args []string) error {
	fs := flag.NewFlagSet("genuser", flag.ExitOnError)
	name := fs.String("name", "", "成员显示名（必填）")
	out := fs.String("out", "", "输出前缀（默认 ./<name>）")
	force := fs.Bool("force", false, "覆盖已存在的 keyfile/pub.json（先备份 .bak.<ts>）")
	_ = fs.Parse(args)

	if *name == "" {
		return errors.New("--name 必填")
	}
	prefix := *out
	if prefix == "" {
		prefix = *name
	}
	keyPath := prefix + ".key"
	pubPath := prefix + ".pub.json"
	// 覆盖保护（§10）：已存在时拒绝，防误覆盖丢旧密钥（keyfile 丢失不可恢复）；-force 先备份
	for _, p := range []string{keyPath, pubPath} {
		if _, err := os.Stat(p); err == nil {
			if !*force {
				return fmt.Errorf("%s 已存在，拒绝覆盖（keyfile 丢失不可恢复）；确认后加 --force（旧文件将备份为 .bak.<ts>）", p)
			}
			bak := fmt.Sprintf("%s.bak.%d", p, time.Now().Unix())
			if err := os.Rename(p, bak); err != nil {
				return fmt.Errorf("备份旧文件失败: %w", err)
			}
			fmt.Printf("已备份旧文件: %s → %s\n", p, bak)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	// 1. 生成 SM2 密钥对
	priv, err := crypto.GenerateSM2Key()
	if err != nil {
		return err
	}
	privDER, err := crypto.MarshalSM2PrivateKey(priv)
	if err != nil {
		return err
	}
	defer crypto.Wipe(privDER)

	// 2. 交互式设置口令（不回显、二次确认、<12 位强度警告）
	pass, err := promptNewPassword()
	if err != nil {
		return err
	}

	// 3. 生成 keyfile 并落盘（0600）
	kf, err := crypto.NewKeyfile(privDER, pass)
	if err != nil {
		return err
	}
	if err := kf.SaveToFile(keyPath); err != nil {
		return err
	}
	crypto.Wipe(pass)

	// 4. 生成 pub.json（含 attestation）
	regSecret := os.Getenv("PB_REG_SECRET")
	if regSecret == "" {
		return fmt.Errorf("PB_REG_SECRET 未设置：无法计算注册凭证（§4.4）")
	}
	pubDER, err := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	if err != nil {
		return err
	}
	pubB64 := base64.StdEncoding.EncodeToString(pubDER)
	att := crypto.HMACSM3([]byte(regSecret), []byte(attestationPrefix+*name+pubB64))

	data, err := json.MarshalIndent(&pubJSON{Name: *name,
		SM2PublicKey: pubB64,
		Attestation:  base64.StdEncoding.EncodeToString(att),
	}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(pubPath, data, 0o600); err != nil {
		return err
	}

	fmt.Printf("已生成:\n  %s  （keyfile，口令保护，权限 0600）\n  %s  （注册凭证，导入管理端用）\n", keyPath, pubPath)
	fmt.Println("请将 keyfile 与口令分两条安全通道转交本人（§4.4）。")
	return nil
}

// promptNewPassword 交互式口令输入：不回显、二次确认、<12 位强度警告（§10）。
// 非交互脚本可通过 PB_PASSWORD 环境变量提供口令（e2e 验收脚本用），跳过二次确认。
func promptNewPassword() ([]byte, error) {
	if p := os.Getenv("PB_PASSWORD"); p != "" {
		if len(p) < 12 {
			fmt.Println("⚠ 口令少于 12 位，强度不足，建议使用更长口令（仍可继续）")
		}
		return []byte(p), nil
	}
	for {
		p1, err := readPass("设置 keyfile 口令（不回显）: ")
		if err != nil {
			return nil, err
		}
		if len(p1) < 12 {
			fmt.Println("⚠ 口令少于 12 位，强度不足，建议使用更长口令（仍可继续）")
		}
		p2, err := readPass("再次输入口令确认: ")
		if err != nil {
			crypto.Wipe(p1)
			return nil, err
		}
		if !bytes.Equal(p1, p2) {
			fmt.Println("两次输入不一致，请重试")
			crypto.Wipe(p1)
			crypto.Wipe(p2)
			continue
		}
		crypto.Wipe(p2)
		return p1, nil
	}
}

// readPassword 读取一行不回显输入。
func readPassword(prompt string) ([]byte, error) {
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, fmt.Errorf("读取口令失败: %w", err)
	}
	return pw, nil
}

// readPass 可注入的口令读取函数（测试替换用）。
var readPass = readPassword

// runPubKey keytool pubkey --key <file> [--out <der>]（§10）。
// 输入口令解出私钥，显示/导出公钥。
func runPubKey(args []string) error {
	fs := flag.NewFlagSet("pubkey", flag.ExitOnError)
	key := fs.String("key", "", "keyfile 路径（必填）")
	out := fs.String("out", "", "公钥 DER 导出路径（可选；不填则打印 base64）")
	_ = fs.Parse(args)
	if *key == "" {
		return errors.New("--key 必填")
	}

	kf, err := crypto.LoadKeyfile(*key)
	if err != nil {
		return err
	}
	pass, err := readPass("输入 keyfile 口令: ")
	if err != nil {
		return err
	}
	defer crypto.Wipe(pass)

	privDER, err := kf.DecryptPrivateKey(pass)
	if err != nil {
		return fmt.Errorf("口令错误或 keyfile 损坏: %w", err)
	}
	defer crypto.Wipe(privDER)
	priv, err := crypto.UnmarshalSM2PrivateKey(privDER)
	if err != nil {
		return err
	}
	pubDER, err := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	if err != nil {
		return err
	}

	if *out != "" {
		if err := os.WriteFile(*out, pubDER, 0o600); err != nil {
			return err
		}
		fmt.Printf("公钥 DER 已导出: %s\n", *out)
		return nil
	}
	fmt.Println(base64.StdEncoding.EncodeToString(pubDER))
	return nil
}

// runInspect keytool inspect --key <file>（§10）。
// 校验 keyfile 完整性（格式与 kdf 参数，不解密内容）。
func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	key := fs.String("key", "", "keyfile 路径（必填）")
	_ = fs.Parse(args)
	if *key == "" {
		return errors.New("--key 必填")
	}
	kf, err := crypto.LoadKeyfile(*key)
	if err != nil {
		return err
	}
	if err := kf.Validate(); err != nil {
		return fmt.Errorf("keyfile 校验失败: %w", err)
	}
	fmt.Printf("keyfile 完整: v=%d, kdf=%s, iter=%d, salt=%dB, nonce=%dB, ct=%dB\n",
		kf.V, kf.KDF.Alg, kf.KDF.Iter, len(kf.KDF.Salt), len(kf.Nonce), len(kf.CT))
	return nil
}
