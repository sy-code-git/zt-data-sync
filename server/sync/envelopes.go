package sync

import (
	"context"
	"errors"

	"passbook/internal/proto"
	"passbook/server/store"
)

// UploadKeys 信封集合提交（§6.3 POST /groups/:gid/keys：入伙追加 / rekey 替换）。
// 校验：
//   - 请求者是组成员；组未归档（40905）
//   - key_version == 组当前 kv（入伙追加：只为 missing_envelopes 成员新增，不允许覆盖）
//     或 == 当前 kv+1（rekey 替换：集合必须恰好等于全部 active 成员）
//   - envelopes 的 user 集合是 active 成员的子集
func (s *Service) UploadKeys(ctx context.Context, userID, groupID string, req *proto.KeysUploadRequest) error {
	isMember, err := s.store.GetGroupMember(groupID, userID)
	if err != nil {
		return err
	}
	if !isMember {
		return errCode(proto.ErrNotMember, "非该组成员")
	}
	g, err := s.store.GetGroup(groupID)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			return errCode(proto.ErrNotMember, "组不存在")
		}
		return err
	}
	if g.Archived == store.GroupArchived {
		return errCode(proto.ErrGroupArchived, "组已归档，信封提交被拒绝")
	}

	active, err := s.activeMemberIDs(ctx, groupID)
	if err != nil {
		return err
	}
	activeSet := toSet(active)

	// envelopes user 集合必须是 active 成员子集（§6.3）
	for _, e := range req.Envelopes {
		if !activeSet[e.UserID] {
			return errCode(proto.ErrBadEnvelopes, "信封包含非 active 成员")
		}
	}

	// 冷启动：组当前 kv 无信封，missing == 全部 active，入伙追加分支（kv=1==当前 kv）接收全集
	if req.KeyVersion == g.KeyVersion {
		// 入伙追加：只为当前 kv 下无信封的成员新增，不允许覆盖（§6.3）
		if err := s.appendEnvelopes(ctx, groupID, req.KeyVersion, req.Envelopes, activeSet); err != nil {
			return err
		}
		s.hubNotify(groupID)
		return nil
	}

	if req.KeyVersion == g.KeyVersion+1 {
		// rekey 替换：集合必须恰好等于全部 active 成员（§6.3）
		if len(req.Envelopes) != len(active) {
			return errCode(proto.ErrBadEnvelopes, "rekey 信封集合必须等于全部 active 成员")
		}
		submitted := toSet(envelopeUserIDs(req))
		for _, uid := range active {
			if !submitted[uid] {
				return errCode(proto.ErrBadEnvelopes, "rekey 信封集合缺失成员")
			}
		}
		return s.commitRekey(ctx, groupID, g, req.KeyVersion, req.Envelopes)
	}

	return errCode(proto.ErrBadEnvelopes, "key_version 不合法（须为当前 kv 或 kv+1）")
}

// envelopeUserIDs 提取信封集合中的 user_id 列表（rekey 完整性校验辅助）。
func envelopeUserIDs(req *proto.KeysUploadRequest) []string {
	out := make([]string, 0, len(req.Envelopes))
	for _, env := range req.Envelopes {
		out = append(out, env.UserID)
	}
	return out
}

// appendEnvelopes 入伙追加（kv 不变，仅新增；覆盖已有 → 40904）。
func (s *Service) appendEnvelopes(ctx context.Context, groupID string, kv int, envs []proto.EnvelopeUpload, activeSet map[string]bool) error {
	// 事务外读已有信封（避免单连接池死锁，见 P1 fix）
	existing, err := s.store.GetGroupEnvelopes(groupID, kv)
	if err != nil {
		return err
	}
	has := map[string]bool{}
	for _, e := range existing {
		has[e.UserID] = true
	}
	for _, e := range envs {
		if has[e.UserID] {
			return errCode(proto.ErrBadEnvelopes, "信封已存在，不允许覆盖")
		}
	}
	now := s.now().Unix()
	return s.store.WithTx(ctx, func(tx store.Tx) error {
		for _, e := range envs {
			if err := tx.UpsertEnvelope(&store.Envelope{
				GroupID: groupID, KeyVersion: kv, UserID: e.UserID,
				WrappedDEK: e.WrappedDEK, UpdatedAt: now,
			}); err != nil {
				if errors.Is(err, store.ErrConstraintUnique) {
					return errCode(proto.ErrBadEnvelopes, "信封已存在，不允许覆盖")
				}
				return err
			}
		}
		return nil
	})
}

// commitRekey rekey 提交（kv+1）：
//   - 替换信封集合 → groups.key_version 升 kv+1
//   - 若组内全部条目已到达新 kv（排除墓碑）→ 清 pending_rekey + 删除旧 kv 信封（§6.3）
func (s *Service) commitRekey(ctx context.Context, groupID string, g *store.Group, newKV int, envs []proto.EnvelopeUpload) error {
	// 事务外读条目收敛状态（避免单连接池死锁）
	below, err := s.store.CountEntriesBelowKV(groupID, newKV)
	if err != nil {
		return err
	}
	now := s.now().Unix()
	envStore := make([]store.Envelope, 0, len(envs))
	for _, e := range envs {
		envStore = append(envStore, store.Envelope{GroupID: groupID, KeyVersion: newKV, UserID: e.UserID, WrappedDEK: e.WrappedDEK, UpdatedAt: now})
	}
	return s.store.WithTx(ctx, func(tx store.Tx) error {
		if err := tx.ReplaceEnvelopes(groupID, newKV, envStore, now); err != nil {
			return err
		}
		if err := tx.SetGroupKeyVersion(groupID, newKV); err != nil {
			return err
		}
		// 收尾：全部条目到达新 kv（排除墓碑）→ 清 pending_rekey + 删旧信封
		if below == 0 {
			if err := tx.SetGroupRekey(groupID, store.RekeyDone); err != nil {
				return err
			}
			if err := tx.DeleteOldKVEnvelopes(groupID, newKV); err != nil {
				return err
			}
		}
		return nil
	})
}

// activeMemberIDs 组内 active 成员 id 列表。
func (s *Service) activeMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	members, err := s.store.ListGroupMembers(groupID)
	if err != nil {
		return nil, err
	}
	var active []string
	for _, m := range members {
		u, err := s.store.GetUserByID(m.UserID)
		if err != nil {
			if errors.Is(err, store.ErrNoRows) {
				continue
			}
			return nil, err
		}
		if u.Status == store.StatusActive {
			active = append(active, m.UserID)
		}
	}
	return active, nil
}

func toSet(ids []string) map[string]bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

func (s *Service) hubNotify(groupID string) {
	if s.hub != nil {
		s.hub.Notify([]string{groupID})
	}
}
