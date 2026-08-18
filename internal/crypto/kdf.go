package crypto

import (
	"encoding/binary"

	"github.com/tjfoc/gmsm/sm3"
	"golang.org/x/crypto/pbkdf2"
)

const (
	// PBKDF2Iter 迭代次数（§4.2：100000）
	PBKDF2Iter = 100000
	// KEKSize KEK 长度（§4.2：输出 16B）
	KEKSize = 16
	// SaltSize keyfile 口令派生盐长度（§4.2：salt=16B 随机）
	SaltSize = 16
)

// pbkdf2SM3 以 SM3 为哈希的 PBKDF2（HMAC-SM3 构造）。
func pbkdf2SM3(password, salt []byte, iter, keyLen int) []byte {
	return pbkdf2.Key(password, salt, iter, keyLen, sm3.New)
}

// DeriveKEK 由 keyfile 口令派生 KEK（§4.2 keyfile 派生）。
// salt 由调用方持有（存于 keyfile.kdf.salt），迭代固定 PBKDF2Iter。
// 返回的新切片由调用方负责 Wipe。
func DeriveKEK(password, salt []byte, iter int) []byte {
	if iter <= 0 {
		iter = PBKDF2Iter
	}
	return pbkdf2SM3(password, salt, iter, KEKSize)
}

// EncodeUint64 长度前缀编码辅助：uint64 大端（供 seq 等整数进 HMAC/长度前缀流时使用）。
func EncodeUint64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}
