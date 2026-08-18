package sync

import (
	"context"
	"log"
	"time"

	"passbook/server/store"
)

// TombstoneCleaner 墓碑清理（§7.4：每天 03:00 UTC 清理 90 天前的墓碑）。
type TombstoneCleaner struct {
	store store.Store
	days  int
}

// NewTombstoneCleaner 构造清理器。
func NewTombstoneCleaner(s store.Store, days int) *TombstoneCleaner {
	if days <= 0 {
		days = TombstoneCleanDays
	}
	return &TombstoneCleaner{store: s, days: days}
}

// CleanOnce 执行一次清理（幂等；单个事务，不影响正常同步）。
func (c *TombstoneCleaner) CleanOnce(ctx context.Context, now time.Time) error {
	cutoff := now.Add(-time.Duration(c.days) * 24 * time.Hour).Unix()
	return c.store.WithTx(ctx, func(tx store.Tx) error {
		return tx.DeleteOldTombstones(cutoff)
	})
}

// Run 后台循环：每 24h 在指定 UTC 小时执行一次（默认 03:00，§12.2）。
// ctx 取消即退出。
func (c *TombstoneCleaner) Run(ctx context.Context, cleanHour int) {
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
			if err := c.CleanOnce(ctx, time.Now().UTC()); err != nil {
				log.Printf("墓碑清理失败: %v", err) // 失败不阻塞，下一周期重试
			}
		}
	}
}
