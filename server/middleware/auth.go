package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"passbook/internal/proto"
	"passbook/server/authn"
	"passbook/server/store"
)

// logMiddleware 中间件内部日志（可注入：测试可替换为 discard；默认标准库 slog）。
var logMiddleware = slog.Default()

// ctxKey context 键（避免字符串碰撞）。
type ctxKey int

const (
	ctxKeyDevice ctxKey = iota
	ctxKeyUser
)

// AuthContext 认证中间件注入的请求上下文。
type AuthContext struct {
	Device *store.Device
	User   *store.User
}

// WithAuth 从 context 取认证信息。
func WithAuth(ctx context.Context) (*AuthContext, bool) {
	a, ok := ctx.Value(ctxKeyDevice).(*AuthContext)
	return a, ok
}

// RequireAuth Bearer 认证中间件（§8.2）：
//   - Authorization: Bearer <token>
//   - token → SM3 哈希 → devices.token_hash 匹配 → 设备 active → 用户 active
//   - 注入 device_id / user_id / role 到 context
//   - 失败：40101（token 无效/过期/设备禁用）/ 40301（用户吊销）
func RequireAuth(s store.Store, hashToken func(string) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, ok := bearerToken(r)
			if !ok || token == "" {
				writeErr(w, proto.ErrUnauthorized, "缺少 Bearer token")
				return
			}
			dev, err := s.GetDeviceByTokenHash(hashToken(token))
			if err != nil {
				writeErr(w, proto.ErrUnauthorized, "token 无效")
				return
			}
			if dev.Status != store.DeviceActive {
				writeErr(w, proto.ErrUnauthorized, "设备已禁用")
				return
			}
			u, err := s.GetUserByID(dev.UserID)
			if err != nil {
				writeErr(w, proto.ErrUnauthorized, "token 无效")
				return
			}
			if u.Status != store.StatusActive {
				writeErr(w, proto.ErrUserRevoked, "用户已吊销")
				return
			}
			// 中间件更新 last_ip（§8.2：每次带 token 的请求）
			// 失败不阻断请求（辅助信息），但记录日志（A2：错误可见）
			if err := s.WithTx(r.Context(), func(tx store.Tx) error {
				return tx.UpdateDeviceIP(dev.ID, ClientIP(r))
			}); err != nil {
				logMiddleware.Warn("更新 last_ip 失败", "device_id", dev.ID, "err", err)
			}

			ctx := context.WithValue(r.Context(), ctxKeyDevice, &AuthContext{Device: dev, User: u})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireAdmin admin 权限中间件（§6.2 admin 接口；40303）。
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ac, ok := WithAuth(r.Context())
		if !ok {
			writeErr(w, proto.ErrUnauthorized, "未认证")
			return
		}
		if ac.User.Role != store.RoleAdmin {
			writeErr(w, proto.ErrNotAdmin, "需要 admin 权限")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// bearerToken 解析 Authorization: Bearer <token>。
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(h, "Bearer ")), true
}

// writeErr 写统一错误响应（§13 错误码 → HTTP 状态）。
func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(proto.HTTPStatus(code))
	_ = json.NewEncoder(w).Encode(proto.ErrorBody{Code: code, Message: msg})
}

// IsErrCode 判断错误是否携带指定错误码。
func IsErrCode(err error, code int) bool {
	var ae *authn.Error
	if errors.As(err, &ae) {
		return ae.Code == code
	}
	return false
}
