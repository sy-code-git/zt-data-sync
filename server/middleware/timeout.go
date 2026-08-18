package middleware

import (
	"context"
	"net/http"
	"time"
)

// Timeout handler context 超时中间件（§14.1 工程规范 #9：默认 10s）。
// 超时后 handler 内的 DB/网络调用随 ctx 取消而失败，返回 50001。
// 注意：SSE 长连接等流式接口不应挂本中间件（由调用方按路由取舍）。
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
