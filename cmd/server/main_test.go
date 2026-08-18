package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"passbook/server/api"
	"passbook/server/authn"
	"passbook/server/middleware"
	"passbook/server/store"
	"passbook/server/sync"
)

// newTestRouter 构造完整 API 路由（内存 store），供冒烟测试。
func newTestRouter(t *testing.T) http.Handler {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	hub := sync.NewHub()
	svc := api.New(&api.Options{
		Store:     st,
		Authn:     authn.New(st, authn.Options{BootstrapCode: "boot"}),
		Sync:      sync.New(st, hub, nil),
		Hub:       hub,
		Limiter:   middleware.NewRateLimiter(middleware.RateConfig{Auth: 100, Sync: 1000, Heartbeat: 100, Admin: 1000, MaxFail: 10, LockoutFor: 0}, nil),
		Audit:     middleware.NewAudit(st, nil),
		RegSecret: []byte("reg-secret"),
	})
	return svc.Router()
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "ok") {
		t.Fatalf("body = %q, want contains ok", rec.Body.String())
	}
}

func TestHealthzSecurityHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	newTestRouter(t).ServeHTTP(rec, req)
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("healthz 应带安全头")
	}
}

// 确保 proto 依赖被引用（避免 unused import 误报）
