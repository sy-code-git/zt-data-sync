package sync

import (
	"context"
	"errors"
	"log"

	"passbook/internal/proto"
	"passbook/server/store"
)

// Push 批量推送变更（§6.3 POST /sync/push）。
// 校验链顺序固定（§6.3）：token 有效 → 用户 active → 属于组 → 组未归档 →
// ciphertext ≤256KB → key_version==组当前 kv → base_seq 匹配 → 事务内分配 seq。
// 多条各自独立事务（单条失败不影响其他）。
func (s *Service) Push(ctx context.Context, userID, deviceID string, req *proto.PushRequest) (*proto.PushResponse, error) {
	resp := &proto.PushResponse{Results: make([]proto.PushResult, 0, len(req.Mutations))}
	notifyGroups := map[string]bool{}

	for _, m := range req.Mutations {
		res, notify := s.pushOne(ctx, userID, deviceID, m)
		if notify {
			notifyGroups[m.GroupID] = true
		}
		resp.Results = append(resp.Results, res)
	}

	// 推送成功后经 SSE 通知相关组（§6.4 硬规则 #8：通知仅含 seq 元数据与组 ID）
	if s.hub != nil && len(notifyGroups) > 0 {
		s.hub.Notify(groupsList(notifyGroups))
	}
	return resp, nil
}

// pushOne 处理单条变更，返回结果与是否需通知。
func (s *Service) pushOne(ctx context.Context, userID, deviceID string, m proto.Mutation) (proto.PushResult, bool) {
	// 1. 属于组
	isMember, err := s.store.GetGroupMember(m.GroupID, userID)
	if err != nil {
		return fail(m.EntryID, proto.ErrInternal, nil), false
	}
	if !isMember {
		return fail(m.EntryID, proto.ErrNotMember, nil), false
	}
	// 2. 组存在 + 未归档
	g, err := s.store.GetGroup(m.GroupID)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			return fail(m.EntryID, proto.ErrNotMember, nil), false
		}
		return fail(m.EntryID, proto.ErrInternal, nil), false
	}
	if g.Archived == store.GroupArchived {
		return fail(m.EntryID, proto.ErrGroupArchived, nil), false
	}
	// 3. ciphertext 上限（§5.1 256KB）
	if len(m.Ciphertext) > MaxCiphertext {
		return fail(m.EntryID, proto.ErrTooLarge, nil), false
	}
	// 4. key_version == 组当前 kv（§6.3 旧 kv 拒收 40902）
	if m.KeyVersion != g.KeyVersion {
		return fail(m.EntryID, proto.ErrKeyVersionStale, nil), false
	}
	// 5. base_seq 冲突检测（§7.3）
	cur, err := s.store.GetEntry(m.EntryID)
	if err != nil && !errors.Is(err, store.ErrNoRows) {
		return fail(m.EntryID, proto.ErrInternal, nil), false
	}
	if err == nil {
		// 条目已存在：base_seq 必须等于当前 seq，否则 40901（携带服务端当前版）
		if m.BaseSeq != cur.Seq {
			return fail(m.EntryID, proto.ErrConflict, &proto.Change{
				EntryID: cur.ID, GroupID: cur.GroupID, Seq: cur.Seq,
				KeyVersion: cur.KeyVersion, Ciphertext: cur.Ciphertext,
				Deleted: cur.Deleted, UpdatedAt: cur.UpdatedAt,
			}), false
		}
	} else {
		// 条目不存在：base_seq 必须为 0（新增），否则 40903
		if m.BaseSeq != 0 {
			return fail(m.EntryID, proto.ErrEntryState, nil), false
		}
	}

	// 6. 事务内分配 seq 写入
	now := s.now().Unix()
	var newSeq int64
	err = s.store.WithTx(ctx, func(tx store.Tx) error {
		var err error
		newSeq, err = tx.UpsertEntry(&store.Entry{
			ID: m.EntryID, GroupID: m.GroupID, KeyVersion: m.KeyVersion,
			Ciphertext: m.Ciphertext, SizeBytes: len(m.Ciphertext), Deleted: m.Deleted,
			UpdatedBy: deviceID, UpdatedAt: now,
		})
		return err
	})
	if err != nil {
		if errors.Is(err, store.ErrConstraintUnique) {
			return fail(m.EntryID, proto.ErrEntryState, nil), false
		}
		return fail(m.EntryID, proto.ErrInternal, nil), false
	}

	// 收尾检测（§6.3：push 事务内检测到全部条目到达当前 kv → 清 pending_rekey + 删旧 kv 信封）。
	// 成员用新 DEK 重加密条目 push 后触发收敛；幂等，失败不阻塞推送（下一轮 push/拉取再收敛）。
	if err := s.maybeFinishRekey(ctx, m.GroupID, g.KeyVersion); err != nil {
		log.Printf("sync: rekey 收尾检测失败 group=%s: %v", m.GroupID, err)
	}

	return proto.PushResult{EntryID: m.EntryID, OK: true, NewSeq: newSeq}, true
}

// maybeFinishRekey rekey 收尾：组内全部条目（排除墓碑）已到达当前 kv → 清 pending_rekey + 删旧 kv 信封。
// 读在事务外（避免单连接池死锁），写在独立事务；幂等可并发。
func (s *Service) maybeFinishRekey(ctx context.Context, groupID string, currentKV int) error {
	below, err := s.store.CountEntriesBelowKV(groupID, currentKV)
	if err != nil {
		return err
	}
	if below > 0 {
		return nil // 仍有条目未到达当前 kv，未收敛
	}
	return s.store.WithTx(ctx, func(tx store.Tx) error {
		if err := tx.SetGroupRekey(groupID, store.RekeyDone); err != nil {
			return err
		}
		return tx.DeleteOldKVEnvelopes(groupID, currentKV)
	})
}

func fail(entryID string, code int, current *proto.Change) proto.PushResult {
	return proto.PushResult{EntryID: entryID, OK: false, Error: code, Current: current}
}

func groupsList(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for g := range m {
		out = append(out, g)
	}
	return out
}
