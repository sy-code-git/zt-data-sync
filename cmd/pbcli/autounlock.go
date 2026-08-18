package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"passbook/client/core/store"
	"passbook/client/core/vault"
)

// cmdAutoUnlock DPAPI 自动解锁管理（§9.1，Windows 专属）。
// 用法: pbcli autounlock <enable|disable|status|try>
func cmdAutoUnlock(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: pbcli autounlock <enable|disable|status|try>")
	}
	sub := args[0]
	rest := args[1:]

	dataDir := os.Getenv("PB_DATA")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".passbook")
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil { // #nosec G703 -- 本地数据目录，路径来自本机环境变量，非不可信输入
		return err
	}
	local, err := store.OpenLocal(filepath.Join(dataDir, "local.db"))
	if err != nil {
		return err
	}
	defer local.Close()
	if err := local.Migrate(); err != nil {
		return err
	}
	app.local = local
	app.vault = vault.New(local)

	switch sub {
	case "enable":
		return autoUnlockEnable(rest)
	case "disable":
		return autoUnlockDisable()
	case "status":
		return autoUnlockStatus()
	case "try":
		return autoUnlockTry()
	default:
		return fmt.Errorf("未知 autounlock 子命令 %q", sub)
	}
}

// autoUnlockEnable 口令解锁后开启自动解锁：把内存 KEK 用 DPAPI 保护落盘，
// 并记录 keyfile 绝对路径供下次免口令定位（§9.1）。
func autoUnlockEnable(args []string) error {
	fs := flag.NewFlagSet("enable", flag.ExitOnError)
	keyfile := fs.String("keyfile", os.Getenv("PB_KEYFILE"), "keyfile 路径")
	_ = fs.Parse(args)
	if *keyfile == "" {
		return fmt.Errorf("--keyfile 必填（或设 PB_KEYFILE）")
	}
	pass, err := readPass("输入 keyfile 口令: ")
	if err != nil {
		return err
	}
	if _, err := app.vault.ImportKeyfile(*keyfile, pass); err != nil {
		return err
	}
	abs, err := filepath.Abs(*keyfile)
	if err != nil {
		return err
	}
	if err := app.vault.EnableAutoUnlock(abs); err != nil {
		return err
	}
	fmt.Println("已开启自动解锁。")
	return nil
}

// autoUnlockDisable 关闭自动解锁（清除 DPAPI blob，§9.1）。
func autoUnlockDisable() error {
	if err := app.vault.DisableAutoUnlock(); err != nil {
		return err
	}
	fmt.Println("已关闭自动解锁。")
	return nil
}

// autoUnlockStatus 查询自动解锁状态。
func autoUnlockStatus() error {
	cfg, err := app.local.GetAutoUnlock()
	if err != nil {
		return err
	}
	if cfg != nil && cfg.Enabled {
		fmt.Printf("自动解锁已开启。keyfile=%s\n", cfg.KeyfilePath)
	} else {
		fmt.Println("自动解锁未开启。")
	}
	return nil
}

// autoUnlockTry 免口令解锁（DPAPI 取回 KEK）。模拟"重启/锁屏后免输口令"（§9.1）。
// 未开启 / 非 Windows / DPAPI 取回失败均返回错误，调用方回退口令解锁。
func autoUnlockTry() error {
	ds, err := app.vault.TryAutoUnlock()
	if err != nil {
		return fmt.Errorf("自动解锁失败（回退口令解锁）: %w", err)
	}
	deviceID := ""
	if ds != nil {
		deviceID = ds.DeviceID
	}
	fmt.Printf("自动解锁成功。device_id=%s\n", deviceID)
	return nil
}
