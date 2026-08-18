// Package api HTTP 路由与 handler（§6.1 server/api）。
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"passbook/internal/proto"
	"passbook/server/authn"
	"passbook/server/middleware"
	"passbook/server/store"
	"passbook/server/sync"
)

// Server API 服务器（组装路由与依赖）。
type Server struct {
	store     store.Store
	authn     *authn.Service
	sync      *sync.Service
	hub       *sync.Hub
	limiter   *middleware.RateLimiter
	audit     *middleware.Audit
	regSecret []byte              // PB_REG_SECRET（§4.4 attestation 校验）
	cors      map[string]struct{} // 跨域来源白名单（§9.3）
	slots     *streamSlots        // SSE 每 token 连接数（§8.3 ≤4，实例级防多 Server 串扰）
	reconnect *streamReconnect    // SSE 2s 重连防护（§8.3）
	now       func() time.Time
}

// Options 构造参数。
type Options struct {
	Store     store.Store
	Authn     *authn.Service
	Sync      *sync.Service
	Hub       *sync.Hub
	Limiter   *middleware.RateLimiter
	Audit     *middleware.Audit
	RegSecret []byte
	Now       func() time.Time
	// CORSOrigins 允许跨域访问的来源白名单（§9.3；空 = 同源部署，不放宽）
	CORSOrigins map[string]struct{}
}

// New 构造 API 服务器。
func New(o *Options) *Server {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Server{
		store: o.Store, authn: o.Authn, sync: o.Sync, hub: o.Hub,
		limiter: o.Limiter, audit: o.Audit, regSecret: o.RegSecret,
		cors: o.CORSOrigins, now: o.Now,
		slots:     &streamSlots{count: map[string]int{}},
		reconnect: &streamReconnect{lastEnd: map[string]time.Time{}},
	}
}

// Router 构建 HTTP 路由（§6.2 API 一览）。
// 非 SSE 路由挂 10s context 超时（§14.1 工程规范 #9）；SSE 长连接单独组不挂。
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.SecurityHeaders)
	if len(s.cors) > 0 {
		r.Use(middleware.CORS(s.cors))
	}

	// ---- 无认证（Timeout + 认证限流 5/min/IP，§8.3） ----
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(10 * time.Second))
		r.Use(s.limiter.Middleware(middleware.LimitAuth, middleware.ClientIP))
		r.Post("/auth/bootstrap", s.handleBootstrap)
		r.Post("/auth/device-challenge", s.handleDeviceChallenge)
		r.Post("/auth/device", s.handleDeviceRegister)
	})

	auth := middleware.RequireAuth(s.store, authn.HashToken)

	// ---- 需认证（Timeout + auth）----
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(10 * time.Second))
		r.Use(auth)
		// 同步类限流 120/min/token
		r.Group(func(r chi.Router) {
			r.Use(s.limiter.Middleware(middleware.LimitSync, tokenKey))
			r.Post("/auth/refresh", s.handleRefresh)
			r.Get("/sync", s.handleSync)
			r.Post("/sync/push", s.handlePush)
			r.Get("/keys/mine", s.handleKeysMine)
			r.Post("/groups/{gid}/keys", s.handleUploadKeys)
			r.Get("/users", s.handleListUsers)
		})
		// 心跳限流 30/min/token（§8.3 独立）
		r.Group(func(r chi.Router) {
			r.Use(s.limiter.Middleware(middleware.LimitHeartbeat, tokenKey))
			r.Post("/auth/heartbeat", s.handleHeartbeat)
		})
	})

	// ---- SSE 长连接（auth，无 Timeout；连接数上限由 handler 管理） ----
	r.Group(func(r chi.Router) {
		r.Use(auth)
		r.Get("/sync/stream", s.handleStream)
	})

	// ---- admin（Timeout + admin 限流 30/min/token，§8.3） ----
	r.Group(func(r chi.Router) {
		r.Use(middleware.Timeout(10 * time.Second))
		r.Use(auth)
		r.Use(middleware.RequireAdmin)
		r.Use(s.limiter.Middleware(middleware.LimitAdmin, tokenKey))

		r.Post("/admin/users", s.handleAdminCreateUser)
		r.Post("/admin/users/{uid}/revoke", s.handleAdminRevoke)
		r.Post("/admin/users/{uid}/keyfile-reset", s.handleAdminKeyfileReset)
		r.Post("/admin/groups", s.handleAdminCreateGroup)
		r.Get("/admin/groups", s.handleAdminListGroups)
		r.Put("/admin/groups/{gid}/members", s.handleAdminAddMember)
		r.Get("/admin/groups/{gid}/members", s.handleAdminGroupMembers)
		r.Post("/admin/groups/{gid}/rekey", s.handleAdminRekey)
		r.Post("/admin/groups/{gid}/archive", s.handleAdminArchive)
		r.Post("/admin/groups/{gid}/unarchive", s.handleAdminUnarchive)
		r.Delete("/admin/groups/{gid}/members/{uid}", s.handleAdminRemoveMember)
		r.Get("/admin/devices", s.handleAdminDevices)
		r.Post("/admin/devices/{did}/disable", s.handleAdminDisableDevice)
		r.Get("/admin/audit", s.handleAdminAudit)
	})

	// 健康探针
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})

	return r
}

// tokenKey 限流 key：取认证 context 的设备 id（§8.3 按 token 维度）。
func tokenKey(r *http.Request) string {
	ac, ok := middleware.WithAuth(r.Context())
	if !ok {
		return ""
	}
	return ac.Device.ID
}

// ---- 工具 ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, proto.HTTPStatus(code), proto.ErrorBody{Code: code, Message: msg})
}

func writeOK(w http.ResponseWriter, v any) {
	writeJSON(w, http.StatusOK, v)
}

// handleErr 统一业务错误处理：sync.ErrCode / authn.Error → 错误码，其他 → 50001。
// authn 接口（bootstrap/device-challenge/device）返回 authn.Error，
// sync.CodeOf 只识别 sync.ErrCode，须再尝试 authn.CodeOf，否则错误码被误映射为 50001。
func handleErr(w http.ResponseWriter, err error) {
	code := sync.CodeOf(err)
	if code == proto.ErrInternal {
		if c := authn.CodeOf(err); c != proto.ErrInternal {
			code = c
		}
	}
	writeErr(w, code, err.Error())
}

// userIDFrom 从认证 context 取用户 id。
func userIDFrom(ctx context.Context) string {
	ac, ok := middleware.WithAuth(ctx)
	if !ok {
		return ""
	}
	return ac.User.ID
}

func deviceIDFrom(ctx context.Context) string {
	ac, ok := middleware.WithAuth(ctx)
	if !ok {
		return ""
	}
	return ac.Device.ID
}
