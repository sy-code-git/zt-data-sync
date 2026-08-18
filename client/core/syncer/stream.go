package syncer

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// sseClient SSE 变更推送客户端（§6.3 / §7.2）。
// 收到 event: change 即触发一次增量拉取；断线由调用方按 backoff 重连。
type sseClient struct {
	serverURL string
	token     string
	client    *http.Client
}

func newSSEClient(serverURL, token string, base *http.Client) *sseClient {
	return &sseClient{
		serverURL: strings.TrimSuffix(serverURL, "/"),
		token:     token,
		client:    &http.Client{Timeout: 0, Transport: base.Transport}, // 复用 API Transport：CA pinning + 智能选路；长连接不超时
	}
}

// changeEvent SSE 通知内容（§6.3：仅 seq 元数据与组 ID）。
type changeEvent struct {
	ServerSeq int64 `json:"server_seq"`
}

// Run 阻塞监听 SSE；每收到 change 事件调用 onChange；
// 连接断开返回错误（调用方按 backoff 重连）；ctx 取消返回 nil。
func (s *sseClient) Run(ctx context.Context, onChange func(seq int64)) error {
	url := s.serverURL + "/sync/stream"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, resp.Body)
		return fmt.Errorf("sse: 服务端 %d", resp.StatusCode)
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if ctx.Err() != nil {
				return nil // 主动取消
			}
			return err
		}
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "event: change"):
			// 下一行 data
			dataLine, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			ev := parseChangeEvent(strings.TrimSpace(strings.TrimPrefix(dataLine, "data: ")))
			if ev.ServerSeq > 0 && onChange != nil {
				onChange(ev.ServerSeq)
			}
		case len(line) > 0 && line[0] == ':':
			// 心跳/注释（含 keepalive），忽略
		}
	}
}

// parseChangeEvent 解析 SSE data（容错）。
func parseChangeEvent(data string) changeEvent {
	var ev changeEvent
	// 仅提取 server_seq（JSON 简单解析）
	if i := strings.Index(data, `"server_seq"`); i >= 0 {
		rest := data[i+len(`"server_seq"`):]
		if j := strings.Index(rest, ":"); j >= 0 {
			num := strings.TrimSpace(rest[j+1:])
			num = strings.TrimRight(num, "}")
			var n int64
			if _, err := fmt.Sscanf(num, "%d", &n); err == nil {
				ev.ServerSeq = n
			}
		}
	}
	return ev
}
