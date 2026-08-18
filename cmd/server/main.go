// cmd/server 服务端入口（§6）。一期 1.5 阶段挂接完整 API 路由。
package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"passbook/internal/crypto"
	"passbook/server"
	"passbook/server/api"
	"passbook/server/authn"
	"passbook/server/middleware"
	"passbook/server/store"
	"passbook/server/sync"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("server exited with error: %v", err)
	}
}

func run() error {
	cfg, err := server.Load()
	if err != nil {
		return err
	}

	// §4.4：PB_REG_SECRET 必配——缺失时 admin 建用户/注册凭证校验全部拒绝（fail-closed），启动即警告
	if cfg.RegSecret == "" {
		log.Printf("警告: PB_REG_SECRET 未设置——admin 创建用户与注册凭证校验将全部拒绝（§4.4 必配，与 keytool 共享）")
	}

	// 首启引导 token：未设置则生成随机一次性值并打印（§12.2 用后即毁）
	if cfg.BootstrapCode == "" {
		b, err := crypto.Random(32)
		if err != nil {
			return err
		}
		cfg.BootstrapCode = base64.RawURLEncoding.EncodeToString(b)
		log.Printf("一次性 bootstrap token（完成引导后即失效）: %s", cfg.BootstrapCode)
	}

	// 数据目录
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return err
	}

	st, err := store.Open(cfg.DataDir + "/passbook.db")
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		return err
	}

	hub := sync.NewHub()
	authnSvc := authn.New(st, authn.Options{BootstrapCode: cfg.BootstrapCode})
	syncSvc := sync.New(st, hub, nil)
	limiter := middleware.NewRateLimiter(middleware.RateConfig{
		Auth: cfg.RateAuth, Sync: cfg.RateSync, Heartbeat: cfg.RateHeartbeat,
		Admin: cfg.RateAdmin, MaxFail: 10, LockoutFor: 10 * time.Minute,
	}, nil)
	audit := middleware.NewAudit(st, nil)

	apiSrv := api.New(&api.Options{
		Store: st, Authn: authnSvc, Sync: syncSvc, Hub: hub,
		Limiter: limiter, Audit: audit, RegSecret: []byte(cfg.RegSecret),
		CORSOrigins: cfg.CORSOrigins,
	})

	// 墓碑清理后台任务（§7.4：每天 03:00 UTC）
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cleaner := sync.NewTombstoneCleaner(st, cfg.TombstoneDays)
	go cleaner.Run(ctx, cfg.TombstoneHour)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           apiSrv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      0, // SSE 长连接需要禁用 WriteTimeout（§6.3）
		IdleTimeout:       60 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS13},
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if cfg.TLSCert != "" && cfg.TLSKey != "" {
		log.Printf("passbook server listening on https://%s (TLS 1.3)", cfg.Addr)
		if err := srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	log.Printf("passbook server listening on http://%s (TLS 未配置，仅限内网调试)", cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
