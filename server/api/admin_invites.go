package api

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"passbook/internal/proto"
	"passbook/server/middleware"
	"passbook/server/store"
)

// inviteTTLDefault 邀请码默认有效期（天，§6.3 可配）。
const inviteTTLDefault = 3

// currentUserID 当前请求认证用户 ID（admin 路由必有）。
func currentUserID(r *http.Request) string {
	if a, ok := middleware.WithAuth(r.Context()); ok && a.User != nil {
		return a.User.ID
	}
	return ""
}

// boolToInt bool → 0/1（SQLite 存 int）。
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// genInviteCode 生成 8 位字母数字邀请码（crypto/rand）。
func genInviteCode() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去易混淆 I/O/0/1
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("genInviteCode: " + err.Error()) // 启动/运行期 rand 失败不可恢复
	}
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// handleAdminCreateInvite POST /admin/invites（生成邀请码，绑定工号）。
// 同工号未开户可重复生成（每次新码）；已开户不可生成。
func (s *Server) handleAdminCreateInvite(w http.ResponseWriter, r *http.Request) {
	var req proto.CreateInviteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	if req.Username == "" {
		writeErr(w, proto.ErrBadRequest, "工号必填")
		return
	}
	// 已开户不可生成（先查用户）
	if _, err := s.store.GetUserByUsername(req.Username); err == nil {
		writeErr(w, proto.ErrBadRequest, "该工号已开户，无需邀请码")
		return
	} else if !errors.Is(err, store.ErrNoRows) {
		handleErr(w, err)
		return
	}
	ttl := req.TTLDays
	if ttl <= 0 {
		ttl = inviteTTLDefault
	}
	now := s.now().Unix()
	adminID := currentUserID(r)
	inv := &store.Invite{
		ID: newUUID(), Code: genInviteCode(), Username: req.Username,
		AutoApprove: boolToInt(req.AutoApprove), Status: store.InviteUnused,
		ExpiresAt: now + int64(ttl)*86400, CreatedBy: adminID, CreatedAt: now,
	}
	if err := s.store.CreateInvite(inv); err != nil {
		handleErr(w, err)
		return
	}
	s.audit.Record(r, "create_invite", "", "")
	writeOK(w, proto.InviteResponse{Invite: toInviteOut(inv)})
}

// handleAdminListInvites GET /admin/invites（邀请码列表）。
func (s *Server) handleAdminListInvites(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.ListInvites()
	if err != nil {
		handleErr(w, err)
		return
	}
	out := make([]proto.InviteOut, 0, len(list))
	for i := range list {
		out = append(out, toInviteOut(&list[i]))
	}
	writeOK(w, proto.InvitesResponse{Invites: out})
}

// handleAdminListRegisterRequests GET /admin/register-requests?status=pending|approved|rejected|（空=全部）。
func (s *Server) handleAdminListRegisterRequests(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	list, err := s.store.ListRegisterRequests(status)
	if err != nil {
		handleErr(w, err)
		return
	}
	out := make([]proto.RegisterRequestOut, 0, len(list))
	for i := range list {
		out = append(out, toRegisterRequestOut(&list[i]))
	}
	writeOK(w, proto.RegisterRequestsResponse{Requests: out})
}

// handleAdminApproveRequest POST /admin/register-requests/:id/approve（审核通过 = 开户）。
func (s *Server) handleAdminApproveRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, err := s.store.GetRegisterRequestByID(id)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "申请不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if req.Status != store.RegPending {
		writeErr(w, proto.ErrBadRequest, "申请已处理（非待审核状态）")
		return
	}
	var body proto.ReviewRequestRequest
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = req.Username // 默认显示名 = 工号
	}
	// 开户（工号/公钥唯一性在 register-request 提交时已校验，此处复核）
	if _, err := s.store.GetUserByUsername(req.Username); err == nil {
		writeErr(w, proto.ErrBadRequest, "工号已开户")
		return
	}
	adminID := currentUserID(r)
	now := s.now().Unix()
	if err := s.approveAndCreateUser(req.InviteCode, id, req.Username, req.SM2PublicKey, name); err != nil {
		handleErr(w, err)
		return
	}
	// 记录审核人
	_ = s.store.UpdateRegisterRequest(id, store.RegApproved, adminID, now)
	s.audit.Record(r, "create_user", "", "")
	writeOK(w, proto.RegisterStatusResponse{ID: id, Status: store.RegApproved})
}

// handleAdminRejectRequest POST /admin/register-requests/:id/reject（拒绝申请）。
func (s *Server) handleAdminRejectRequest(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	req, err := s.store.GetRegisterRequestByID(id)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "申请不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if req.Status != store.RegPending {
		writeErr(w, proto.ErrBadRequest, "申请已处理（非待审核状态）")
		return
	}
	now := s.now().Unix()
	if err := s.store.UpdateRegisterRequest(id, store.RegRejected, currentUserID(r), now); err != nil {
		handleErr(w, err)
		return
	}
	s.audit.Record(r, "reject_register", "", "")
	writeOK(w, proto.RegisterStatusResponse{ID: id, Status: store.RegRejected})
}

func toInviteOut(inv *store.Invite) proto.InviteOut {
	return proto.InviteOut{
		Code: inv.Code, Username: inv.Username, AutoApprove: inv.AutoApprove == 1,
		Status: inv.Status, ExpiresAt: inv.ExpiresAt, CreatedAt: inv.CreatedAt, UsedAt: inv.UsedAt,
	}
}

func toRegisterRequestOut(r *store.RegisterRequest) proto.RegisterRequestOut {
	return proto.RegisterRequestOut{
		ID: r.ID, InviteCode: r.InviteCode, Username: r.Username,
		SM2PublicKey: r.SM2PublicKey, DeviceName: r.DeviceName, IP: r.IP,
		Status: r.Status, CreatedAt: r.CreatedAt, ReviewedBy: r.ReviewedBy, ReviewedAt: r.ReviewedAt,
	}
}
