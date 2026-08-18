package crypto

import (
	"crypto/hmac"
	"crypto/subtle"
	"errors"

	"github.com/tjfoc/gmsm/sm3"
)

// HMACKeySize HMAC-SM3 输出长度（32 字节）。
const HMACKeySize = 32

// hmacDeriveKey 由根密钥派生 HMAC 专用密钥：SM3(key || label) 前 16 字节（§4.2 条目防伪章）。
// 派生过程产生的完整哈希（32B）在拷贝出前 16B 后立即清零，避免密钥材料残留内存
// （§14.1 不变量 #2 使用后立即覆写）。
func hmacDeriveKey(root []byte, label string) []byte {
	h := sm3.New()
	h.Write(root)
	h.Write([]byte(label))
	sum := h.Sum(nil)
	out := make([]byte, 16)
	copy(out, sum[:16])
	Wipe(sum)
	return out
}

// EntryHMACKey 由 DEK 派生条目 HMAC 密钥（§4.2：key = SM3(DEK 拼接 "hmac") 前 16 字节）。
func EntryHMACKey(dek []byte) []byte {
	return hmacDeriveKey(dek, "hmac")
}

// HMACSM3 计算 HMAC-SM3（标准 HMAC 构造 + SM3 哈希），输出 32 字节。
func HMACSM3(key, msg []byte) []byte {
	mac := hmac.New(sm3.New, key)
	mac.Write(msg)
	return mac.Sum(nil)
}

// ConstantTimeEqual 常量时间比较，防止时序侧信道（§4.2 HMAC 比较用 hmac.Equal 的常量时间语义）。
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}

// VerifyHMACSM3 校验 HMAC-SM3 标签（常量时间比较）。
func VerifyHMACSM3(key, msg, tag []byte) bool {
	want := HMACSM3(key, msg)
	if len(want) != len(tag) {
		return false
	}
	return ConstantTimeEqual(want, tag)
}

// deriveSM3Subkey 通用派生：SM3(root || label) 前 16 字节。
// 用于本地敏感列派生（plaintext-cache / device-token / base 等，§9.1），
// 不同标签密钥隔离。
func deriveSM3Subkey(root []byte, label string) []byte {
	return hmacDeriveKey(root, label)
}

// DeriveKEKSubkey 由 KEK 派生本地数据加密子密钥（§9.1：不同派生标签密钥隔离）。
func DeriveKEKSubkey(kek []byte, label string) []byte {
	return deriveSM3Subkey(kek, label)
}

// EntryHMAC 计算条目防伪章（§4.3）：
//
//	hmac = HMAC-SM3(EntryHMACKey(dek), 长度前缀编码(entry_id | group_id | kv | nonce | ct))
//
// 其中 kv 以 uint64 大端 8 字节编码（§4.3 未明示宽度，本项目统一为 8B 定宽，
// 见 docs 设计文档 §4.3 与 README 同步约定）。
//
// ★ 跨端一致性铁律：条目由 A 端加密写入、B 端拉取校验，所有客户端必须调用本函数
// 计算/校验 HMAC，禁止各自组装输入，否则合法条目会被误判为篡改。
// 返回的 HMAC 为 32B；调用方负责 Wipe dek。
func EntryHMAC(dek []byte, entryID, groupID string, kv int, nonce, ct []byte) []byte {
	key := EntryHMACKey(dek)
	defer Wipe(key)
	// kv 为组 key_version，正常调用恒为正（服务端校验 ≥ 当前 kv ≥ 1）；
	// 防御性钳制负数，避免 int→uint64 溢出语义（gosec G115）
	if kv < 0 {
		kv = 0
	}
	// #nosec G115 -- 上方已钳制 kv ≥ 0，此转换安全
	msg := LengthPrefixed([]byte(entryID), []byte(groupID), EncodeUint64(uint64(kv)), nonce, ct)
	return HMACSM3(key, msg)
}

// ErrInvalidKeySize 派生上下文密钥长度不合法。
var ErrInvalidKeySize = errors.New("crypto: 派生密钥长度必须为 16 字节")

// SM3 计算 SM3 摘要（32 字节）。
// 用于 token 哈希（§8.2：库中只存 SM3(token)）等非密钥场景。
func SM3(data []byte) []byte {
	h := sm3.New()
	h.Write(data)
	return h.Sum(nil)
}
