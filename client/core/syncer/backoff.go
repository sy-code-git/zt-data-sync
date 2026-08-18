package syncer

import "time"

// backoff 指数退避（§7.2：1s→2s→…→30s 封顶；连续失败 10 次切换轮询模式）。
type backoff struct {
	base    time.Duration
	max     time.Duration
	current time.Duration
	fails   int
}

func newBackoff() *backoff {
	return &backoff{base: time.Second, max: 30 * time.Second}
}

// Next 返回下一次重试延迟并递增退避。
func (b *backoff) Next() time.Duration {
	if b.current == 0 {
		b.current = b.base
	} else {
		b.current *= 2
		if b.current > b.max {
			b.current = b.max
		}
	}
	b.fails++
	return b.current
}

// Reset 成功后复位。
func (b *backoff) Reset() {
	b.current = 0
	b.fails = 0
}

// Fails 连续失败次数。
func (b *backoff) Fails() int { return b.fails }

// 轮询策略（§7.2）：网络健康 5s、降级/失败 30s；连续失败 10 次切轮询（30s + 每 5min 尝试恢复 SSE）。
const (
	pollHealthy   = 5 * time.Second
	pollDegraded  = 30 * time.Second
	maxSSEFails   = 10
	recoverSSEInt = 5 * time.Minute
)
