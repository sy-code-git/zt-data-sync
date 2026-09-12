package buildinfo

import (
	"runtime"
	"testing"
)

// TestGetFillsDefaults 构建信息任何字段都不得为空：/version 与启动日志的可用性依赖它，
// 未注入 ldflags 时也必须回退出可读值（而不是空串）。
func TestGetFillsDefaults(t *testing.T) {
	i := Get()
	if i.Version == "" {
		t.Fatal("Version 不得为空（未注入时应为 dev）")
	}
	if i.Commit == "" {
		t.Fatal("Commit 不得为空（未注入时应回退 VCS 或 unknown）")
	}
	if i.BuildTime == "" {
		t.Fatal("BuildTime 不得为空（未注入时应回退 VCS 或 unknown）")
	}
	if i.GoVersion != runtime.Version() {
		t.Fatalf("GoVersion = %q, want %q", i.GoVersion, runtime.Version())
	}
	// 进程内缓存：两次调用必须一致
	if Get() != i {
		t.Fatal("Get() 应返回缓存值（进程内构建信息不变）")
	}
	if s := String(); len(s) < len(i.Version) {
		t.Fatalf("String() 输出异常: %q", s)
	}
}

// TestShortCommit 提交号截断：git 习惯取前 12 位，短号原样返回。
func TestShortCommit(t *testing.T) {
	if got := shortCommit("0123456789abcdef0123"); got != "0123456789ab" {
		t.Fatalf("shortCommit 长号 = %q, want 0123456789ab", got)
	}
	if got := shortCommit("abc123"); got != "abc123" {
		t.Fatalf("shortCommit 短号 = %q, want abc123", got)
	}
}
