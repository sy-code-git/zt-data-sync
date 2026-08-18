package middleware

import (
	"net/http"
	"strings"
)

// CORS 跨域支持中间件（§9.3：管理端/UI 独立部署时跨域访问 API）。
// 白名单模式：仅允许配置的来源（PB_CORS_ORIGINS）；未配置 → 同源部署，不返回任何 CORS 头
// （浏览器同源请求不受影响；跨域来源被拦截——默认不放宽，防 CSRF）。
// 预检 OPTIONS：白名单来源返回 204，其余走原路由（不处理，防 CSRF）。
func CORS(allowed map[string]struct{}) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if _, ok := allowed[origin]; ok {
					h := w.Header()
					h.Set("Access-Control-Allow-Origin", origin)
					h.Set("Vary", "Origin")
					h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Key-Versions")
					h.Set("Access-Control-Max-Age", "600")
					if r.Method == http.MethodOptions {
						w.WriteHeader(http.StatusNoContent)
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ParseOrigins 解析逗号分隔的来源白名单（空串 → 空集合 = 同源模式）。
func ParseOrigins(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, o := range strings.Split(s, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out[o] = struct{}{}
		}
	}
	return out
}
