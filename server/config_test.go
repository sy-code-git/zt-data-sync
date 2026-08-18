package server

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	t.Setenv("PB_DATA_DIR", "/data") // 固定默认值，避免平台差异

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.DataDir != "/data" {
		t.Errorf("DataDir = %q, want /data", cfg.DataDir)
	}
	if cfg.Addr != ":8443" {
		t.Errorf("Addr = %q, want :8443", cfg.Addr)
	}
	if cfg.TokenTTL != 720*time.Hour {
		t.Errorf("TokenTTL = %v, want 720h", cfg.TokenTTL)
	}
	if cfg.RateAuth != 5 || cfg.RateSync != 120 || cfg.RateHeartbeat != 30 || cfg.RateAdmin != 30 {
		t.Errorf("rate defaults wrong: %+v", cfg)
	}
	if cfg.SSEMaxConn != 4 || cfg.TombstoneDays != 90 || cfg.TombstoneHour != 3 {
		t.Errorf("misc defaults wrong: %+v", cfg)
	}
}

func TestLoadRequiresRegSecret(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "")
	t.Setenv("PB_DATA_DIR", "")
	t.Setenv("PB_ADDR", "")
	t.Setenv("PB_TLS_CERT", "")
	t.Setenv("PB_TLS_KEY", "")

	if _, err := Load(); err == nil {
		t.Fatal("Load() should fail when PB_REG_SECRET is unset")
	}
}

func TestLoadTLSCertKeyPair(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	t.Setenv("PB_TLS_CERT", "/tmp/server.crt")
	// TLS_KEY 未设置 → 报错
	if _, err := Load(); err == nil {
		t.Fatal("Load() should fail when only PB_TLS_CERT is set")
	}
}

func TestLoadRejectsInvalidEnv(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")

	t.Setenv("PB_RATE_AUTH", "abc") // 非法整数值 → 报错，不静默回退
	if _, err := Load(); err == nil {
		t.Fatal("Load() should fail on invalid int env value")
	}

	t.Setenv("PB_RATE_AUTH", "5")
	t.Setenv("PB_TOKEN_TTL", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Fatal("Load() should fail on invalid duration env value")
	}
}

func TestLoadAcceptCustomEnv(t *testing.T) {
	t.Setenv("PB_REG_SECRET", "test-secret")
	t.Setenv("PB_ADDR", ":9443")
	t.Setenv("PB_RATE_SYNC", "200")
	t.Setenv("PB_TOKEN_TTL", "48h")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Addr != ":9443" {
		t.Errorf("Addr = %q, want :9443", cfg.Addr)
	}
	if cfg.RateSync != 200 {
		t.Errorf("RateSync = %d, want 200", cfg.RateSync)
	}
	if cfg.TokenTTL != 48*time.Hour {
		t.Errorf("TokenTTL = %v, want 48h", cfg.TokenTTL)
	}
}
