package api

import (
	"encoding/json"
	"net/http"
	"passbook/server/store"

	"passbook/internal/proto"
	"passbook/server/authn"
	"passbook/server/middleware"
)

// ---- 认证接口（§6.3） ----

func (s *Server) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	var req proto.BootstrapRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	resp, err := s.authn.Bootstrap(r.Context(), &req)
	if err != nil {
		handleErr(w, err)
		return
	}
	// 无认证分组（本接口即身份签发入口）：显式 actor 记审计（§5.2 bootstrap）
	s.audit.RecordActor(r.Context(), resp.DeviceID, resp.UserID, "bootstrap", "", middleware.ClientIP(r), req.DeviceName, "", "")
	writeOK(w, resp)
}

func (s *Server) handleDeviceChallenge(w http.ResponseWriter, r *http.Request) {
	var req proto.DeviceChallengeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	resp, err := s.authn.CreateChallenge(r.Context(), req.Username)
	if err != nil {
		handleErr(w, err)
		return
	}
	writeOK(w, resp)
}

func (s *Server) handleDeviceRegister(w http.ResponseWriter, r *http.Request) {
	var req proto.DeviceRegisterRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	resp, err := s.authn.RegisterDevice(r.Context(), &req)
	if err != nil {
		handleErr(w, err)
		return
	}
	// 无认证分组（本接口即身份签发入口）：显式 actor 记审计（§5.2 device_register）
	s.audit.RecordActor(r.Context(), resp.DeviceID, s.userIDByUsername(req.Username), "device_register", "", middleware.ClientIP(r), req.DeviceName, req.Hostname, "")
	writeOK(w, resp)
}

// handleRefresh POST /auth/refresh（§6.3：旧 token 即刻作废，返回新 token）。
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	deviceID := deviceIDFrom(r.Context())
	if deviceID == "" {
		writeErr(w, proto.ErrUnauthorized, "未认证")
		return
	}
	tokenRaw, tokenHash, err := authn.GenerateToken()
	if err != nil {
		handleErr(w, err)
		return
	}
	// 单事务：替换 token_hash（旧 token 即刻作废，§6.3）
	err = s.store.WithTx(r.Context(), func(tx store.Tx) error {
		return tx.RefreshTokenHash(deviceID, tokenHash)
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	s.audit.Record(r, "device_refresh", "", "")
	// token 刷新 = 设备登录类低频事件，记 device_online（§5.2）。
	// 注意：仅 refresh 记，heartbeat 不记——心跳 30s 一次属状态信号而非事件，
	// 写审计会灌水（1 台设备在线 8h ≈ 960 条/天），把真正的安全事件挤出查询窗口；
	// 心跳的活跃语义由 last_seen 更新承载（设备 tab 在线状态/离线监视器据此判定）。
	s.auditRecordOnline(r, "device_refresh")
	writeOK(w, &proto.TokenRefreshResponse{Token: tokenRaw, ExpiresIn: authn.TokenTTL})
}

// handleHeartbeat POST /auth/heartbeat（§6.3：更新 hostname/last_seen）。
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	deviceID := deviceIDFrom(r.Context())
	var req proto.HeartbeatRequest
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req) // 请求体可空，解析失败忽略；限 1MB 防内存 DoS

	err := s.store.WithTx(r.Context(), func(tx store.Tx) error {
		return tx.UpdateDeviceSeen(deviceID, req.Hostname, s.now().Unix())
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	// 心跳不记审计（状态信号非事件）：last_seen 已更新，在线/离线跃迁由
	// sync.DeviceOfflineMonitor 记 device_offline；device_register/refresh 仍记 device_online。
	writeOK(w, &proto.HeartbeatResponse{OK: true})
}

// auditRecordOnline 记一条 device_online（§5.2/§3.6：登录类低频事件——注册/token 刷新）。
// 心跳不记（状态信号非事件，防审计灌水）：活跃语义由 last_seen 更新承载，
// 在线/离线跃迁由 sync.DeviceOfflineMonitor 记 device_offline。
// 在线/离线事件去重由离线监视器统一负责（内存标记：仅"离线→在线"跃迁才落审计），
// handler 只负责"在正确的时机上报活跃信号"。
func (s *Server) auditRecordOnline(r *http.Request, source string) {
	ac, ok := middleware.WithAuth(r.Context())
	if !ok {
		return
	}
	s.audit.RecordActor(r.Context(), ac.Device.ID, ac.User.ID, "device_online", "", middleware.ClientIP(r), ac.Device.Name, ac.Device.Hostname, source)
}

// userIDByUsername 按工号查用户 ID（device_register 审计用；用户必存在，失败返回空）。
func (s *Server) userIDByUsername(username string) string {
	u, err := s.store.GetUserByUsername(username)
	if err != nil {
		return ""
	}
	return u.ID
}
