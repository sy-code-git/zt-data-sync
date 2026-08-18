package main

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// cmdSSEListen 连接 SSE 长连接并打印事件时间戳（§6.3 验收/诊断用）。
// 用法: pbcli sse-listen [--timeout 15s]（需先设置 PB_KEYFILE/PB_SERVER/PB_PASSWORD/PB_CA/PB_DATA）
// 输出: SSE_CONNECTED t=<unixnano> / CHANGE t=<unixnano>，供测量推送延迟。
func cmdSSEListen(args []string) error {
	fs := flag.NewFlagSet("sse-listen", flag.ExitOnError)
	timeout := fs.Duration("timeout", 15*time.Second, "监听时长（超时自动退出）")
	_ = fs.Parse(args)

	if err := ensureUnlocked(); err != nil {
		return err
	}
	token := app.hc.Token()
	server := strings.TrimSuffix(os.Getenv("PB_SERVER"), "/")

	client := &http.Client{Timeout: 0} // 长连接无超时
	if ca := os.Getenv("PB_CA"); ca != "" {
		caPEM, err := os.ReadFile(ca) // #nosec G304 G703 -- 本地 CA 路径，来自本机环境变量，CLI 诊断工具非不可信输入
		if err != nil {
			return fmt.Errorf("读取 CA 失败: %w", err)
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caPEM)
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}
	}

	req, err := http.NewRequest(http.MethodGet, server+"/sync/stream", nil) // #nosec G704 -- server 来自本机环境变量 PB_SERVER，CLI 诊断工具非不可信输入
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req) // #nosec G704 -- 同上，SSE 长连接目标由本机配置指定
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE 服务端返回 %d", resp.StatusCode)
	}
	fmt.Printf("SSE_CONNECTED t=%d\n", time.Now().UnixNano())

	done := time.After(*timeout)
	reader := bufio.NewReader(resp.Body)
	lines := make(chan string)
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				close(lines)
				return
			}
			lines <- line
		}
	}()

	for {
		select {
		case <-done:
			return nil
		case line, ok := <-lines:
			if !ok {
				return fmt.Errorf("SSE 连接断开")
			}
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "event: change") {
				fmt.Printf("CHANGE t=%d\n", time.Now().UnixNano())
			}
		}
	}
}
