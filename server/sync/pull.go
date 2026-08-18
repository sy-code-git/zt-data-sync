// Package sync 同步引擎的服务端部分（§6.1 server/sync）。
// seq 分配、冲突检测、墓碑清理、信封保留规则、SSE 通知。
// 服务端只做状态与集合校验，不参与任何加解密（§6.4 硬规则 #7）。
package sync

import (
	"context"
	"errors"
	"sort"
	"time"

	"passbook/internal/proto"
	"passbook/server/store"
)

const (
	// PullLimit 单次拉取上限（§6.3：按 seq 升序、单次上限 500 条）。
	PullLimit = 500
	// MaxCiphertext 单条密文包上限（§5.1：256KB）。
	MaxCiphertext = 256 * 1024
	// TombstoneCleanDays 墓碑保留天数（§7.4：90 天）。
	TombstoneCleanDays = 90
)

// Service 同步服务（依赖 Store 接口）。
type Service struct {
	store store.Store
	hub   *Hub
	now   func() time.Time
}

// New 构造同步服务。
func New(s store.Store, hub *Hub, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: s, hub: hub, now: now}
}

// ErrCode 业务错误码包装（供 api 层映射 HTTP）。
type ErrCode struct {
	Code    int
	Message string
}

func (e *ErrCode) Error() string { return e.Message }

func errCode(code int, msg string) error {
	return &ErrCode{Code: code, Message: msg}
}

// CodeOf 提取错误码。
func CodeOf(err error) int {
	var ec *ErrCode
	if errors.As(err, &ec) {
		return ec.Code
	}
	return proto.ErrInternal
}

// Pull 增量拉取（§6.3 GET /sync）。
// userID：请求者；since：全局游标；groupID：可选单组拉取；keyVersions：X-Key-Versions 声明。
func (s *Service) Pull(ctx context.Context, userID string, since int64, groupID string, keyVersions map[string]int) (*proto.SyncResponse, error) {
	// since 负数 clamp 为 0（等价全量；防御恶意/异常参数）
	if since < 0 {
		since = 0
	}
	userGroups, err := s.store.ListUserGroups(userID)
	if err != nil {
		return nil, err
	}
	if groupID != "" {
		// 指定组：校验成员（§6.3 指定时校验请求者为该组成员，否则 40302）
		ok, err := s.store.GetGroupMember(groupID, userID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errCode(proto.ErrNotMember, "非该组成员")
		}
		userGroups = filterGroups(userGroups, groupID)
	}

	// 变更：全部新变更，内存过滤到请求者所在组
	changes, err := s.store.PullChanges(since, PullLimit)
	if err != nil {
		return nil, err
	}
	inGroups := groupSet(userGroups)
	var myChanges []proto.Change
	for _, e := range changes {
		if inGroups[e.GroupID] {
			myChanges = append(myChanges, proto.Change{
				EntryID: e.ID, GroupID: e.GroupID, Seq: e.Seq, KeyVersion: e.KeyVersion,
				Ciphertext: e.Ciphertext, Deleted: e.Deleted, UpdatedAt: e.UpdatedAt,
			})
		}
	}

	// 我的新信封：user_id=自己 且比声明版本新（§6.3）
	envs, err := s.store.GetUserEnvelopes(userID)
	if err != nil {
		return nil, err
	}
	var myEnvs []proto.KeyEnvelopeInfo
	for _, e := range envs {
		if groupID != "" && e.GroupID != groupID {
			continue
		}
		if decl, ok := keyVersions[e.GroupID]; ok && e.KeyVersion <= decl {
			continue
		}
		myEnvs = append(myEnvs, proto.KeyEnvelopeInfo{GroupID: e.GroupID, KeyVersion: e.KeyVersion, WrappedDEK: e.WrappedDEK})
	}

	// 组协同状态（pending_rekey / missing_envelopes / archived）
	groupsState, err := s.groupStates(ctx, userGroups)
	if err != nil {
		return nil, err
	}

	serverSeq, err := s.store.GetServerSeq()
	if err != nil {
		return nil, err
	}

	return &proto.SyncResponse{
		ServerSeq:    serverSeq,
		Changes:      myChanges,
		KeyEnvelopes: myEnvs,
		Groups:       groupsState,
	}, nil
}

func filterGroups(gs []store.Group, gid string) []store.Group {
	for _, g := range gs {
		if g.ID == gid {
			return []store.Group{g}
		}
	}
	return nil
}

func groupSet(gs []store.Group) map[string]bool {
	m := make(map[string]bool, len(gs))
	for _, g := range gs {
		m[g.ID] = true
	}
	return m
}

// groupStates 计算各组协同状态（§6.3 groups 携带 pending_rekey/missing_envelopes/archived）。
func (s *Service) groupStates(ctx context.Context, groups []store.Group) ([]proto.GroupState, error) {
	out := make([]proto.GroupState, 0, len(groups))
	for _, g := range groups {
		missing, active, err := s.missingEnvelopes(ctx, g)
		if err != nil {
			return nil, err
		}
		out = append(out, proto.GroupState{
			ID:               g.ID,
			Name:             g.Name,
			KeyVersion:       g.KeyVersion,
			PendingRekey:     g.PendingRekey == store.RekeyPending,
			Archived:         g.Archived == store.GroupArchived,
			MissingEnvelopes: missing,
			ActiveMembers:    active,
		})
	}
	return out, nil
}

// missingEnvelopes 计算组当前 kv 下无信封的 active 成员（§6.3/§7.2 3a），
// 同时返回该组全部 active 成员（§7.2 auto-rekey 包裹对象）。
// 冷启动：组当前 kv 无任何信封 → missing 返回全部 active 成员（首把 DEK 待生成）。
// 归档组：不产生 missing_envelopes，active 亦为空（§6.3 归档语义）。
func (s *Service) missingEnvelopes(ctx context.Context, g store.Group) (missing, active []string, err error) {
	if g.Archived == store.GroupArchived {
		return nil, nil, nil
	}
	members, err := s.store.ListGroupMembers(g.ID)
	if err != nil {
		return nil, nil, err
	}
	// active 成员集合（排序保证确定性，§14.1）
	for _, m := range members {
		u, err := s.store.GetUserByID(m.UserID)
		if err != nil {
			if errors.Is(err, store.ErrNoRows) {
				continue
			}
			return nil, nil, err
		}
		if u.Status == store.StatusActive {
			active = append(active, m.UserID)
		}
	}
	sort.Strings(active)
	// 冷启动判定：组当前 kv 是否有任何信封
	envs, err := s.store.GetGroupEnvelopes(g.ID, g.KeyVersion)
	if err != nil {
		return nil, nil, err
	}
	if len(envs) == 0 {
		// 冷启动：全部 active 成员缺信封
		return active, active, nil
	}
	has := map[string]bool{}
	for _, e := range envs {
		has[e.UserID] = true
	}
	for _, uid := range active {
		if !has[uid] {
			missing = append(missing, uid)
		}
	}
	sort.Strings(missing)
	return missing, active, nil
}
