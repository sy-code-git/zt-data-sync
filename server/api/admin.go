package api

import (
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"passbook/internal/crypto"
	"passbook/internal/proto"
	"passbook/server/middleware"
	"passbook/server/store"
)

// attestationPrefix 注册凭证计算前缀（§4.4：HMAC-SM3(PB_REG_SECRET, prefix||name||pubkey)）。
const attestationPrefix = "passbook-attestation-v1"

// verifyAttestation 校验注册凭证（§4.4 / §6.3 admin 建用户）。
func (s *Server) verifyAttestation(name, pubKey, attestationB64 string) bool {
	if len(s.regSecret) == 0 {
		return false
	}
	want := crypto.HMACSM3(s.regSecret, []byte(attestationPrefix+name+pubKey))
	got, err := base64.StdEncoding.DecodeString(attestationB64)
	if err != nil {
		return false
	}
	return hmac.Equal(want, got)
}

// newUserID 生成 UUID（复用 authn 内部不可见，此处独立实现）。
func newUUID() string {
	return uuidV4()
}

// ---- admin handlers（§6.3） ----

// handleAdminCreateUser POST /admin/users（校验 attestation，§4.4）。
func (s *Server) handleAdminCreateUser(w http.ResponseWriter, r *http.Request) {
	var req proto.CreateUserRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	if req.Username == "" || req.Name == "" || req.SM2PublicKey == "" || req.Attestation == "" {
		writeErr(w, proto.ErrBadRequest, "字段缺失")
		return
	}
	// 校验注册凭证（§4.4：凭证不匹配一律拒收 40001）
	if !s.verifyAttestation(req.Name, req.SM2PublicKey, req.Attestation) {
		writeErr(w, proto.ErrBadRequest, "注册凭证校验失败")
		return
	}
	// 工号唯一（不可重复开户）
	if _, err := s.store.GetUserByUsername(req.Username); err == nil {
		writeErr(w, proto.ErrBadRequest, "工号已存在")
		return
	} else if !errors.Is(err, store.ErrNoRows) {
		handleErr(w, err)
		return
	}
	// 同一 sm2_public_key 不可重复注册（§6.3）
	if _, err := s.store.GetUserByPublicKey(req.SM2PublicKey); err == nil {
		writeErr(w, proto.ErrBadRequest, "公钥已注册")
		return
	} else if !errors.Is(err, store.ErrNoRows) {
		handleErr(w, err)
		return
	}
	uid := newUUID()
	err := s.store.WithTx(r.Context(), func(tx store.Tx) error {
		return tx.CreateUser(&store.User{
			ID: uid, Username: req.Username, Name: req.Name, SM2PublicKey: req.SM2PublicKey,
			Attestation: req.Attestation, Role: store.RoleMember,
			Status: store.StatusActive, CreatedAt: s.now().Unix(),
		})
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	s.audit.Record(r, "create_user", "", "")
	writeOK(w, proto.CreateUserResponse{UserID: uid})
}

// handleAdminRevoke POST /admin/users/:uid/revoke（§6.3：一键断权 + pending_rekey）。
func (s *Server) handleAdminRevoke(w http.ResponseWriter, r *http.Request) {
	uid := chi.URLParam(r, "uid")
	var req proto.RevokeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	u, err := s.store.GetUserByID(uid)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "用户不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if req.ConfirmName != u.Name {
		writeErr(w, proto.ErrBadRequest, "成员名确认不匹配")
		return
	}

	now := s.now().Unix()
	// 先查用户所在组（事务外读；事务内再写，避免单连接池死锁，见 P1 fix）
	groups, err := s.store.ListUserGroups(uid)
	if err != nil {
		handleErr(w, err)
		return
	}
	err = s.store.WithTx(r.Context(), func(tx store.Tx) error {
		if err := tx.SetUserRevoked(uid, now); err != nil {
			return err
		}
		if err := tx.DisableUserDevices(uid); err != nil {
			return err
		}
		if err := tx.DeleteUserEnvelopes(uid); err != nil {
			return err
		}
		// 从所有组移除 + 各组 pending_rekey=1（§6.3）
		for _, g := range groups {
			if err := tx.RemoveGroupMember(g.ID, uid); err != nil {
				return err
			}
			if err := tx.SetGroupRekey(g.ID, store.RekeyPending); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	// 断权联动：关闭该用户全部 SSE 连接（§6.3）
	if s.hub != nil {
		s.hub.DisconnectUser(uid)
	}
	// 最后成员检测：被吊销者是组内唯一成员 → 该组已无成员，提示管理员
	var emptyGroups []string
	for _, g := range groups {
		ms, err := s.store.ListGroupMembers(g.ID)
		if err != nil {
			continue
		}
		if len(ms) == 0 {
			emptyGroups = append(emptyGroups, g.Name)
		}
	}
	auditDetail := ""
	if len(emptyGroups) > 0 {
		auditDetail = "吊销后以下组已无成员: " + strings.Join(emptyGroups, ", ")
	}
	s.audit.Record(r, "revoke", auditDetail, "")
	writeOK(w, proto.RevokeResponse{UserID: uid, Status: store.StatusRevoked, EmptyGroups: emptyGroups})
}

// handleAdminKeyfileReset POST /admin/users/:uid/keyfile-reset（§4.4 / §6.3）。
func (s *Server) handleAdminKeyfileReset(w http.ResponseWriter, r *http.Request) {
	uid := chi.URLParam(r, "uid")
	var req proto.KeyfileResetRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	u, err := s.store.GetUserByID(uid)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "用户不存在")
			return
		}
		handleErr(w, err)
		return
	}
	// revoked 是终态，拒收（§6.3）
	if u.Status == store.StatusRevoked {
		writeErr(w, proto.ErrBadRequest, "已吊销用户不可 keyfile-reset")
		return
	}
	if !s.verifyAttestation(u.Name, req.SM2PublicKey, req.Attestation) {
		writeErr(w, proto.ErrBadRequest, "注册凭证校验失败")
		return
	}

	// 先查用户所在组（事务外读，避免单连接池死锁）
	groups, err := s.store.ListUserGroups(uid)
	if err != nil {
		handleErr(w, err)
		return
	}
	err = s.store.WithTx(r.Context(), func(tx store.Tx) error {
		if err := tx.ReplaceUserPublicKey(uid, req.SM2PublicKey, req.Attestation); err != nil {
			return err
		}
		if err := tx.DisableUserDevices(uid); err != nil {
			return err
		}
		if err := tx.DeleteUserEnvelopes(uid); err != nil {
			return err
		}
		for _, g := range groups {
			if err := tx.SetGroupRekey(g.ID, store.RekeyPending); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	if s.hub != nil {
		s.hub.DisconnectUser(uid)
	}
	s.audit.Record(r, "keyfile_reset", "", "")
	// 响应反映该用户所在任一组的当前状态
	var kv int
	var pending bool
	if groups, err := s.store.ListUserGroups(uid); err == nil && len(groups) > 0 {
		kv = groups[0].KeyVersion
		pending = groups[0].PendingRekey == store.RekeyPending
	}
	writeOK(w, proto.KeyfileResetResponse{UserID: uid, KeyVersion: kv, PendingRekey: pending})
}

// handleAdminCreateGroup POST /admin/groups（建组，kv=1，§6.3）。
func (s *Server) handleAdminCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req proto.GroupCreateRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	if req.Name == "" {
		writeErr(w, proto.ErrBadRequest, "组名不能为空")
		return
	}
	gid := newUUID()
	err := s.store.WithTx(r.Context(), func(tx store.Tx) error {
		if err := tx.CreateGroup(&store.Group{ID: gid, Name: req.Name, KeyVersion: 1,
			PendingRekey: store.RekeyDone, Archived: store.GroupNotArchived, CreatedAt: s.now().Unix()}); err != nil {
			return err
		}
		// 管理员自动加入新建组（§6.3：管理员默认为所有组成员）
		if ac, ok := middleware.WithAuth(r.Context()); ok && ac.User != nil && ac.User.Role == store.RoleAdmin {
			if err := tx.AddGroupMember(&store.GroupMember{GroupID: gid, UserID: ac.User.ID, CreatedAt: s.now().Unix()}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	s.audit.Record(r, "create_group", "", "")
	writeOK(w, proto.GroupCreateResponse{GroupID: gid, Name: req.Name, KeyVersion: 1})
}

// handleAdminListGroups GET /admin/groups（仅元数据，§6.3）。
func (s *Server) handleAdminListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListGroups()
	if err != nil {
		handleErr(w, err)
		return
	}
	out := make([]proto.GroupInfo, 0, len(groups))
	for _, g := range groups {
		members, err := s.store.ListGroupMembers(g.ID)
		if err != nil {
			handleErr(w, err)
			return
		}
		out = append(out, proto.GroupInfo{
			ID: g.ID, Name: g.Name, KeyVersion: g.KeyVersion,
			PendingRekey: g.PendingRekey == store.RekeyPending,
			Archived:     g.Archived == store.GroupArchived,
			ArchivedAt:   g.ArchivedAt, MemberCount: len(members), CreatedAt: g.CreatedAt,
		})
	}
	writeOK(w, proto.GroupsResponse{Groups: out})
}

// handleAdminAddMember PUT /admin/groups/:gid/members（幂等，§6.3）。
func (s *Server) handleAdminAddMember(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	var req proto.MemberAddRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	// 已是成员 → 幂等返回 200（§6.3）
	if ok, _ := s.store.GetGroupMember(gid, req.UserID); ok {
		writeOK(w, proto.MemberAddResponse{GroupID: gid, UserID: req.UserID})
		return
	}
	// 用户须存在且 active
	u, err := s.store.GetUserByID(req.UserID)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "用户不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if u.Status != store.StatusActive {
		writeErr(w, proto.ErrBadRequest, "用户已吊销")
		return
	}
	err = s.store.WithTx(r.Context(), func(tx store.Tx) error {
		return tx.AddGroupMember(&store.GroupMember{GroupID: gid, UserID: req.UserID, CreatedAt: s.now().Unix()})
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	s.audit.Record(r, "add_member", "", "")
	writeOK(w, proto.MemberAddResponse{GroupID: gid, UserID: req.UserID})
}

// handleAdminGroupMembers GET /admin/groups/:gid/members（成员清单，§6.3）。
func (s *Server) handleAdminGroupMembers(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	members, err := s.store.ListGroupMembers(gid)
	if err != nil {
		handleErr(w, err)
		return
	}
	out := make([]proto.GroupMemberInfo, 0, len(members))
	for _, m := range members {
		u, err := s.store.GetUserByID(m.UserID)
		if err != nil {
			continue
		}
		devices, err := s.store.ListDevicesByUser(m.UserID)
		if err != nil {
			handleErr(w, err)
			return
		}
		info := proto.GroupMemberInfo{UserID: m.UserID, Name: u.Name}
		online := false
		for i := range devices {
			d := &devices[i]
			devOnline := isDeviceOnline(d.LastSeen, s.now().Unix())
			if devOnline {
				online = true
			}
			info.Devices = append(info.Devices, proto.DeviceBrief{
				DeviceID: d.ID, Name: d.Name, Hostname: d.Hostname, IP: d.LastIP,
				Online: devOnline, LastSeen: d.LastSeen,
			})
		}
		info.Online = online
		out = append(out, info)
	}
	writeOK(w, proto.GroupMembersResponse{Members: out})
}

// isDeviceOnline 在线判定（§6.3：last_seen 60s 阈值内算在线；SSE 活跃连接由心跳维持）。
func isDeviceOnline(lastSeen, now int64) bool {
	return lastSeen > 0 && now-lastSeen <= 60
}

// handleAdminRekey POST /admin/groups/:gid/rekey（触发=置位，§6.3）。
func (s *Server) handleAdminRekey(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	g, err := s.store.GetGroup(gid)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "组不存在")
			return
		}
		handleErr(w, err)
		return
	}
	// 幂等：已是 1 则返回当前状态（§6.3）
	if g.PendingRekey != store.RekeyPending {
		if err := s.store.WithTx(r.Context(), func(tx store.Tx) error {
			return tx.SetGroupRekey(gid, store.RekeyPending)
		}); err != nil {
			handleErr(w, err)
			return
		}
	}
	s.audit.Record(r, "rekey", "", "")
	writeOK(w, proto.RekeyResponse{GroupID: gid, KeyVersion: g.KeyVersion, PendingRekey: true})
}

// handleAdminArchive POST /admin/groups/:gid/archive（组名二次确认，§6.3）。
func (s *Server) handleAdminArchive(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	var req proto.ArchiveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	g, err := s.store.GetGroup(gid)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "组不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if req.ConfirmName != g.Name {
		writeErr(w, proto.ErrBadRequest, "组名确认不匹配")
		return
	}
	// 幂等：已归档返回当前状态（§6.3）
	archivedAt := g.ArchivedAt
	if g.Archived != store.GroupArchived {
		now := s.now().Unix()
		if err := s.store.WithTx(r.Context(), func(tx store.Tx) error {
			return tx.SetGroupArchived(gid, store.GroupArchived, now)
		}); err != nil {
			handleErr(w, err)
			return
		}
		archivedAt = now
	}
	s.audit.Record(r, "archive", "", "")
	writeOK(w, proto.ArchiveResponse{GroupID: gid, Archived: true, ArchivedAt: archivedAt})
}

// handleAdminUnarchive POST /admin/groups/:gid/unarchive（重启组，§6.3）。
func (s *Server) handleAdminUnarchive(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	g, err := s.store.GetGroup(gid)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "组不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if g.Archived != store.GroupNotArchived {
		if err := s.store.WithTx(r.Context(), func(tx store.Tx) error {
			return tx.SetGroupArchived(gid, store.GroupNotArchived, 0)
		}); err != nil {
			handleErr(w, err)
			return
		}
	}
	s.audit.Record(r, "unarchive", "", "")
	writeOK(w, proto.UnarchiveResponse{GroupID: gid, Archived: false})
}

// handleAdminRemoveMember DELETE /admin/groups/:gid/members/:uid（§6.3）。
func (s *Server) handleAdminRemoveMember(w http.ResponseWriter, r *http.Request) {
	gid := chi.URLParam(r, "gid")
	uid := chi.URLParam(r, "uid")
	var req proto.MemberRemoveRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	u, err := s.store.GetUserByID(uid)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "用户不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if req.ConfirmName != u.Name {
		writeErr(w, proto.ErrBadRequest, "成员名确认不匹配")
		return
	}
	err = s.store.WithTx(r.Context(), func(tx store.Tx) error {
		if err := tx.RemoveGroupMember(gid, uid); err != nil {
			return err
		}
		if err := tx.DeleteGroupUserEnvelopes(gid, uid); err != nil {
			return err
		}
		return tx.SetGroupRekey(gid, store.RekeyPending)
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	s.audit.Record(r, "remove_member", "", "")
	writeOK(w, proto.MemberRemoveResponse{GroupID: gid, UserID: uid, Removed: true})
}

// handleAdminDevices GET /admin/devices（设备列表，§6.3）。
func (s *Server) handleAdminDevices(w http.ResponseWriter, r *http.Request) {
	devices, err := s.store.ListAllDevices()
	if err != nil {
		handleErr(w, err)
		return
	}
	now := s.now().Unix()
	out := make([]proto.AdminDevice, 0, len(devices))
	for i := range devices {
		d := &devices[i]
		u, err := s.store.GetUserByID(d.UserID)
		if err != nil {
			continue
		}
		out = append(out, proto.AdminDevice{
			DeviceID: d.ID, UserID: d.UserID, UserName: u.Name, Name: d.Name,
			Hostname: d.Hostname, IP: d.LastIP, Online: isDeviceOnline(d.LastSeen, now),
			LastSeen: d.LastSeen, Status: d.Status,
		})
	}
	writeOK(w, proto.DevicesResponse{Devices: out})
}

// handleAdminDisableDevice POST /admin/devices/:did/disable（设备名二次确认，§6.3）。
func (s *Server) handleAdminDisableDevice(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")
	var req proto.DisableDeviceRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, proto.ErrBadRequest, "请求体解析失败")
		return
	}
	d, err := s.store.GetDeviceByID(did)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			writeErr(w, proto.ErrBadRequest, "设备不存在")
			return
		}
		handleErr(w, err)
		return
	}
	if req.ConfirmName != d.Name {
		writeErr(w, proto.ErrBadRequest, "设备名确认不匹配")
		return
	}
	if err := s.store.WithTx(r.Context(), func(tx store.Tx) error {
		return tx.DisableDevice(did)
	}); err != nil {
		handleErr(w, err)
		return
	}
	// 断权联动：该设备用户的 SSE 由 token 失效自然断开（中间件 40101）
	s.audit.Record(r, "disable_device", "", "")
	writeOK(w, proto.DisableDeviceResponse{DeviceID: did, Status: store.DeviceDisabled})
}

// handleAdminAudit GET /admin/audit（审计查询，§6.3）。
func (s *Server) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var from, to int64
	var err error
	if v := q.Get("from"); v != "" {
		from, err = timeParse(v)
		if err != nil {
			writeErr(w, proto.ErrBadRequest, "from 参数非法（须 RFC3339）")
			return
		}
	}
	if v := q.Get("to"); v != "" {
		to, err = timeParse(v)
		if err != nil {
			writeErr(w, proto.ErrBadRequest, "to 参数非法（须 RFC3339）")
			return
		}
	}
	events, err := s.store.QueryAudit(from, to, q.Get("user_id"), q.Get("action"), 500)
	if err != nil {
		handleErr(w, err)
		return
	}
	out := make([]proto.AuditEventOut, 0, len(events))
	for i := range events {
		e := &events[i]
		u, _ := s.store.GetUserByID(e.UserID)
		name := ""
		if u != nil {
			name = u.Name
		}
		out = append(out, proto.AuditEventOut{
			ID: e.ID, TS: e.TS, UserID: e.UserID, UserName: name, Action: e.Action,
			DeviceID: e.DeviceID, DeviceName: e.DeviceName, Hostname: e.Hostname,
			IP: e.IP, EntryID: e.EntryID, Detail: e.Detail,
		})
	}
	writeOK(w, proto.AuditResponse{Events: out})
}

func timeParse(s string) (int64, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}
