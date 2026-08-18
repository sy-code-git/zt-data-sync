package middleware

import (
	"net/http"
	"time"

	"passbook/server/store"
)

// Audit 审计中间件：请求完成后写审计日志（§5.2 audit_log）。
// 仅记录元数据（动作/设备/用户/IP），不记任何明文/密文。
// pull 不逐次记审计（回退轮询会爆量，§5.2 注释）；仅异常路径在 handler 内记 pull_denied。
type Audit struct {
	store store.Store
	now   func() time.Time
}

// NewAudit 构造审计写入器。
func NewAudit(s store.Store, now func() time.Time) *Audit {
	if now == nil {
		now = time.Now
	}
	return &Audit{store: s, now: now}
}

// Record 写一条审计（供 handler 在业务动作后调用）。
// 写失败不阻断业务，但记录 Error 日志（A3：安全日志完整性可见）。
func (a *Audit) Record(r *http.Request, action string, entryID, detail string) {
	ac, ok := WithAuth(r.Context())
	if !ok {
		return
	}
	if err := a.store.WithTx(r.Context(), func(tx store.Tx) error {
		return tx.Audit(&store.AuditEvent{
			TS:         a.now().Unix(),
			DeviceID:   ac.Device.ID,
			UserID:     ac.User.ID,
			Action:     action,
			EntryID:    entryID,
			IP:         ClientIP(r),
			DeviceName: ac.Device.Name,
			Hostname:   ac.Device.Hostname,
			Detail:     detail,
		})
	}); err != nil {
		logMiddleware.Error("写审计日志失败", "action", action, "user_id", ac.User.ID, "err", err)
	}
}
