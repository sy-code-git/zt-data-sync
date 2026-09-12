// Package buildinfo 提供服务端的版本与构建信息（§12.3 可观测性）。
//
// 构建期注入（发布/部署推荐）：
//
//	go build -trimpath -ldflags "-s -w \
//	  -X passbook/internal/buildinfo.Version=v0.5 \
//	  -X passbook/internal/buildinfo.Commit=$(git rev-parse --short HEAD) \
//	  -X passbook/internal/buildinfo.BuildTime=$(date -u +%FT%TZ)" ./cmd/server
//
// 未注入时回退读取 Go 内置 VCS 信息（debug.ReadBuildInfo）：只要在 git 工作区内构建，
// 本地 go build / go run 也能报出真实 commit 与提交时间，不会出现"永远是 dev"的空壳。
package buildinfo

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// 以下三个变量供 -ldflags -X 覆盖（必须保持为可寻址的字符串变量，勿改成 const）。
var (
	Version   = "dev" // 语义化版本，如 v0.5
	Commit    = ""    // 构建提交号
	BuildTime = ""    // 构建时间（RFC3339）
)

// Info 构建信息快照（/version 的 wire 契约）。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
	GoVersion string `json:"go_version"`
	Dirty     bool   `json:"dirty"` // 构建时工作区是否有未提交改动（仅 VCS 回退时可知）
}

var (
	once   sync.Once
	cached Info
)

// Get 返回解析后的构建信息（首次调用解析并缓存；进程内不变）。
func Get() Info {
	once.Do(func() { cached = resolve() })
	return cached
}

func resolve() Info {
	info := Info{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
		GoVersion: runtime.Version(),
	}
	// VCS 信息由 go 在构建时写入（git 工作区内构建时存在）；已注入的字段优先，不覆盖
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return withDefaults(info)
	}
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			if info.Commit == "" {
				info.Commit = shortCommit(s.Value)
			}
		case "vcs.time":
			if info.BuildTime == "" {
				info.BuildTime = s.Value
			}
		case "vcs.modified":
			info.Dirty = s.Value == "true"
		}
	}
	return withDefaults(info)
}

func withDefaults(info Info) Info {
	if info.Commit == "" {
		info.Commit = "unknown"
	}
	if info.BuildTime == "" {
		info.BuildTime = "unknown"
	}
	return info
}

// shortCommit 提交号取前 12 位（与 git 习惯一致，够区分即可）。
func shortCommit(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// String 一行摘要，用于启动日志。
func String() string {
	i := Get()
	s := i.Version + " (commit " + i.Commit + ", built " + i.BuildTime + ", " + i.GoVersion
	if i.Dirty {
		s += ", 工作区有未提交改动"
	}
	return s + ")"
}
