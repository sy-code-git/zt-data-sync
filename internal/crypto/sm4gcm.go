package crypto

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/tjfoc/gmsm/sm4"
)

const (
	// SM4KeySize SM4-GCM 密钥长度（§4.2：key=DEK(16B)）
	SM4KeySize = 16
	// SM4NonceSize GCM nonce 长度（§4.2：nonce=12B 随机）
	SM4NonceSize = 12
)

var errBadKey = errors.New("crypto: SM4 密钥必须为 16 字节")

// Random 生成 n 字节密码学安全随机数（唯一随机源 crypto/rand，§14.1 不变量 #3）。
func Random(n int) ([]byte, error) {
	if n < 0 {
		return nil, fmt.Errorf("crypto: 随机数长度 %d 非法", n)
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

// lpAppend 追加一个"长度前缀编码"字段（uint32 大端长度 + 内容），
// 用于 AAD 与 HMAC 输入（§4.2 长度前缀编码，消除字段边界歧义）。
func lpAppend(dst []byte, field []byte) []byte {
	// 防护 uint32 溢出（gosec G115）：单字段超 4GB 视为异常，直接拒绝编码
	const maxUint32 = 1<<32 - 1
	if uint64(len(field)) > maxUint32 {
		return dst
	}
	var lb [4]byte
	// #nosec G115 -- 上方已用 uint64 比较完成长度上限防护，此转换安全
	binary.BigEndian.PutUint32(lb[:], uint32(len(field)))
	dst = append(dst, lb[:]...)
	dst = append(dst, field...)
	return dst
}

// LengthPrefixed 将若干字段编码为"长度前缀编码"字节流：
// 每个字段 = uint32(长度) || 内容，依次拼接。
func LengthPrefixed(fields ...[]byte) []byte {
	var out []byte
	for _, f := range fields {
		out = lpAppend(out, f)
	}
	return out
}

// SM4GCMSeal 用 SM4-GCM 加密（§4.2 条目加密）。
// 参数：
//   - key：DEK，16 字节
//   - aad：附加认证数据（客户端按 §4.2 传长度前缀编码的 entry_id||group_id）
//   - plaintext：明文
//
// nonce 由函数内部用 crypto/rand 生成，随密文一并返回。
func SM4GCMSeal(key, aad, plaintext []byte) (nonce, ciphertext []byte, err error) {
	if len(key) != SM4KeySize {
		return nil, nil, errBadKey
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: sm4.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("crypto: cipher.NewGCM: %w", err)
	}
	nonce, err = Random(SM4NonceSize)
	if err != nil {
		return nil, nil, err
	}
	return nonce, gcm.Seal(nil, nonce, plaintext, aad), nil
}

// SM4GCMOpen 用 SM4-GCM 解密，返回明文。
// 任一参数被篡改（nonce/ciphertext/aad/key）均会导致认证失败并返回错误。
func SM4GCMOpen(key, nonce, aad, ciphertext []byte) ([]byte, error) {
	if len(key) != SM4KeySize {
		return nil, errBadKey
	}
	if len(nonce) != SM4NonceSize {
		return nil, fmt.Errorf("crypto: nonce 必须为 %d 字节，实际 %d", SM4NonceSize, len(nonce))
	}
	// GCM 密文至少含 16B 认证 tag；空/过短密文直接拒绝，
	// 不依赖标准库对短密文的行为（历史版本曾 panic，防御性兜底）
	if len(ciphertext) < 16 {
		return nil, fmt.Errorf("crypto: SM4-GCM 密文长度 %d 非法（最短含 tag 16 字节）", len(ciphertext))
	}
	block, err := sm4.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("crypto: sm4.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: cipher.NewGCM: %w", err)
	}
	pt, err := gcm.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("crypto: SM4-GCM 认证失败: %w", err)
	}
	return pt, nil
}
