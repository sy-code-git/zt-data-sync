package syncer

import (
	"passbook/client/core/api"
	"passbook/internal/crypto"
	"passbook/internal/proto"
)

// autoRekey 自动重加密（§7.2 auto-rekey 完整流程，崩溃可续）。
// 触发：pull 看到 pending_rekey=true 且本地持有旧 kv 信封。
func (e *Engine) autoRekey(g *proto.GroupState) error {
	oldKV := g.KeyVersion
	// 本地持有旧 kv 信封（DEK）才可执行（§7.2：pending_rekey 且本地有旧 kv 信封）
	oldDEK, err := e.vault.GetDEK(g.ID, oldKV)
	if err != nil {
		return nil // 无旧 DEK：等信封到达后再收敛（§7.2 崩溃续跑）
	}
	defer crypto.Wipe(oldDEK)

	// 1. 写挂起该组（§7.2：重加密完成前该组新写入只进本地队列）
	e.mu.Lock()
	e.writeHeld[g.ID] = true
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		delete(e.writeHeld, g.ID)
		e.mu.Unlock()
	}()
	e.emitRekey(g.ID, true)
	defer e.emitRekey(g.ID, false)

	// 2-4. 生成新 DEK（kv+1）→ 缓存 → 为全部 active 成员包裹 → 上传信封集合
	newKV := oldKV + 1
	newDEK, err := e.vault.NewDEK()
	if err != nil {
		return err
	}
	// 必须先缓存新 DEK（SetDEK 拷贝），后续 reencrypt 用 kv+1 加密才能取到（§7.2）
	if err := e.vault.SetDEK(g.ID, newKV, newDEK); err != nil {
		crypto.Wipe(newDEK)
		return err
	}
	// 包裹对象必须是"该组全部 active 成员"（§7.2），而非全局 ListUsers()：
	// 多组场景下全局 active 用户 ⊃ 该组成员，信封含非该组成员会被服务端
	// 40904 拒收（"信封包含非 active 成员"），导致 rekey 永不收敛（P1 fix）。
	users, err := e.api.ListUsers() // 仅取公钥（byID）；包裹范围由 g.ActiveMembers 限定
	if err != nil {
		crypto.Wipe(newDEK)
		return err
	}
	byID := make(map[string]string, len(users))
	for _, u := range users {
		byID[u.UserID] = u.SM2PublicKey
	}
	envs := make([]proto.EnvelopeUpload, 0, len(g.ActiveMembers))
	for _, uid := range g.ActiveMembers {
		pub, ok := byID[uid]
		if !ok {
			continue // active 成员公钥缺失（理论不出现），跳过不阻塞
		}
		wrapped, err := e.vault.WrapDEKFor(pub, newDEK)
		if err != nil {
			crypto.Wipe(newDEK)
			return err
		}
		envs = append(envs, proto.EnvelopeUpload{UserID: uid, WrappedDEK: wrapped})
	}
	crypto.Wipe(newDEK)
	if len(envs) == 0 {
		return nil // 无包裹对象（异常/旧服务端未返回 active_members），跳过本轮等下轮
	}
	if err := e.api.UploadKeys(g.ID, &proto.KeysUploadRequest{KeyVersion: newKV, Envelopes: envs}); err != nil {
		// 40904/40902：另一成员已先完成 → 重拉后收敛，本方退出（§7.2）
		return nil
	}

	// 5. 用新 DEK 重加密本地全部旧 kv 条目并 push（§7.2 崩溃可续）。
	// reencryptGroupEntries 内部 pushDirty 会跳过 writeHeld 组（写挂起），
	// 重加密条目的实际推送由 syncOnce 步骤 5 的 pushDirty 在 autoRekey 返回后
	// （writeHeld 已 defer 清除）完成，两段分工最终收敛一致。
	if err := e.reencryptGroupEntries(g.ID, oldKV, newKV); err != nil {
		return err
	}
	return nil
}

// reencryptGroupEntries 用新 DEK 重加密组内旧 kv 条目并置脏（§7.2 步骤 5）。
func (e *Engine) reencryptGroupEntries(groupID string, oldKV, newKV int) error {
	entries, err := e.local.ListLocalEntries()
	if err != nil {
		return err
	}
	for i := range entries {
		le := &entries[i]
		// 只处理该组且 key_version 未达 newKV 的条目（§7.2 崩溃可续）。
		// 关键：不能只匹配 oldKV——rekey 中断恢复时本地条目可能停留在更老的 kv
		// （首次 push 失败后服务端 kv 已升），若只重加密 ==oldKV 的条目，则下一轮
		// oldKV 又变、条目永远匹配不上，kv 每轮 +1 死循环（P1 fix）。
		// dirty（本地未推送修改）也必须重加密——否则 rekey 后用旧 kv push 恒 40902。
		if le.GroupID != groupID || le.KeyVersion == newKV {
			continue
		}
		// 解密旧版本（密文内 pack.KV 决定用哪个 DEK）→ 用新 DEK 重加密（dirty 保留，待 push）
		plaintext, err := e.vault.DecryptPlaintext(groupID, le.ID, le.Ciphertext)
		if err != nil {
			continue // 坏密文跳过（bad_seq 已记录），不阻塞 rekey
		}
		newCT, err := e.vault.EncryptPlaintext(groupID, le.ID, plaintext, newKV)
		if err != nil {
			return err
		}
		le.Ciphertext = newCT
		le.KeyVersion = newKV
		// dirty 保持原值：原本 dirty 的条目重加密后仍待推送；原本干净的变为待推送
		le.Dirty = true
		// 重新加密后更新明文缓存（§9.1）
		cache, err := e.vault.EncryptCache(plaintext)
		if err == nil {
			le.PlaintextCache = cache
		}
		if err := e.local.UpsertLocalEntry(le); err != nil {
			return err
		}
	}
	// push 重加密条目（触发服务端收尾检测，§6.3）
	return e.pushDirty()
}

func (e *Engine) emitRekey(groupID string, started bool) {
	typ := api.EventRekeyDone
	if started {
		typ = api.EventRekeyStarted
	}
	e.emit(api.Event{Type: typ, Data: groupID})
}
