package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"passbook/internal/proto"
)

// handleSync GET /sync?since=N[&group_id=GID]（§6.3）。
func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	since, err := strconv.ParseInt(q.Get("since"), 10, 64)
	if err != nil {
		writeErr(w, proto.ErrBadRequest, "since 参数非法")
		return
	}
	groupID := q.Get("group_id")
	keyVersions := parseKeyVersions(r.Header.Get("X-Key-Versions"))

	resp, err := s.sync.Pull(r.Context(), userIDFrom(r.Context()), since, groupID, keyVersions)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeOK(w, resp)
}

// parseKeyVersions 解析 X-Key-Versions: gid:kv,gid2:kv2（§6.3）。
func parseKeyVersions(h string) map[string]int {
	out := map[string]int{}
	for _, part := range strings.Split(h, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			continue
		}
		if v, err := strconv.Atoi(kv[1]); err == nil {
			out[kv[0]] = v
		}
	}
	return out
}

// handlePush POST /sync/push（§6.3）。
func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	var req proto.PushRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	resp, err := s.sync.Push(r.Context(), userIDFrom(r.Context()), deviceIDFrom(r.Context()), &req)
	if err != nil {
		handleErr(w, err)
		return
	}
	// 审计：push 动作（§5.2 action 清单）
	s.audit.Record(r, "push", "", "")
	writeOK(w, resp)
}

// handleStream GET /sync/stream SSE 长连接（§6.3）。
// 连接数上限：每 token ≤4 条（§8.3）；断权联动：revoke/disable 时 hub.DisconnectUser 关闭。
func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	deviceID := deviceIDFrom(r.Context())
	userID := userIDFrom(r.Context())

	// §8.3 重连防护：同一 token 断开后 2s 内不允许重连（防抖动风暴）
	if !s.allowStreamReconnect(deviceID) {
		w.Header().Set("Retry-After", "2")
		writeErr(w, proto.ErrRateLimited, "SSE 重连过于频繁，请稍后重试")
		return
	}
	if !s.acquireStreamSlot(deviceID) {
		writeErr(w, proto.ErrRateLimited, "SSE 连接数超限")
		return
	}
	defer s.releaseStreamSlot(deviceID)
	defer s.markStreamDisconnect(deviceID)

	ch, cancel := s.hub.Subscribe(userID)
	defer cancel()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	// 立即发送初始注释确认连接
	_, _ = fmt.Fprintf(w, ": connected\n\n")
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second) // SSE keepalive（§6.3）
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case _, open := <-ch:
			if !open {
				return // 被断权关闭（§6.3 断权联动）
			}
			serverSeq, err := s.store.GetServerSeq()
			if err != nil {
				continue
			}
			// 通知仅含 seq 元数据，不含密文/信封（§6.4 硬规则 #8）
			_, _ = fmt.Fprintf(w, "event: change\ndata: {\"server_seq\":%d}\n\n", serverSeq)
			flusher.Flush()
		case <-ticker.C:
			_, _ = fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// streamSlots 每 token（device）SSE 连接数（§8.3 ≤4）。
type streamSlots struct {
	mu    sync.Mutex
	count map[string]int
}

const maxStreamPerToken = 4

func (s *Server) acquireStreamSlot(deviceID string) bool {
	slots := s.slots
	slots.mu.Lock()
	defer slots.mu.Unlock()
	if slots.count[deviceID] >= maxStreamPerToken {
		return false
	}
	slots.count[deviceID]++
	return true
}

func (s *Server) releaseStreamSlot(deviceID string) {
	slots := s.slots
	slots.mu.Lock()
	defer slots.mu.Unlock()
	if slots.count[deviceID] <= 1 {
		delete(slots.count, deviceID)
		return
	}
	slots.count[deviceID]--
}

// handleKeysMine GET /keys/mine（§6.2：拉取我名下的全部信封）。
func (s *Server) handleKeysMine(w http.ResponseWriter, r *http.Request) {
	envs, err := s.store.GetUserEnvelopes(userIDFrom(r.Context()))
	if err != nil {
		handleErr(w, err)
		return
	}
	out := make([]proto.KeyEnvelopeInfo, 0, len(envs))
	for _, e := range envs {
		out = append(out, proto.KeyEnvelopeInfo{GroupID: e.GroupID, KeyVersion: e.KeyVersion, WrappedDEK: e.WrappedDEK})
	}
	writeOK(w, struct {
		Envelopes []proto.KeyEnvelopeInfo `json:"envelopes"`
	}{Envelopes: out})
}

// handleUploadKeys POST /groups/:gid/keys（入伙追加 / rekey 替换，§6.3）。
func (s *Server) handleUploadKeys(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	var req proto.KeysUploadRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	if err := s.sync.UploadKeys(r.Context(), userIDFrom(r.Context()), gid, &req); err != nil {
		handleErr(w, err)
		return
	}
	action := "wrap"
	if req.KeyVersion > 1 {
		action = "rekey"
	}
	s.audit.Record(r, action, "", "")
	writeOK(w, struct {
		KeyVersion int  `json:"key_version"`
		OK         bool `json:"ok"`
	}{KeyVersion: req.KeyVersion, OK: true})
}

// handleListUsers GET /users（§6.3：返回全部 active 用户，供包裹 DEK 用）。
func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListActiveUsers()
	if err != nil {
		handleErr(w, err)
		return
	}
	out := make([]proto.UserInfo, 0, len(users))
	for i := range users {
		u := &users[i]
		out = append(out, proto.UserInfo{UserID: u.ID, Username: u.Username, Name: u.Name, SM2PublicKey: u.SM2PublicKey, Status: u.Status, Role: u.Role})
	}
	writeOK(w, proto.UsersResponse{Users: out})
}

// ensure sync import used

// streamReconnect 设备 SSE 重连时间防护（§8.3：断开后 2s 内禁止重连）。
type streamReconnect struct {
	mu      sync.Mutex
	lastEnd map[string]time.Time // deviceID → 上次连接结束时间
}

// allowStreamReconnect 检查是否允许建立新连接（距上次断开 ≥2s）。
func (s *Server) allowStreamReconnect(deviceID string) bool {
	rc := s.reconnect
	rc.mu.Lock()
	defer rc.mu.Unlock()
	last, ok := rc.lastEnd[deviceID]
	if !ok {
		return true
	}
	return time.Since(last) >= 2*time.Second
}

// markStreamDisconnect 记录连接结束时间（handler 退出时调用）。
func (s *Server) markStreamDisconnect(deviceID string) {
	rc := s.reconnect
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.lastEnd[deviceID] = time.Now()
}
