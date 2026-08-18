package vault

import (
	"runtime"
	"testing"
)

// TestAutoUnlockRoundTrip 验证自动解锁完整闭环（§9.1，Windows 专属）。
// 非 Windows 平台跳过（DPAPI 不可用）。
func TestAutoUnlockRoundTrip(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("自动解锁依赖 Windows DPAPI")
	}
	v, path := newTestVault(t)

	// 初始未开启
	if v.AutoUnlockEnabled() {
		t.Fatal("初始不应开启自动解锁")
	}
	// 未解锁时开启应失败
	if err := v.EnableAutoUnlock(path); err == nil {
		t.Fatal("未解锁开启自动解锁应失败")
	}

	// 口令解锁
	if _, err := v.ImportKeyfile(path, []byte("correct-password-123")); err != nil {
		t.Fatalf("ImportKeyfile: %v", err)
	}

	// 开启自动解锁
	if err := v.EnableAutoUnlock(path); err != nil {
		t.Fatalf("EnableAutoUnlock: %v", err)
	}
	if !v.AutoUnlockEnabled() {
		t.Fatal("开启后 AutoUnlockEnabled 应为 true")
	}

	// 锁定
	v.Lock()
	if v.IsUnlocked() {
		t.Fatal("锁定后应锁定")
	}

	// 免口令自动解锁
	ds, err := v.TryAutoUnlock()
	if err != nil {
		t.Fatalf("TryAutoUnlock: %v", err)
	}
	if !v.IsUnlocked() {
		t.Fatal("自动解锁后应解锁")
	}
	if ds != nil {
		t.Fatal("无设备状态时应为 nil")
	}

	// 关闭自动解锁
	if err := v.DisableAutoUnlock(); err != nil {
		t.Fatalf("DisableAutoUnlock: %v", err)
	}
	if v.AutoUnlockEnabled() {
		t.Fatal("关闭后 AutoUnlockEnabled 应为 false")
	}
	// 关闭后锁定，自动解锁应失败（回退口令）
	v.Lock()
	if _, err := v.TryAutoUnlock(); err == nil {
		t.Fatal("关闭自动解锁后 TryAutoUnlock 应失败")
	}
}

// TestAutoUnlockNotEnabled 未开启自动解锁时 TryAutoUnlock 应失败（跨平台）。
func TestAutoUnlockNotEnabled(t *testing.T) {
	v, _ := newTestVault(t)
	if _, err := v.TryAutoUnlock(); err == nil {
		t.Fatal("未开启自动解锁时 TryAutoUnlock 应失败")
	}
}
