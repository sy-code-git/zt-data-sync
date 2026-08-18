// Package middleware HTTP 中间件（§6.1 server/middleware）。
// Bearer 认证、限流（§8.3）、安全头、审计写入。
package middleware

import (
	"net/http"
)

// SecurityHeaders 安全响应头中间件（§8.3 传输安全）。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}
