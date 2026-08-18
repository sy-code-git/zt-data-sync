package authn

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"sync"
	"time"

	"passbook/internal/proto"
	"passbook/server/store"
)

// Service 认证服务（§8）。依赖 Store 接口，可换内存假实现测试。
type Service struct {
	store store.Store

	// bootstrap 一次性引导 token（PB_BOOTSTRAP_CODE 或启动时生成，用后即毁，§12.2）
	bootstrapCode string
	bootstrapUsed bool
	bootstrapMu   sync.Mutex

	// 设备注册挑战（内存态，绑定 username 工号，5 分钟过期一次性，§6.3）
	challengesMu sync.Mutex
	challenges   map[string]pendingChallenge

	now func() time.Time
}

type pendingChallenge struct {
	challenge string // base64(32B)
	expiresAt int64  // unix 秒
}

// Options 构造参数（依赖注入：时间源可替换，便于测试）。
type Options struct {
	BootstrapCode string
	Now           func() time.Time
}

// New 构造认证服务。bootstrapCode 为空时生成随机一次性值。
func New(s store.Store, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Service{
		store:         s,
		bootstrapCode: opts.BootstrapCode,
		challenges:    map[string]pendingChallenge{},
		now:           opts.Now,
	}
}

// Bootstrap 首启引导（§6.3 POST /auth/bootstrap）：
//   - users 表为空 且 token 与首启生成值一致（一次性）
//   - 单事务：建 admin 用户、建设备、签发 token；此后本接口永久不可用
func (s *Service) Bootstrap(ctx context.Context, req *proto.BootstrapRequest) (*proto.BootstrapResponse, error) {
	// 一次性 token 校验（常量时间比较，防时序侧信道）
	s.bootstrapMu.Lock()
	defer s.bootstrapMu.Unlock()
	if s.bootstrapUsed {
		return nil, errCode(proto.ErrBadBootstrap, "bootstrap token 已使用")
	}
	if subtle.ConstantTimeCompare([]byte(s.bootstrapCode), []byte(req.BootstrapToken)) != 1 {
		return nil, errCode(proto.ErrBadBootstrap, "bootstrap token 无效")
	}

	n, err := s.store.GetUserCount()
	if err != nil {
		return nil, err
	}
	if n != 0 {
		return nil, errCode(proto.ErrBadBootstrap, "users 表非空，bootstrap 已失效")
	}

	now := s.now().Unix()
	uid := newID()
	did := newID()
	tokenRaw, tokenHash, err := GenerateToken()
	if err != nil {
		return nil, err
	}

	err = s.store.WithTx(ctx, func(tx store.Tx) error {
		if err := tx.CreateUser(&store.User{
			ID: uid, Username: req.Username, Name: req.Name, SM2PublicKey: req.SM2PublicKey,
			Role: store.RoleAdmin, Status: store.StatusActive, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("authn: bootstrap 建用户: %w", err)
		}
		if err := tx.CreateDevice(&store.Device{
			ID: did, UserID: uid, Name: req.DeviceName, TokenHash: tokenHash,
			Status: store.DeviceActive, CreatedAt: now,
		}); err != nil {
			return fmt.Errorf("authn: bootstrap 建设备: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.bootstrapUsed = true
	return &proto.BootstrapResponse{
		UserID: uid, Username: req.Username, DeviceID: did, Token: tokenRaw,
		Role: store.RoleAdmin, ExpiresIn: TokenTTL,
	}, nil
}

// CreateChallenge 生成设备注册挑战（§6.3 POST /auth/device-challenge）。
// challenge 绑定 username（工号），5 分钟过期、一次性。
func (s *Service) CreateChallenge(ctx context.Context, username string) (*proto.DeviceChallengeResponse, error) {
	u, err := s.store.GetUserByUsername(username)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			return nil, errCode(proto.ErrBadRequest, "用户不存在")
		}
		return nil, err
	}
	if u.Status != store.StatusActive {
		return nil, errCode(proto.ErrUserRevoked, "用户已吊销")
	}

	challenge, err := GenerateChallenge()
	if err != nil {
		return nil, err
	}

	now := s.now().Unix()
	s.challengesMu.Lock()
	// 惰性清理过期挑战，防止 map 无限增长（P2 DoS 面：恶意调用方可无限取挑战）
	if len(s.challenges) > 1000 {
		for uid, pc := range s.challenges {
			if now > pc.expiresAt {
				delete(s.challenges, uid)
			}
		}
	}
	s.challenges[username] = pendingChallenge{
		challenge: challenge,
		expiresAt: now + ChallengeTTL,
	}
	s.challengesMu.Unlock()

	return &proto.DeviceChallengeResponse{
		Challenge: challenge,
		ExpiresIn: ChallengeTTL,
	}, nil
}

// RegisterDevice 设备注册（§6.3 POST /auth/device）：
//   - 核销一次性 challenge（与注册同事务语义：校验通过即删除）
//   - 服务端用库中用户公钥验签（SM3withSM2）
//   - 验签通过建设备、发 token；库中只存 SM3(token)
func (s *Service) RegisterDevice(ctx context.Context, req *proto.DeviceRegisterRequest) (*proto.DeviceRegisterResponse, error) {
	// 1. 核销 challenge（内存态，一次性）
	s.challengesMu.Lock()
	pc, ok := s.challenges[req.Username]
	if ok {
		delete(s.challenges, req.Username)
	}
	s.challengesMu.Unlock()
	if !ok {
		return nil, errCode(proto.ErrBadChallenge, "challenge 无效/已使用")
	}
	if s.now().Unix() > pc.expiresAt {
		return nil, errCode(proto.ErrBadChallenge, "challenge 已过期")
	}
	if pc.challenge != req.Challenge {
		return nil, errCode(proto.ErrBadChallenge, "challenge 不匹配")
	}

	// 2. 查用户（按工号）+ 验签
	u, err := s.store.GetUserByUsername(req.Username)
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			return nil, errCode(proto.ErrBadRequest, "用户不存在")
		}
		return nil, err
	}
	if u.Status != store.StatusActive {
		return nil, errCode(proto.ErrUserRevoked, "用户已吊销")
	}
	pub, err := parseSM2PublicKey(u.SM2PublicKey)
	if err != nil {
		return nil, err
	}
	sig, err := decodeBase64(req.Signature)
	if err != nil {
		return nil, errCode(proto.ErrBadRequest, "签名格式错误")
	}
	if !verifySM2(pub, []byte(pc.challenge), sig) {
		return nil, errCode(proto.ErrBadSignature, "签名校验失败")
	}

	// 3. 建设备 + token（单事务）
	now := s.now().Unix()
	did := newID()
	tokenRaw, tokenHash, err := GenerateToken()
	if err != nil {
		return nil, err
	}
	err = s.store.WithTx(ctx, func(tx store.Tx) error {
		return tx.CreateDevice(&store.Device{
			ID: did, UserID: u.ID, Name: req.DeviceName, Hostname: req.Hostname,
			TokenHash: tokenHash, Status: store.DeviceActive, CreatedAt: now,
		})
	})
	if err != nil {
		return nil, fmt.Errorf("authn: 注册设备: %w", err)
	}

	return &proto.DeviceRegisterResponse{
		DeviceID: did, Token: tokenRaw, ExpiresIn: TokenTTL,
	}, nil
}
