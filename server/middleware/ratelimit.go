package middleware

import (
	"net/http"
	"sync"
	"time"
)

// 限流实现（§8.3 限流策略，固定窗口计数）。

// fixedWindowLimiter 固定窗口限流器：窗口内计数，超限拒绝。
type fixedWindowLimiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	now    func() time.Time
	counts map[string]*windowEntry
}

type windowEntry struct {
	start int64 // 窗口起点（unix 毫秒）
	count int
}

func newFixedWindowLimiter(limit int, window time.Duration, now func() time.Time) *fixedWindowLimiter {
	return &fixedWindowLimiter{
		limit:  limit,
		window: window,
		now:    now,
		counts: map[string]*windowEntry{},
	}
}

// Allow 判断 key 当前窗口是否允许（超限返回 false）。
// 惰性清理过期条目，防止 map 无限增长（P2 内存泄漏防护）。
func (l *fixedWindowLimiter) Allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true // 未配置限流 → 放行
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	nowMs := l.now().UnixMilli()

	// 惰性清理：map 过大时移除过期窗口条目
	if len(l.counts) > 10000 {
		cutoff := nowMs - l.window.Milliseconds()
		for k, e := range l.counts {
			if e.start < cutoff {
				delete(l.counts, k)
			}
		}
	}

	e, ok := l.counts[key]
	if !ok || nowMs-e.start >= l.window.Milliseconds() {
		l.counts[key] = &windowEntry{start: nowMs, count: 1}
		return true
	}
	e.count++
	return e.count <= l.limit
}

// lockoutTracker 认证防暴破（§8.3：连续 10 次失败锁 IP 10 分钟，独立于频率限流）。
type lockoutTracker struct {
	mu           sync.Mutex
	maxFail      int
	lockDuration time.Duration
	now          func() time.Time
	states       map[string]*lockState
}

type lockState struct {
	fails       int
	lockedUntil int64 // unix 毫秒；0 = 未锁定
}

func newLockoutTracker(maxFail int, lockDuration time.Duration, now func() time.Time) *lockoutTracker {
	return &lockoutTracker{
		maxFail:      maxFail,
		lockDuration: lockDuration,
		now:          now,
		states:       map[string]*lockState{},
	}
}

// IsLocked 判断 key（IP）当前是否被锁定。
func (t *lockoutTracker) IsLocked(key string) bool {
	if t == nil || t.maxFail <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	nowMs := t.now().UnixMilli()

	// 惰性清理已过期条目，防 map 无限增长
	if len(t.states) > 10000 {
		for k, st := range t.states {
			if st.lockedUntil > 0 && nowMs >= st.lockedUntil {
				delete(t.states, k)
			}
		}
	}

	st, ok := t.states[key]
	if !ok {
		return false
	}
	if st.lockedUntil > 0 {
		if nowMs >= st.lockedUntil {
			st.lockedUntil = 0
			st.fails = 0
			return false
		}
		return true
	}
	return false
}

// RecordFailure 记录一次认证失败；达到阈值即锁定。
func (t *lockoutTracker) RecordFailure(key string) {
	if t == nil || t.maxFail <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	st, ok := t.states[key]
	if !ok {
		st = &lockState{}
		t.states[key] = st
	}
	if st.lockedUntil > 0 {
		return // 已锁定，无需累加
	}
	st.fails++
	if st.fails >= t.maxFail {
		st.lockedUntil = t.now().UnixMilli() + t.lockDuration.Milliseconds()
	}
}

// Reset 认证成功后清零失败计数。
func (t *lockoutTracker) Reset(key string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.states, key)
}

// RateLimiter 全量限流策略集合（§8.3）：
//   - 认证 5/min/IP：/auth/bootstrap、/auth/device-challenge、/auth/device
//   - 同步 120/min/token：/sync、/sync/push、/groups/:gid/keys、/keys/mine
//   - 心跳 30/min/token：/auth/heartbeat
//   - Admin 30/min/token：/admin/*
//   - SSE 每 token ≤4 条：/sync/stream（连接计数由 SSE handler 单独管理）
//   - 认证防暴破：连续 10 次失败锁 IP 10 分钟
type RateLimiter struct {
	auth     *fixedWindowLimiter
	sync     *fixedWindowLimiter
	heart    *fixedWindowLimiter
	admin    *fixedWindowLimiter
	lockout  *lockoutTracker
	now      func() time.Time
}

// RateConfig 限流参数（来自 server.Config，§12.2）。
type RateConfig struct {
	Auth       int
	Sync       int
	Heartbeat  int
	Admin      int
	MaxFail    int // 认证防暴破阈值（10）
	LockoutFor time.Duration
}

// NewRateLimiter 构造限流器。
func NewRateLimiter(cfg RateConfig, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{
		auth:    newFixedWindowLimiter(cfg.Auth, time.Minute, now),
		sync:    newFixedWindowLimiter(cfg.Sync, time.Minute, now),
		heart:   newFixedWindowLimiter(cfg.Heartbeat, time.Minute, now),
		admin:   newFixedWindowLimiter(cfg.Admin, time.Minute, now),
		lockout: newLockoutTracker(cfg.MaxFail, cfg.LockoutFor, now),
		now:     now,
	}
}

// LimitCategory 限流类别（§8.3 限流策略表）。
type LimitCategory int

const (
	// LimitAuth 认证接口 5/min/IP（bootstrap/device-challenge/device）。
	LimitAuth LimitCategory = iota
	// LimitSync 同步接口 120/min/token。
	LimitSync
	// LimitHeartbeat 心跳接口 30/min/token。
	LimitHeartbeat
	// LimitAdmin Admin 接口 30/min/token。
	LimitAdmin
)

// Middleware 返回限流 HTTP 中间件（超限 42901）。
// keyFn 决定限流维度：按 IP 传 middleware.ClientIP，按 token 传提取函数（§8.3）。
func (rl *RateLimiter) Middleware(cat LimitCategory, keyFn func(r *http.Request) string) func(http.Handler) http.Handler {
	if rl == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := ""
			if keyFn != nil {
				key = keyFn(r)
			}
			if !rl.allow(cat, key) {
				w.Header().Set("Retry-After", "60")
				writeErr(w, 42901, "触发限流")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (rl *RateLimiter) allow(cat LimitCategory, key string) bool {
	var l *fixedWindowLimiter
	switch cat {
	case LimitAuth:
		l = rl.auth
	case LimitSync:
		l = rl.sync
	case LimitHeartbeat:
		l = rl.heart
	case LimitAdmin:
		l = rl.admin
	}
	return l.Allow(key)
}

// ClientIP 提取请求来源 IP（§8.2：中间件更新 devices.last_ip 用；不信任 X-Forwarded-For，取 socket 地址）。
func ClientIP(r *http.Request) string {
	host := r.RemoteAddr
	// 去掉端口
	for i := len(host) - 1; i >= 0; i-- {
		if host[i] == ':' {
			return host[:i]
		}
	}
	return host
}
