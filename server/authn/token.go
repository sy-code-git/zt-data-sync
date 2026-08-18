// Package authn 认证与设备管理（§6.1 server/authn / §8）。
// 首启 bootstrap、设备挑战、设备注册、token 签发。
package authn

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"passbook/internal/crypto"
)

const (
	// TokenBytes token 长度（§8.2：32B(256bit) 随机）。
	TokenBytes = 32
	// ChallengeBytes 设备注册挑战长度（§6.3：32B 随机）。
	ChallengeBytes = 32
	// ChallengeTTL 挑战有效期秒数（§6.3：5 分钟）。
	ChallengeTTL = 300
	// TokenTTL 默认 token 有效期秒数（§8.2：30 天，可由 PB_TOKEN_TTL 覆盖）。
	TokenTTL = 30 * 24 * 3600
)

// GenerateToken 生成设备 token（§8.2：32B 随机，base64url）。
// 返回原始 token（仅此一次可见）与其 SM3 哈希（落库值）。
func GenerateToken() (raw, hash string, err error) {
	b, err := crypto.Random(TokenBytes)
	if err != nil {
		return "", "", fmt.Errorf("authn: 生成 token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, HashToken(raw), nil
}

// HashToken 计算 token 的 SM3 哈希（§8.2：库中只存 SM3(token)，token 本体不落库）。
func HashToken(raw string) string {
	return hex.EncodeToString(crypto.SM3([]byte(raw)))
}

// GenerateChallenge 生成设备注册挑战（§6.3：base64(32B 随机)）。
func GenerateChallenge() (string, error) {
	b, err := crypto.Random(ChallengeBytes)
	if err != nil {
		return "", fmt.Errorf("authn: 生成 challenge: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}
