package main

import (
	"errors"
	"passbook/client/core/store"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePassword(t *testing.T) {
	pw, err := generatePassword(16)
	if err != nil {
		t.Fatal(err)
	}
	if len(pw) != 16 {
		t.Fatalf("len = %d", len(pw))
	}
	// 字符集合法
	for _, c := range pw {
		if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*", c) {
			t.Fatalf("非法字符 %q", c)
		}
	}
	// 随机性
	pw2, _ := generatePassword(16)
	if pw == pw2 {
		t.Fatal("两次生成不应相同")
	}
	// 边界
	if _, err := generatePassword(3); err == nil {
		t.Fatal("过短应报错")
	}
	if _, err := generatePassword(200); err == nil {
		t.Fatal("过长应报错")
	}
}

// §9.2 服务端地址配置：首次使用无配置 → 引导提示；已存配置自动带出；--reinit 重启引导。
func TestUnlockServerConfig(t *testing.T) {
	// 1. 首次：无 --server、库无配置 → 报引导提示（无需真实 keyfile，server 解析在口令输入前）
	dir1 := t.TempDir()
	err := cmdUnlock([]string{"--keyfile", "x.key", "--data", dir1})
	if err == nil || !strings.Contains(err.Error(), "首次使用") {
		t.Fatalf("首次无 server 应报引导提示, got: %v", err)
	}
	// 2. 库已存配置 → 自动带出（不再报"未配置"；注入口令读取失败以提前终止，验证走了带出分支）
	dir2 := t.TempDir()
	ls, _ := store.OpenLocal(filepath.Join(dir2, "local.db"))
	_ = ls.Migrate()
	_ = ls.SetServerURL("https://host:8443")
	_ = ls.Close()
	orig := readPass
	readPass = func(string) ([]byte, error) { return nil, errors.New("stop") }
	defer func() { readPass = orig }()
	err = cmdUnlock([]string{"--keyfile", "x.key", "--data", dir2})
	if err == nil || strings.Contains(err.Error(), "未配置服务端地址") {
		t.Fatalf("已存配置应自动带出（不应报未配置）, got: %v", err)
	}
	if !strings.Contains(err.Error(), "stop") {
		t.Fatalf("应走到口令输入步骤, got: %v", err)
	}
	// 3. --reinit + 无 --server → 忽略已存配置，报引导提示
	err = cmdUnlock([]string{"--keyfile", "x.key", "--data", dir2, "--reinit"})
	if err == nil || !strings.Contains(err.Error(), "首次使用") {
		t.Fatalf("--reinit 应重启引导, got: %v", err)
	}
}
