package middleware

import (
	"context"
	"net/http"
	"time"

	"passbook/server/store"
)

// Audit 审计中间件：请求完成后写审计日志（§5.2 audit_log）。
// 仅记录元数据（动作/设备/用户/IP），不记任何明文/密文。
// pull 不逐次记审计（回退轮询会爆量，§5.2 注释）；pull_denied 为预留 action 名，暂未实现
// （40302 拒绝路径无数据流出、审计价值低且 pull 为高频接口易灌水，决定不记）。
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
	a.RecordActor(context.Background(), ac.Device.ID, ac.User.ID, action, entryID, ClientIP(r), ac.Device.Name, ac.Device.Hostname, detail)
}

// RecordActor 写一条审计（显式指定 actor，不依赖请求认证上下文）。
// 用于两类场景：
//   - 无认证分组的入口接口（/auth/bootstrap、/auth/device、register-request）：
//     它们本身就是签发身份的入口，天然没有认证上下文，此前 Record 全部静默空转；
//   - 后台任务（离线监视器）：无 *http.Request，只有设备/用户库内信息。
// ip 参数为事件来源 IP（登录/上线事件记，§5.2）；写失败不阻断业务但记录 Error 日志。
func (a *Audit) RecordActor(ctx context.Context, deviceID, userID, action, entryID, ip, deviceName, hostname, detail string) {
	if action == "" {
		return
	}
	if err := a.store.WithTx(ctx, func(tx store.Tx) error {
		return tx.Audit(&store.AuditEvent{
			TS:         a.now().Unix(),
			DeviceID:   deviceID,
			UserID:     userID,
			Action:     action,
			EntryID:    entryID,
			IP:         ip,
			DeviceName: deviceName,
			Hostname:   hostname,
			Detail:     detail,
		})
	}); err != nil {
		logMiddleware.Error("写审计日志失败", "action", action, "user_id", userID, "err", err)
	}
}
