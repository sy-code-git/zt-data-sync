package sync

import (
	"context"
	"log"
	"sync"
	"time"

	"passbook/server/store"
)

// offlineThreshold 判定离线的 last_seen 阈值（§6.3：last_seen 60s 阈值内算在线，
// 离线判定放宽到 90s，避免边界抖动导致在线/离线事件反复）。
const offlineThreshold = 90 * time.Second

// offlineSweepInterval 离线监视器扫描周期（60s 一趟）。
const offlineSweepInterval = time.Minute

// DeviceOfflineMonitor 设备离线监视器（§3.6/§5.2 device_offline）：
// 周期扫描 devices 表，last_seen 超过阈值且上次已知状态为在线的设备
// 记一次 device_offline 审计。在线/离线事件去重（内存标记）由本监视器统一负责：
//   - handler 侧 device_register/device_refresh 记 device_online 时不判跃迁
//     （宁可多记上线，不漏记上线——上线信号是安全审计的正向基线）；
//   - 本监视器只对"曾见到在线→现已超时"的设备记一次 offline，记完即从内存标记移除，
//     设备重新上线（device_online 再现）后才会进入下一轮离线判定。
// 服务重启内存标记清零：重启期间本就无人观测，重启后设备要么重新活跃（记 online）、
// 要么静默超时（无 online 记录在先，不记 offline），语义自洽。
type DeviceOfflineMonitor struct {
	store store.Store
	audit AuditRecorder
	now   func() time.Time

	mu       sync.Mutex
	seenOn   map[string]bool // device_id → 上次扫描时是否在线
	swept    bool            // 首趟标记：首趟只建立基线，不判离线（否则启动即对全部历史设备刷 offline）
}

// AuditRecorder 离线事件审计写入（与 middleware.Audit 解耦：后台任务无 HTTP 上下文）。
type AuditRecorder interface {
	RecordActor(ctx context.Context, deviceID, userID, action, entryID, ip, deviceName, hostname, detail string)
}

// NewDeviceOfflineMonitor 构造监视器。audit 可为 nil（关闭离线审计，仅测试用）。
func NewDeviceOfflineMonitor(s store.Store, audit AuditRecorder, now func() time.Time) *DeviceOfflineMonitor {
	if now == nil {
		now = time.Now
	}
	return &DeviceOfflineMonitor{store: s, audit: audit, now: now, seenOn: map[string]bool{}}
}

// SweepOnce 执行一次扫描（幂等；返回本轮记了几个 device_offline）。
// 单个设备查询失败跳过（下一趟重试），扫描整体失败返回 error。
func (m *DeviceOfflineMonitor) SweepOnce(ctx context.Context) (int, error) {
	devices, err := m.store.ListAllDevices()
	if err != nil {
		return 0, err
	}
	cutoff := m.now().Add(-offlineThreshold).Unix()

	m.mu.Lock()
	defer m.mu.Unlock()

	recorded := 0
	for i := range devices {
		d := &devices[i]
		online := d.LastSeen > 0 && d.LastSeen >= cutoff
		wasOn, tracked := m.seenOn[d.ID]
		// 首趟只建立基线不判离线；后续趟：上一轮在线、本轮超时 → 记一次 offline 并移除标记
		if m.swept && tracked && wasOn && !online {
			delete(m.seenOn, d.ID)
			if d.Status != store.DeviceActive {
				continue // 禁用设备不记离线（下线由 disable_device 审计承载）
			}
			if m.audit != nil {
				m.audit.RecordActor(ctx, d.ID, d.UserID, "device_offline", "", d.LastIP, d.Name, d.Hostname, "")
			}
			recorded++
			continue
		}
		// 在线/维持离线：更新标记（离线设备保持"未标记"，重新上线时 handler 的
		// device_online 会刷新 last_seen → 下一趟扫描重新 seenOn）
		if online {
			m.seenOn[d.ID] = true
		} else {
			delete(m.seenOn, d.ID)
		}
	}
	m.swept = true
	return recorded, nil
}

// Run 后台循环：每 60s 扫描一趟；ctx 取消即退出。失败记日志不退出（下一趟重试）。
func (m *DeviceOfflineMonitor) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(offlineSweepInterval):
			if _, err := m.SweepOnce(ctx); err != nil {
				log.Printf("设备离线扫描失败: %v", err)
			}
		}
	}
}
