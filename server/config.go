// Package server 提供服务端配置集中读取（§12.2）。
// 所有环境变量仅在此文件读取，包内其他文件一律从 Config 取值。
package server

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config 服务端全部配置（对应设计文档 §12.2 环境变量表）。
// 默认值严格按文档定值；PB_REG_SECRET 与 TLS 证书无默认（必配或告警）。
type Config struct {
	DataDir string
	Addr    string

	TLSCert string
	TLSKey  string

	// 首启引导 token（可选；未设置则启动时打印一次到日志）
	BootstrapCode string
	// 注册凭证密钥（keytool 与服务端共享，校验 pub.json attestation，必配）
	RegSecret string

	TokenTTL time.Duration

	// CORSOrigins 允许跨域访问的来源白名单（§9.3；空 = 同源部署，不放宽）
	CORSOrigins map[string]struct{}

	RateAuth      int // 认证接口限流 5/min/IP
	RateSync      int // 同步接口限流 120/min/token
	RateHeartbeat int // 心跳接口限流 30/min/token
	RateAdmin     int // Admin 接口限流 30/min/token
	SSEMaxConn    int // SSE 每 token 并发上限
	TombstoneDays int // 墓碑保留天数
	TombstoneHour int // 墓碑清理执行时刻（UTC 小时）
}

// Load 从环境变量读取配置并应用默认值。
// 配置项解析失败（非空但非法）返回 error，不静默回退默认值（§14.1 错误不吞）。
func Load() (*Config, error) {
	c := &Config{
		DataDir: getenv("PB_DATA_DIR", defaultDataDir()),
		Addr:    getenv("PB_ADDR", ":8443"),

		TLSCert: os.Getenv("PB_TLS_CERT"),
		TLSKey:  os.Getenv("PB_TLS_KEY"),

		BootstrapCode: os.Getenv("PB_BOOTSTRAP_CODE"),
		RegSecret:     os.Getenv("PB_REG_SECRET"),
	}

	var err error
	if c.TokenTTL, err = durEnv("PB_TOKEN_TTL", 720*time.Hour); err != nil {
		return nil, err
	}
	c.CORSOrigins = parseOrigins(os.Getenv("PB_CORS_ORIGINS"))

	if c.RateAuth, err = intEnv("PB_RATE_AUTH", 5); err != nil {
		return nil, err
	}
	if c.RateSync, err = intEnv("PB_RATE_SYNC", 120); err != nil {
		return nil, err
	}
	if c.RateHeartbeat, err = intEnv("PB_RATE_HEARTBEAT", 30); err != nil {
		return nil, err
	}
	if c.RateAdmin, err = intEnv("PB_RATE_ADMIN", 30); err != nil {
		return nil, err
	}
	if c.SSEMaxConn, err = intEnv("PB_SSE_MAX_CONN", 4); err != nil {
		return nil, err
	}
	if c.TombstoneDays, err = intEnv("PB_TOMBSTONE_DAYS", 90); err != nil {
		return nil, err
	}
	if c.TombstoneHour, err = intEnv("PB_TOMBSTONE_CLEAN_HOUR", 3); err != nil {
		return nil, err
	}

	if c.RegSecret == "" {
		return nil, fmt.Errorf("PB_REG_SECRET 未设置：keytool 与服务端共享的注册凭证密钥，必配（§12.2）")
	}

	if (c.TLSCert == "") != (c.TLSKey == "") {
		return nil, fmt.Errorf("PB_TLS_CERT 与 PB_TLS_KEY 必须同时设置或同时为空")
	}

	return c, nil
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func intEnv(key string, def int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s 非法整数值 %q：%w", key, v, err)
	}
	return n, nil
}

func durEnv(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s 非法时长 %q：%w", key, v, err)
	}
	return d, nil
}

// defaultDataDir 平台相关默认数据目录（§12.2：Linux/容器 /data；Windows 本地目录）。
func defaultDataDir() string {
	if runtime.GOOS == "windows" {
		if base, err := os.UserHomeDir(); err == nil {
			return filepath.Join(base, ".passbook")
		}
		return `.\passbook-data`
	}
	return "/data"
}

// parseOrigins 解析逗号分隔的来源白名单（空 → 空集合 = 同源模式，§9.3）。
func parseOrigins(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, o := range strings.Split(s, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out[o] = struct{}{}
		}
	}
	return out
}
