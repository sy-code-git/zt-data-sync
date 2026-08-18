package sync

import (
	"sync"
)

// Hub SSE 变更通知中心（§6.3 GET /sync/stream / §6.4 硬规则 #8）。
// 通知仅含 seq 元数据与组 ID，不含任何密文/信封；数据仍通过 GET /sync 加密拉取。
type Hub struct {
	mu     sync.Mutex
	byUser map[string]map[chan struct{}]struct{} // userID → 订阅 channel 集合
	// byGroup: 组 → 订阅该组的用户（用户可订阅多个组；为简化，通知按用户粒度，
	// 客户端收到通知后拉取自己所有组——§7.2 触发源：SSE 推送事件立即再拉）
}

// NewHub 构造通知中心。
func NewHub() *Hub {
	return &Hub{byUser: map[string]map[chan struct{}]struct{}{}}
}

// Subscribe 注册订阅，返回通知 channel 与取消函数。
// 同一用户可订阅多条（每 token ≤4 条由 api 层校验）。
func (h *Hub) Subscribe(userID string) (<-chan struct{}, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan struct{}, 1)
	if h.byUser[userID] == nil {
		h.byUser[userID] = map[chan struct{}]struct{}{}
	}
	h.byUser[userID][ch] = struct{}{}

	once := sync.Once{}
	cancel := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if set, ok := h.byUser[userID]; ok {
				delete(set, ch)
				if len(set) == 0 {
					delete(h.byUser, userID)
				}
			}
		})
	}
	return ch, cancel
}

// Notify 通知相关组的订阅用户（非阻塞：channel 满则跳过，客户端以拉取为准，§7.5）。
// 全程持锁：与 DisconnectUser 的 close 串行化，避免 send-on-closed panic（P2 竞态）。
func (h *Hub) Notify(groupIDs []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	// 简化：通知所有订阅用户（组过滤由客户端拉取时处理；小团队规模可接受）。
	for _, set := range h.byUser {
		for ch := range set {
			select {
			case ch <- struct{}{}:
			default: // 满则跳过（已有待处理通知）
			}
		}
	}
}

// DisconnectUser 断权联动：关闭用户全部 SSE 连接（revoke/disable 时调用，§6.3）。
// 通过发送关闭信号让订阅端退出；实际关闭由 api 层 handle 响应。
func (h *Hub) DisconnectUser(userID string) {
	h.mu.Lock()
	set := h.byUser[userID]
	delete(h.byUser, userID)
	h.mu.Unlock()

	for ch := range set {
		select {
		case ch <- struct{}{}:
		default:
		}
		close(ch)
	}
}
