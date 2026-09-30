package sync

import (
	"context"
	"log"
	"time"

	"passbook/server/store"
)

// auditRetentionDefault 审计日志默认保留期（§3.6：如 180 天）。
const auditRetentionDefault = 180

// AuditCleaner 审计日志保留期清理（§12.2 PB_AUDIT_RETENTION，默认 180 天，0 = 永久）。
// 并入每日清理趟（与墓碑清理同刻，§12.2 PB_TOMBSTONE_CLEAN_HOUR）。
// audit_log append-only 的唯一删除例外：仅此处按保留期批量删除（§5.2）。
type AuditCleaner struct {
	store store.Store
	days  int
}

// NewAuditCleaner 构造清理器。days <= 0 用默认 180 天；days == 0 语义（永久保留）
// 由调用方（main）决定不启动清理器，此处 days<=0 一律回退默认值。
func NewAuditCleaner(s store.Store, days int) *AuditCleaner {
	if days < 0 {
		days = auditRetentionDefault
	}
	return &AuditCleaner{store: s, days: days}
}

// Days 保留天数（main 据此判断 0=永久时不启动）。
func (c *AuditCleaner) Days() int { return c.days }

// CleanOnce 执行一次清理（幂等；返回删除条数）。
func (c *AuditCleaner) CleanOnce(ctx context.Context, now time.Time) (int64, error) {
	cutoff := now.Add(-time.Duration(c.days) * 24 * time.Hour).Unix()
	var n int64
	err := c.store.WithTx(ctx, func(tx store.Tx) error {
		var err error
		n, err = tx.DeleteOldAudit(cutoff)
		return err
	})
	return n, err
}

// Run 后台循环：每 24h 在指定 UTC 小时执行一次（与墓碑清理同刻，默认 03:00）。
// ctx 取消即退出。
func (c *AuditCleaner) Run(ctx context.Context, cleanHour int) {
	if cleanHour < 0 || cleanHour > 23 {
		cleanHour = 3
	}
	for {
		now := time.Now().UTC()
		next := time.Date(now.Year(), now.Month(), now.Day(), cleanHour, 0, 0, 0, time.UTC)
		if !next.After(now) {
			next = next.Add(24 * time.Hour)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			n, err := c.CleanOnce(ctx, time.Now().UTC())
			if err != nil {
				log.Printf("审计日志保留期清理失败: %v", err) // 失败不阻塞，下一周期重试
			} else if n > 0 {
				log.Printf("审计日志保留期清理: 删除 %d 条（保留 %d 天）", n, c.days)
			}
		}
	}
}
