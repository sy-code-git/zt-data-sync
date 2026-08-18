// Package api HTTP 服务端客户端（同步引擎的 ServerClient 真实实现，§6.2 调用）。
package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"passbook/internal/proto"
)

// HTTPClient 服务端 REST 客户端。
type HTTPClient struct {
	serverURL string
	mu        sync.RWMutex // 保护 token（SSE 重连 / syncOnce / token 刷新可能并发访问）
	token     string
	client    *http.Client
}

// NewHTTPClient 构造客户端。
// 同步请求设 30s 超时（P2：防无限挂起；SSE 长连接由 sseClient 单独管理，不设超时）。
func NewHTTPClient(serverURL, token string) *HTTPClient {
	return &HTTPClient{
		serverURL: strings.TrimSuffix(serverURL, "/"),
		token:     token,
		client:    &http.Client{Timeout: 30 * time.Second, Transport: newTransport()},
	}
}

// newTransport 构造 HTTP Transport（地址填什么优先什么 + 合理握手/空闲超时）。
// 按连接目标智能选择网络族：
//   - 填 IPv4 地址 → tcp4（显式走 IPv4，避免 DNS AAAA 记录干扰导致挂起）
//   - 填 IPv6 地址 → tcp6
//   - 填域名 → 默认 tcp，按系统 DNS 解析（可能 IPv4 或 IPv6）
func newTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, _, err := net.SplitHostPort(addr); err == nil {
				if ip := net.ParseIP(host); ip != nil {
					if ip.To4() != nil {
						network = "tcp4" // 填的是 IPv4
					} else {
						network = "tcp6" // 填的是 IPv6
					}
				}
				// 域名：保持默认 tcp，交给系统 DNS 解析
			}
			return dialer.DialContext(ctx, network, addr)
		},
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   4,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
}

// SSEBaseClient 返回 SSE 长连接用基础客户端：复用同一 Transport（继承 CA pinning
// 与「地址填什么优先什么」智能选路），Timeout=0 长连接不超时。
// 此前 SSE 客户端独立用默认 http.Client，导致自签 CA 场景 TLS 验证失败、SSE 连不上，
// 客户端 UI 显示「离线」（修复 commit 对应 §6.3 在线判定）。
func (c *HTTPClient) SSEBaseClient() *http.Client {
	return &http.Client{Timeout: 0, Transport: c.client.Transport}
}

// NewHTTPClientWithCA 构造客户端并信任指定 CA 证书（§8.3：自签 CA + pinning）。
// caPath 为 PEM 编码的 CA 证书路径；仅该 CA 被加入信任池，其余仍走系统默认验证。
// 供内网自签证书部署的客户端（pbcli/UI 壳）使用。
func NewHTTPClientWithCA(serverURL, token, caPath string) (*HTTPClient, error) {
	c := NewHTTPClient(serverURL, token)
	// #nosec G304 -- CA 证书路径由客户端本地配置提供（pbcli --ca / UI 设置），非不可信输入
	caPEM, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("读取 CA 证书失败: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("CA 证书解析失败（非 PEM 格式）: %s", caPath)
	}
	c.client = &http.Client{
		Timeout: 30 * time.Second,
		Transport: func() *http.Transport {
			tr := newTransport()
			tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13}
			return tr
		}(),
	}
	return c, nil
}

// do 执行请求；header 为附加请求头（可 nil；Authorization/Content-Type 自动设置）。
func (c *HTTPClient) do(method, path string, header map[string]string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, c.serverURL+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token())
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		var eb proto.ErrorBody
		if err := json.Unmarshal(data, &eb); err == nil && eb.Code != 0 {
			return &APIError{Code: eb.Code, Message: eb.Message}
		}
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

// APIError 带错误码的错误。
type APIError struct {
	Code    int
	Message string
}

func (e *APIError) Error() string { return e.Message }

// Pull 增量拉取（§6.3）。
// keyVersions：本地已持有信封版本声明（gid→kv），经 X-Key-Versions 头传给服务端
// 跳过 ≤kv 的信封，减少带宽与解密开销（§6.3）。
func (c *HTTPClient) Pull(since int64, keyVersions map[string]int) (*proto.SyncResponse, error) {
	var resp proto.SyncResponse
	if err := c.do(http.MethodGet, fmt.Sprintf("/sync?since=%d", since), syncHeader(keyVersions), nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// syncHeader 组装 X-Key-Versions 头（gid:kv,逗号分隔；空声明省略头）。
func syncHeader(keyVersions map[string]int) map[string]string {
	if len(keyVersions) == 0 {
		return nil
	}
	parts := make([]string, 0, len(keyVersions))
	for g, kv := range keyVersions {
		parts = append(parts, fmt.Sprintf("%s:%d", g, kv))
	}
	sort.Strings(parts)
	return map[string]string{"X-Key-Versions": strings.Join(parts, ",")}
}

// Push 推送（§6.3）。
func (c *HTTPClient) Push(mutations []proto.Mutation) (*proto.PushResponse, error) {
	var resp proto.PushResponse
	if err := c.do(http.MethodPost, "/sync/push", nil, &proto.PushRequest{Mutations: mutations}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// UploadKeys 上传信封（§6.3）。
func (c *HTTPClient) UploadKeys(groupID string, req *proto.KeysUploadRequest) error {
	return c.do(http.MethodPost, "/groups/"+groupID+"/keys", nil, req, nil)
}

// ListUsers 获取 active 用户（§6.3）。
func (c *HTTPClient) ListUsers() ([]proto.UserInfo, error) {
	var resp proto.UsersResponse
	if err := c.do(http.MethodGet, "/users", nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Users, nil
}

// Health 无鉴权健康探活（§9.2 首次引导验证服务端地址可达；GET /healthz）。
func (c *HTTPClient) Health() error {
	req, err := http.NewRequest(http.MethodGet, c.serverURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return nil
}

// Heartbeat 心跳上报（§6.3：更新 hostname/last_seen，维持在线判定）。
func (c *HTTPClient) Heartbeat(hostname string) error {
	return c.do(http.MethodPost, "/auth/heartbeat", nil, &proto.HeartbeatRequest{Hostname: hostname}, nil)
}

// Token 当前设备 token（SSE 重连取最新用，§7.2 token 刷新）。
func (c *HTTPClient) Token() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.token
}

// SetToken 更新 token（刷新成功后由 engine 调用，§7.2）。
func (c *HTTPClient) SetToken(t string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.token = t
}

// RefreshToken 调用 /auth/refresh 换新 token（旧 token 服务端即刻作废，§6.3）。
func (c *HTTPClient) RefreshToken() (string, int64, error) {
	var resp proto.TokenRefreshResponse
	if err := c.do(http.MethodPost, "/auth/refresh", nil, nil, &resp); err != nil {
		return "", 0, err
	}
	c.SetToken(resp.Token)
	return resp.Token, resp.ExpiresIn, nil
}

// IsAuthErr 判断错误是否为认证失败（40101，§13）。
func IsAuthErr(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Code == proto.ErrUnauthorized
	}
	return false
}
