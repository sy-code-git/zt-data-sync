// Package syncer 同步引擎（§7.2）：SSE 推送 + 回退轮询 + auto-wrap/auto-rekey。
package syncer

import (
	"context"
	"errors"
	"net/http"
	"os"
	"sync"
	"time"

	"passbook/client/core/api"
	"passbook/client/core/store"
	"passbook/client/core/vault"
	"passbook/internal/crypto"
	"passbook/internal/merge"
	"passbook/internal/proto"
)

// ServerClient 服务端 HTTP 客户端（可注入测试）。
type ServerClient interface {
	// Pull 增量拉取（§6.3）。
	Pull(since int64, keyVersions map[string]int) (*proto.SyncResponse, error)
	// Push 推送变更（§6.3）。
	Push(mutations []proto.Mutation) (*proto.PushResponse, error)
	// UploadKeys 上传信封集合（§6.3）。
	UploadKeys(groupID string, req *proto.KeysUploadRequest) error
	// ListUsers 获取 active 用户（auto-wrap 包裹 DEK 用，§7.2）。
	ListUsers() ([]proto.UserInfo, error)
	// Heartbeat 心跳上报（§9.1：启动 + 每 30s；维持服务端在线判定）。
	Heartbeat(hostname string) error
	// Token 当前设备 token（SSE 重连取最新，§7.2 token 刷新）。
	Token() string
	// SetToken 更新 token（刷新成功后）。
	SetToken(string)
	// RefreshToken 换新 token（旧 token 服务端即刻作废，§6.3）。
	RefreshToken() (string, int64, error)
}

// Engine 同步引擎（§7.2 状态机）。
type Engine struct {
	vault *vault.Vault
	local store.LocalStore
	api   ServerClient

	serverURL string // SSE 连接地址
	token     string // 解锁态设备 token（SSE 鉴权）
	sseBase   *http.Client // SSE 长连接基础客户端（复用 API Transport，继承 CA pinning/智能选路）
	tokenTTL  int64  // 最近一次签发 token 的总有效期（秒，0=未知），提前刷新阈值用

	mu        sync.Mutex
	status    api.SyncStatus
	running   bool
	stopCh    chan struct{}
	streamCtx context.CancelFunc

	listeners []api.Listener
	now       func() time.Time

	// 写挂起组（§7.2：rekey 期间该组新写入本地排队，不出网）
	writeHeld map[string]bool
}

// New 构造同步引擎。
// serverURL：服务端地址（SSE 用）；token：解锁态设备 token。
// sseBase：SSE 长连接基础客户端（复用 API 的 Transport，保证 CA pinning 生效）；
// 为 nil 时用默认客户端（测试/系统信任链场景）。
func New(v *vault.Vault, local store.LocalStore, c ServerClient, sseBase *http.Client, serverURL, token string, now func() time.Time) *Engine {
	if now == nil {
		now = time.Now
	}
	if sseBase == nil {
		sseBase = http.DefaultClient
	}
	return &Engine{
		vault: v, local: local, api: c,
		serverURL: serverURL, token: token,
		sseBase:   sseBase,
		status:    api.SyncStatus{Phase: api.PhaseIdle},
		now:       now,
		writeHeld: map[string]bool{},
	}
}

// Subscribe 注册事件监听（UI 刷新用）。
func (e *Engine) Subscribe(fn api.Listener) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, fn)
}

func (e *Engine) emit(ev api.Event) {
	e.mu.Lock()
	ls := append([]api.Listener(nil), e.listeners...)
	e.mu.Unlock()
	for _, fn := range ls {
		fn(ev)
	}
}

// Start 启动同步（SSE 订阅 + 初始增量拉取，§7.2 步骤 0）。
func (e *Engine) Start() {
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return
	}
	e.running = true
	e.stopCh = make(chan struct{})
	e.mu.Unlock()

	go e.runLoop()
}

// Stop 停止同步（并取消 SSE 长连接，防 goroutine 泄漏）。
func (e *Engine) Stop() {
	e.mu.Lock()
	if !e.running {
		e.mu.Unlock()
		return
	}
	e.running = false
	close(e.stopCh)
	if e.streamCtx != nil {
		e.streamCtx()
		e.streamCtx = nil
	}
	e.mu.Unlock()
}

// SyncNow 手动触发一轮同步（UI 刷新，§7.2 触发源）。
func (e *Engine) SyncNow() error {
	return e.syncOnce()
}

// Status 当前状态快照。
func (e *Engine) Status() api.SyncStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	// 组列表从本地 group_state 读取（§7.2 applyGroups 已落库）：
	// UI 新建条目的组回退链依赖 status.groups（修「缺少组 ID」）。
	if gs, err := e.local.ListGroupStates(); err == nil {
		out := make([]api.GroupSyncState, 0, len(gs))
		for i := range gs {
			g := &gs[i]
			if g.Archived {
				continue // 归档组不可建条目，跳过
			}
			out = append(out, api.GroupSyncState{
				ID: g.GroupID, Name: g.Name, KeyVersion: g.KeyVersion, Archived: g.Archived,
			})
		}
		e.status.Groups = out
	}
	return e.status
}

// getToken / setToken 并发安全读写设备 token（SSE 重连 goroutine 读、
// syncOnce 内 token 刷新 goroutine 写，§7.2）。
func (e *Engine) getToken() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.token
}

func (e *Engine) setToken(t string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.token = t
}

func (e *Engine) setPhase(p api.SyncPhase, errMsg string) {
	e.mu.Lock()
	e.status.Phase = p
	e.status.Error = errMsg
	e.mu.Unlock()
	e.emit(api.Event{Type: api.EventSyncStatus, Data: p, At: e.now()})
}

// runLoop 后台循环：SSE 推送驱动；SSE 不可用回退轮询（§7.2）。
func (e *Engine) runLoop() {
	defer func() {
		e.mu.Lock()
		e.running = false
		e.mu.Unlock()
	}()

	// 启动立即增量拉取（§7.2 步骤 0）
	_ = e.syncOnce()

	// 心跳 ticker（§9.1：每 30s 上报 hostname，维持服务端在线判定）
	hb := time.NewTicker(30 * time.Second)
	defer hb.Stop()

	bf := newBackoff()
	for {
		select {
		case <-e.stopCh:
			return
		case <-hb.C:
			_ = e.api.Heartbeat(hostnameOf(e))
		default:
		}

		// token 临近过期 → 提前刷新（前端无感，SSE 用新 token）
		e.refreshIfExpiring()

		// 尝试 SSE（长连接，阻塞直到断线）
		ctx, cancel := context.WithCancel(context.Background())
		e.mu.Lock()
		e.streamCtx = cancel
		e.status.Connected = true
		e.mu.Unlock()

		sc := newSSEClient(e.serverURL, e.getToken(), e.sseBase)
		_ = sc.Run(ctx, func(seq int64) {
			// 收到 change 事件立即再拉（§7.2 触发源）
			_ = e.syncOnce()
		})
		cancel()
		e.mu.Lock()
		e.status.Connected = false
		e.mu.Unlock()

		// SSE 断线：指数退避重连；连续失败 10 次切轮询（§7.2）
		if bf.Fails() >= maxSSEFails {
			e.setPhase(api.PhaseOffline, "SSE 连续失败，回退轮询")
			if !e.pollUntilRecover(bf) {
				return
			}
			bf.Reset()
			continue
		}
		delay := bf.Next()
		e.setPhase(api.PhaseOffline, "")
		select {
		case <-e.stopCh:
			return
		case <-time.After(delay):
		}
	}
}

// pollUntilRecover 轮询模式：每 30s 拉取一次，每 5min 尝试恢复 SSE（§7.2）。
// 返回是否应恢复 SSE（true）或已停止（false）。
func (e *Engine) pollUntilRecover(bf *backoff) bool {
	pollTick := time.NewTicker(pollDegraded)
	defer pollTick.Stop()
	recoverTick := time.NewTicker(recoverSSEInt)
	defer recoverTick.Stop()

	for {
		select {
		case <-e.stopCh:
			return false
		case <-pollTick.C:
			if err := e.syncOnce(); err != nil {
				// 保持轮询
			} else {
				return true // 网络恢复，回 SSE
			}
		case <-recoverTick.C:
			return true // 尝试恢复 SSE
		}
	}
}

// syncOnce 单轮同步循环（§7.2 步骤 1-6）。
func (e *Engine) syncOnce() error {
	// 单例互斥（§14.1：同一时刻只允许一轮同步）
	e.mu.Lock()
	if e.status.Phase == api.PhasePulling || e.status.Phase == api.PhasePushing {
		e.mu.Unlock()
		return nil
	}
	e.status.Phase = api.PhasePulling
	e.mu.Unlock()
	defer func() { e.setPhase(api.PhaseIdle, "") }()

	// token 临近过期 → 提前刷新（避免 401 补救路径）
	e.refreshIfExpiring()

	lastSeq, _ := e.local.GetLastSeq()
	var resp *proto.SyncResponse
	err := e.authRetry(func() error {
		r, err := e.api.Pull(lastSeq, e.localKeyVersions())
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		e.setPhase(api.PhaseError, err.Error())
		return err
	}

	// 2. 处理信封（先于 changes，§7.2：rekey 批次与新成员首次同步都依赖新 DEK）
	if err := e.applyEnvelopes(resp.KeyEnvelopes); err != nil {
		e.setPhase(api.PhaseError, "处理信封失败: "+err.Error())
		return err
	}

	// 3. 处理组协同状态（auto-wrap / auto-rekey / 新组 / 归档）
	if err := e.applyGroups(resp.Groups); err != nil {
		e.setPhase(api.PhaseError, err.Error())
		return err
	}

	// 3.5 补处理 pending_entries（§7.2 4a：信封到达后补验 hmac + 解密入库）
	if err := e.flushPendingEntries(); err != nil {
		e.setPhase(api.PhaseError, err.Error())
		return err
	}

	// 4. 逐条处理 changes
	if err := e.applyChanges(resp.Changes); err != nil {
		e.setPhase(api.PhaseError, err.Error())
		return err
	}

	// 5. push 本地脏条目
	if err := e.pushDirty(); err != nil {
		e.setPhase(api.PhaseError, err.Error())
		return err
	}

	// 6. last_seq 推进（pending 暂存不影响，§7.2）
	// 关键：分页安全——服务端 ServerSeq 是全局当前值，可能大于本次实际返回的最后一条
	// change 的 seq（≥500 条截断或他组变更占额）。last_seq 必须只推进到"本次已收到的
	// 最后一条"，否则跳批漏拉数据（P1 fix）。无 changes 时才用 ServerSeq 快进。
	newLast := resp.ServerSeq
	if n := len(resp.Changes); n > 0 && resp.Changes[n-1].Seq < resp.ServerSeq {
		newLast = resp.Changes[n-1].Seq
	}
	if newLast > lastSeq {
		if err := e.local.SetLastSeq(newLast); err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.status.ServerSeq = resp.ServerSeq
	e.status.LastSeq = newLast
	e.status.LastPullAt = e.now().Unix()
	e.mu.Unlock()
	return nil
}

// applyEnvelopes 解信封并缓存 DEK（§7.2 步骤 2，先于 changes）。
func (e *Engine) applyEnvelopes(envs []proto.KeyEnvelopeInfo) error {
	for _, env := range envs {
		dek, err := e.vault.UnwrapDEK(env.GroupID, env.KeyVersion, env.WrappedDEK)
		if err != nil {
			return err
		}
		crypto.Wipe(dek) // 返回的 DEK 拷贝用后清零（§4.2 内存卫生）
	}
	return nil
}

// flushPendingEntries 补处理等待信封的暂存条目（§7.2 4a：信封到达后补验 hmac + 解密入库并清除暂存）。
func (e *Engine) flushPendingEntries() error {
	pends, err := e.local.ListPendingEntries()
	if err != nil {
		return err
	}
	for _, pe := range pends {
		// 有对应 kv 的 DEK 才补处理（无则继续等待）；探测用的 DEK 拷贝立即清零
		if dek, err := e.vault.GetDEK(pe.GroupID, pe.KeyVersion); err != nil {
			continue
		} else {
			crypto.Wipe(dek)
		}
		change := proto.Change{
			EntryID: pe.ID, GroupID: pe.GroupID, Seq: pe.Seq,
			KeyVersion: pe.KeyVersion, Ciphertext: pe.Ciphertext, UpdatedAt: pe.UpdatedAt,
		}
		if err := e.applyOneChange(&change); err != nil {
			return err
		}
	}
	return nil
}

// applyGroups 处理组协同状态（§7.2 步骤 3）。
func (e *Engine) applyGroups(groups []proto.GroupState) error {
	for i := range groups {
		g := &groups[i]
		// 记录/检测归档翻转（§7.2 4d）
		gs, err := e.local.GetGroupState(g.ID)
		if err != nil && !errors.Is(err, store.ErrNoRows) {
			return err
		}
		newGroup := errors.Is(err, store.ErrNoRows) || gs.InitializedAt == 0
		if gs != nil && gs.Archived && !g.Archived {
			// unarchive 翻转 → 全量拉取（§7.2 4d）
			if err := e.pullGroupFull(g.ID); err != nil {
				return err
			}
		}
		if newGroup && !g.Archived {
			// 新加入的组 → 全量拉取（§7.2 4c）
			if err := e.pullGroupFull(g.ID); err != nil {
				return err
			}
		}
		if err := e.local.SetGroupState(&store.GroupState{GroupID: g.ID, Name: g.Name, Archived: g.Archived, KeyVersion: g.KeyVersion, InitializedAt: e.now().Unix()}); err != nil {
			return err
		}

		// 归档组：跳过全部协同（§7.2 4d）
		if g.Archived {
			continue
		}
		// auto-wrap（含冷启动，§7.2 3a）
		if len(g.MissingEnvelopes) > 0 {
			if err := e.autoWrap(g); err != nil {
				return err
			}
		}
		// auto-rekey（§7.2 3b）
		if g.PendingRekey {
			if err := e.autoRekey(g); err != nil {
				return err
			}
		}
	}
	return nil
}

// pullGroupFull 指定组全量拉取（since=0&group_id，§6.3/§7.2 4c）。
func (e *Engine) pullGroupFull(groupID string) error {
	resp, err := e.api.Pull(0, map[string]int{groupID: 0})
	if err != nil {
		return err
	}
	if err := e.applyEnvelopes(resp.KeyEnvelopes); err != nil {
		return err
	}
	return e.applyChanges(resp.Changes)
}

// applyChanges 逐条处理变更（§7.2 步骤 4：pending/坏密文/墓碑/正常）。
func (e *Engine) applyChanges(changes []proto.Change) error {
	for i := range changes {
		if err := e.applyOneChange(&changes[i]); err != nil {
			return err
		}
	}
	return nil
}

// applyOneChange 处理单条变更（§7.2 步骤 4；flushPendingEntries 复用）。
func (e *Engine) applyOneChange(c *proto.Change) error {
	if c.Deleted {
		// 墓碑（§7.2 4d / §7.3 #5）：本地有未推送修改 → 转冲突副本（不吞本地修改）；
		// 否则正常删除：回收站 + 移除。本地写失败返回错误，last_seq 不推进，下轮重试。
		if le, err := e.local.GetLocalEntry(c.EntryID); err == nil && le.ConflictOf != "" && c.Seq <= le.Seq {
			return nil // 幂等：同版本墓碑冲突副本已处理（pullGroupFull 与主流程同轮重复），跳过
		}
		if le, err := e.local.GetLocalEntry(c.EntryID); err == nil && le.Dirty {
			le.ConflictOf = c.EntryID // 标记冲突：服务端已删 vs 本地修改
			le.Dirty = false          // 不再自动推送，等待用户决策（冲突解决页）
			le.Seq = c.Seq
			le.Ciphertext = c.Ciphertext // 墓碑密文（空）
			le.UpdatedAt = c.UpdatedAt
			// 保留 plaintext_cache（ours）与 base_enc（base）供冲突解决
			if err := e.local.UpsertLocalEntry(le); err != nil {
				return err
			}
			return e.local.DeletePendingEntry(c.EntryID)
		}
		if le, err := e.local.GetLocalEntry(c.EntryID); err == nil {
			if err := e.local.PutRecycleBin(c.EntryID, le.Ciphertext, e.now().Unix()); err != nil {
				return err
			}
		}
		if err := e.local.RemoveLocalEntry(c.EntryID); err != nil {
			return err
		}
		return e.local.DeletePendingEntry(c.EntryID)
	}
	// 无对应 kv DEK → 暂存 pending_entries（不验 hmac、不进 bad_seq，§7.2 4a）
	dek, err := e.vault.GetDEK(c.GroupID, c.KeyVersion)
	if err != nil {
		// 暂存失败返回错误（last_seq 不推进，防条目丢失），下轮重试
		return e.local.PutPendingEntry(&store.PendingEntry{
			ID: c.EntryID, GroupID: c.GroupID, Seq: c.Seq, KeyVersion: c.KeyVersion,
			Ciphertext: c.Ciphertext, UpdatedAt: c.UpdatedAt,
		})
	}
	crypto.Wipe(dek)

	// 有 DEK：验 HMAC + 解密（§7.2 4b/4c）
	plaintext, err := e.vault.DecryptPlaintext(c.GroupID, c.EntryID, c.Ciphertext)
	if err != nil {
		// 解密失败 → 密文落库 + bad_seq 记录（§7.2 4c，本地重试）；写失败返回错误防条目丢失
		if err := e.local.UpsertLocalEntry(&store.LocalEntry{
			ID: c.EntryID, GroupID: c.GroupID, Seq: c.Seq, KeyVersion: c.KeyVersion,
			Ciphertext: c.Ciphertext, UpdatedAt: c.UpdatedAt,
		}); err != nil {
			return err
		}
		return e.local.MarkBadSeq(c.Seq)
	}
	// 解密成功：明文缓存入库；若本地有未推送修改 → 先尝试字段级三路合并（§7.2 4e/§7.3）
	existing, exErr := e.local.GetLocalEntry(c.EntryID)
	// 幂等：同版本冲突副本已处理（pullGroupFull 与主流程可能同轮重复处理），跳过避免覆盖 ours
	if exErr == nil && existing.ConflictOf != "" && c.Seq <= existing.Seq {
		return nil
	}
	conflictOf := ""
	cache, err := e.vault.EncryptCache(plaintext)
	if err != nil {
		return err
	}
	plainCache := cache
	baseEnc := []byte(nil)
	dirty := false
	if exErr == nil && existing.Dirty {
		// §7.3：字段级自动合并（theirs=刚解密的服务端明文）
		if merged := e.tryAutoMerge(existing, plaintext); merged != nil {
			// 自动合并成功：合并结果为本地最新版，标 dirty 待 push（base_seq=c.Seq）
			mc, err := e.vault.EncryptCache(merged)
			if err != nil {
				return err
			}
			mct, err := e.vault.EncryptPlaintext(c.GroupID, c.EntryID, merged, c.KeyVersion)
			if err != nil {
				return err
			}
			le := &store.LocalEntry{
				ID: c.EntryID, GroupID: c.GroupID, Seq: c.Seq, KeyVersion: c.KeyVersion,
				Ciphertext: mct, PlaintextCache: mc, BaseEnc: existing.BaseEnc,
				Dirty: true, ConflictOf: "", UpdatedAt: c.UpdatedAt,
			}
			if err := e.local.UpsertLocalEntry(le); err != nil {
				return err
			}
			return e.local.DeletePendingEntry(c.EntryID)
		}
		// 有冲突/无法自动合并：标 conflict_of（保留 ours/base/theirs 素材）
		conflictOf = c.EntryID
		plainCache = existing.PlaintextCache
		baseEnc = existing.BaseEnc
	}
	le := &store.LocalEntry{
		ID: c.EntryID, GroupID: c.GroupID, Seq: c.Seq, KeyVersion: c.KeyVersion,
		Ciphertext: c.Ciphertext, Dirty: dirty,
		ConflictOf: conflictOf, UpdatedAt: c.UpdatedAt,
		PlaintextCache: plainCache, BaseEnc: baseEnc,
	}
	if err := e.local.UpsertLocalEntry(le); err != nil {
		return err
	}
	if err := e.local.DeletePendingEntry(c.EntryID); err != nil {
		return err
	}
	return e.local.ClearBadSeq(c.Seq)
}


// pushDirty 收集本地脏条目并推送（§7.2 步骤 5）。
func (e *Engine) pushDirty() error {
	dirty, err := e.local.ListDirtyEntries()
	if err != nil {
		return err
	}
	if len(dirty) == 0 {
		return nil
	}
	var mutations []proto.Mutation
	entryGroup := map[string]string{} // entry_id → group_id（40902 挂起用）
	for i := range dirty {
		le := &dirty[i]
		// 写挂起组跳过（§7.2 写挂起不变量）
		e.mu.Lock()
		held := e.writeHeld[le.GroupID]
		e.mu.Unlock()
		if held {
			continue
		}
		entryGroup[le.ID] = le.GroupID
		mutations = append(mutations, proto.Mutation{
			EntryID: le.ID, GroupID: le.GroupID, BaseSeq: le.Seq,
			KeyVersion: le.KeyVersion, Ciphertext: le.Ciphertext, Deleted: le.Deleted,
		})
	}
	if len(mutations) == 0 {
		return nil
	}
	resp, err := e.api.Push(mutations)
	if err != nil {
		return err
	}
	for _, r := range resp.Results {
		if r.OK {
			// 墓碑推送成功 → 本地彻底移除（§7.2 4d）
			if le, err := e.local.GetLocalEntry(r.EntryID); err == nil && le.Deleted {
				if err := e.local.RemoveLocalEntry(r.EntryID); err != nil {
					return err
				}
				continue
			}
			// 清脏标记 + 清 base_enc 快照（§7.3 #5：push 成功即收敛）+ 更新 seq；失败返回错误（下轮重推幂等）
			if err := e.local.SetDirty(r.EntryID, false); err != nil {
				return err
			}
			if err := e.local.SetBaseEnc(r.EntryID, nil); err != nil {
				return err
			}
			if le, err := e.local.GetLocalEntry(r.EntryID); err == nil {
				le.Seq = r.NewSeq
				if err := e.local.UpsertLocalEntry(le); err != nil {
					return err
				}
			}
		} else if r.Error == proto.ErrConflict {
			// 40901 冲突（§7.3）：本地版转冲突副本、服务端版为当前版。
			// 保留 ours（本地明文 plaintext_cache）+ theirs（服务端明文 base_enc）供三路合并。
			if r.Current != nil {
				if err := e.handlePushConflict(r.EntryID, r.Current); err != nil {
					return err
				}
			} else if err := e.local.SetConflict(r.EntryID, r.EntryID); err != nil {
				return err
			}
		} else if r.Error == proto.ErrKeyVersionStale {
			// 40902：kv 落后 → 挂起该组写入，等 rekey 收敛（§7.2）
			if gid, ok := entryGroup[r.EntryID]; ok {
				e.mu.Lock()
				e.writeHeld[gid] = true
				e.mu.Unlock()
			}
		}
	}
	return nil
}

// tryAutoMerge 尝试字段级三路合并（§7.3）：
// 解密 ours（本地明文缓存）/ base（快照，可空）/ theirs（服务端明文）→ merge.MergeJSON。
// 返回合并结果字节（自动合并成功）或 nil（素材缺失 / 有冲突 / 解密异常，需人工解决）。
func (e *Engine) tryAutoMerge(le *store.LocalEntry, theirs []byte) []byte {
	if len(le.PlaintextCache) == 0 {
		return nil
	}
	ours, err := e.vault.DecryptCache(le.PlaintextCache)
	if err != nil {
		return nil
	}
	var base []byte
	if len(le.BaseEnc) > 0 {
		if bPlain, err := e.vault.DecryptCache(le.BaseEnc); err == nil {
			base = bPlain
		}
	}
	res, err := merge.MergeJSON(base, ours, theirs, merge.Options{})
	if err != nil || res.HasConflict {
		return nil
	}
	return res.Merged
}

// handlePushConflict 处理 40901 push 冲突（§7.3）：
//   - 先尝试字段级三路合并（tryAutoMerge）：不同字段自动合并 → 重新加密标 dirty，下轮 push 收敛；
//   - 同字段冲突/素材缺失 → 标记 conflict_of，供冲突解决页人工解决。
func (e *Engine) handlePushConflict(entryID string, current *proto.Change) error {
	// 本地 ours 明文（若存在）
	le, err := e.local.GetLocalEntry(entryID)
	if err != nil {
		return e.local.SetConflict(entryID, entryID)
	}
	// theirs：服务端当前密文现场解密
	if theirPlain, err := e.vault.DecryptPlaintext(current.GroupID, entryID, current.Ciphertext); err == nil {
		// §7.3：字段级自动合并（不同字段 → 自动收敛，无需人工）
		if merged := e.tryAutoMerge(le, theirPlain); merged != nil {
			if err := e.writeMergedDirty(le, current.GroupID, current.Seq, current.KeyVersion, merged); err == nil {
				return nil
			}
		}
	}
	// 自动合并失败/有冲突 → 标记 conflict_of（保留 ours/base/theirs 素材）
	upd := &store.LocalEntry{
		ID: entryID, GroupID: current.GroupID, Seq: current.Seq,
		KeyVersion: current.KeyVersion, Ciphertext: current.Ciphertext,
		PlaintextCache: le.PlaintextCache, BaseEnc: le.BaseEnc,
		Dirty: false, ConflictOf: entryID, UpdatedAt: current.UpdatedAt,
	}
	return e.local.UpsertLocalEntry(upd)
}

// writeMergedDirty 将自动合并结果写回本地（dirty 待 push，base_seq=服务端当前 seq）。
func (e *Engine) writeMergedDirty(le *store.LocalEntry, groupID string, seq int64, kv int, merged []byte) error {
	cache, err := e.vault.EncryptCache(merged)
	if err != nil {
		return err
	}
	ct, err := e.vault.EncryptPlaintext(groupID, le.ID, merged, kv)
	if err != nil {
		return err
	}
	return e.local.UpsertLocalEntry(&store.LocalEntry{
		ID: le.ID, GroupID: groupID, Seq: seq, KeyVersion: kv, Ciphertext: ct,
		PlaintextCache: cache, BaseEnc: le.BaseEnc,
		Dirty: true, ConflictOf: "", UpdatedAt: e.now().Unix(),
	})
}

// hostnameOf 取本机名（心跳上报用，§8.2；可注入测试）。
var hostnameOf = func(_ *Engine) string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// maybeRefreshToken token 过期自动刷新（§7.2/§P2#11）：
//
//	调服务端 /auth/refresh → 更新 HTTPClient/SSE token → 加密持久化本地 device_state。
func (e *Engine) maybeRefreshToken() error {
	newToken, expiresIn, err := e.api.RefreshToken()
	if err != nil {
		return err
	}
	e.api.SetToken(newToken)
	e.setToken(newToken)
	if expiresIn > 0 {
		e.tokenTTL = expiresIn
	}
	enc, err := e.vault.EncryptToken(newToken)
	if err != nil {
		return err
	}
	ds, err := e.local.GetDeviceState()
	if err != nil {
		return err
	}
	ds.TokenEnc = enc
	if expiresIn > 0 {
		ds.ExpiresAt = e.now().Unix() + expiresIn
	}
	return e.local.SetDeviceState(ds)
}

// tokenExpiringSoon token 是否临近过期（剩余 <10% 或 <60s）：
// 提前刷新让 SSE/同步全程无感（§P2#11：失效前主动续期，而非 401 后补救）。
func (e *Engine) tokenExpiringSoon() bool {
	ds, err := e.local.GetDeviceState()
	if err != nil || ds == nil || ds.ExpiresAt <= 0 {
		return false // 无过期信息：不主动刷（依赖 401 兜底）
	}
	remaining := ds.ExpiresAt - e.now().Unix()
	if remaining <= 60 {
		return true // 已过期或 1 分钟内到期
	}
	if e.tokenTTL > 0 && remaining < e.tokenTTL/10 {
		return true // 剩余不足总有效期 10%
	}
	return false
}

// refreshIfExpiring 提前刷新入口：需要时刷新一次，失败静默（下次再试，不影响主流程）。
func (e *Engine) refreshIfExpiring() {
	if e.tokenExpiringSoon() {
		_ = e.maybeRefreshToken()
	}
}

// authRetry 401 时自动刷新 token 并重试一次（其余错误原样返回）。
func (e *Engine) authRetry(fn func() error) error {
	err := fn()
	if err == nil || !api.IsAuthErr(err) {
		return err
	}
	if rerr := e.maybeRefreshToken(); rerr != nil {
		// 刷新接口也返回认证失败 → token 彻底失效（被吊销/作废），
		// 通知 UI 引导重新解锁（网络类错误不算失效，静默等下次）
		if api.IsAuthErr(rerr) {
			e.emit(api.Event{Type: api.EventAuthExpired, Data: "登录已失效，请重新解锁", At: e.now()})
		}
		return err // 刷新失败返回原 401
	}
	return fn()
}

// localKeyVersions 本地已持有信封版本声明（gid→最高 kv，§6.3 X-Key-Versions）。
// 失败时返回 nil（服务端全量返回信封，客户端幂等处理，正确性无碍）。
func (e *Engine) localKeyVersions() map[string]int {
	kv, err := e.local.ListDEKVersions()
	if err != nil {
		return nil
	}
	return kv
}
