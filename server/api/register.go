package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"

	"passbook/internal/crypto"
	"passbook/internal/proto"
	"passbook/server/middleware"
	"passbook/server/store"
)

// handleRegisterRequest POST /auth/register-request（免登录：用户凭邀请码提交注册申请）。
// 流程（§6.3 方案 C）：校验邀请码（未用/未过期/工号匹配）→ 创建申请（pending，记录 IP）
// → 邀请码一次即废 → 若为免审核码（auto_approve）则直接开户（approved）。
func (s *Server) handleRegisterRequest(w http.ResponseWriter, r *http.Request) {
	var req proto.RegisterRequestRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	if req.InviteCode == "" || req.Username == "" || req.SM2PublicKey == "" || req.DeviceName == "" {
		writeErr(w, proto.ErrBadRequest, "字段缺失（邀请码/工号/公钥/设备名）")
		return
	}
	inv, err := s.store.GetInviteByCode(req.InviteCode)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "邀请码无效")
			return
		}
		handleErr(w, err)
		return
	}
	if inv.Status == store.InviteUsed {
		writeErr(w, proto.ErrBadRequest, "邀请码已使用（一次即废，请联系管理员重新生成）")
		return
	}
	if inv.ExpiresAt > 0 && s.now().Unix() > inv.ExpiresAt {
		writeErr(w, proto.ErrBadRequest, "邀请码已过期")
		return
	}
	if inv.Username != req.Username {
		writeErr(w, proto.ErrBadRequest, "工号与邀请码不匹配（邀请码绑定了其他工号）")
		return
	}
	// 公钥未注册（同一公钥不可重复开户，§6.3）
	if _, err := s.store.GetUserByPublicKey(req.SM2PublicKey); err == nil {
		writeErr(w, proto.ErrBadRequest, "公钥已注册")
		return
	} else if !errors.Is(err, store.ErrNoRows) {
		handleErr(w, err)
		return
	}
	// 工号未开户（已开户无需再申请）
	if _, err := s.store.GetUserByUsername(req.Username); err == nil {
		writeErr(w, proto.ErrBadRequest, "工号已开户")
		return
	} else if !errors.Is(err, store.ErrNoRows) {
		handleErr(w, err)
		return
	}

	now := s.now().Unix()
	rid := newUUID()
	ip := middleware.ClientIP(r)
	if err := s.store.CreateRegisterRequest(&store.RegisterRequest{
		ID: rid, InviteCode: req.InviteCode, Username: req.Username,
		SM2PublicKey: req.SM2PublicKey, DeviceName: req.DeviceName, IP: ip,
		Status: store.RegPending, CreatedAt: now,
	}); err != nil {
		handleErr(w, err)
		return
	}
	// 邀请码一次即废（提交申请即标记使用）
	if err := s.store.MarkInviteUsed(req.InviteCode, now); err != nil {
		handleErr(w, err)
		return
	}

	// 免审核码 → 服务端直接开户（attestation 由服务端按 PB_REG_SECRET 计算）
	if inv.AutoApprove == 1 {
		if err := s.approveAndCreateUser(req.InviteCode, rid, req.Username, req.SM2PublicKey, req.Username); err != nil {
			handleErr(w, err)
			return
		}
		s.audit.Record(r, "create_user", "", "")
		writeOK(w, proto.RegisterRequestResponse{ID: rid, Status: store.RegApproved})
		return
	}

	s.audit.Record(r, "register_request", "", "")
	writeOK(w, proto.RegisterRequestResponse{ID: rid, Status: store.RegPending})
}

// handleRegisterStatus GET /auth/register-status?invite_code=xxx（客户端轮询审核结果）。
func (s *Server) handleRegisterStatus(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("invite_code")
	if code == "" {
		writeErr(w, proto.ErrBadRequest, "缺少 invite_code")
		return
	}
	req, err := s.store.GetRegisterRequestByInvite(code)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "未找到申请记录")
			return
		}
		handleErr(w, err)
		return
	}
	writeOK(w, proto.RegisterStatusResponse{ID: req.ID, Status: req.Status})
}

// approveAndCreateUser 审核通过/免审核：按申请开户（attestation 服务端计算，等价信任）。
func (s *Server) approveAndCreateUser(inviteCode, reqID, username, pubKey, name string) error {
	att := crypto.HMACSM3(s.regSecret, []byte(attestationPrefix+name+pubKey))
	uid := newUUID()
	now := s.now().Unix()
	if err := s.store.WithTx(context.Background(), func(tx store.Tx) error {
		return tx.CreateUser(&store.User{
			ID: uid, Username: username, Name: name, SM2PublicKey: pubKey,
			Attestation: base64.StdEncoding.EncodeToString(att), Role: store.RoleMember,
			Status: store.StatusActive, CreatedAt: now,
		})
	}); err != nil {
		return err
	}
	return s.store.UpdateRegisterRequest(reqID, store.RegApproved, "", now)
}
