package api

import (
	"encoding/json"
	"net/http"
	"passbook/server/store"

	"passbook/internal/proto"
	"passbook/server/authn"
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
	s.audit.Record(r, "device_register", "", "")
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
	writeOK(w, &proto.HeartbeatResponse{OK: true})
}
