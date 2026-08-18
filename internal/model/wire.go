package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Wire 相关结构（§4.3 wire format），用于线上/落盘序列化。

// CiphertextAlg 条目加密算法标识（§4.3）。
const CiphertextAlg = "SM4-GCM"

// EnvelopeAlg 信封包裹算法标识（§4.3）。
const EnvelopeAlg = "SM2-C1C3C2"

// WireVersion 当前 wire format 版本（§4.3 全部带 v 字段，前向兼容跳过未知字段）。
const WireVersion = 1

// Ciphertext 条目密文包（§4.3 entries.ciphertext 列，JSON 序列化后存 TEXT）。
type Ciphertext struct {
	V     int    `json:"v"`
	Alg   string `json:"alg"`
	KV    int    `json:"kv"`    // key_version，加密时使用的 DEK 版本
	Nonce []byte `json:"nonce"` // base64(12B)
	CT    []byte `json:"ct"`    // base64(...)
	HMAC  []byte `json:"hmac"`  // base64(32B)，HMAC-SM3
}

// NewCiphertext 构造密文包。
func NewCiphertext(kv int, nonce, ct, hmac []byte) *Ciphertext {
	return &Ciphertext{
		V:     WireVersion,
		Alg:   CiphertextAlg,
		KV:    kv,
		Nonce: nonce,
		CT:    ct,
		HMAC:  hmac,
	}
}

// MarshalCiphertext 序列化密文包为 JSON 字节。
func MarshalCiphertext(c *Ciphertext) ([]byte, error) {
	if c == nil {
		return nil, errors.New("model: ciphertext 为 nil")
	}
	return json.Marshal(c)
}

// ParseCiphertext 解析密文包 JSON。
// 前向兼容：忽略不认识的字段（§14.1 工程规范 #7）。
func ParseCiphertext(data []byte) (*Ciphertext, error) {
	var c Ciphertext
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("model: 解析 ciphertext: %w", err)
	}
	if c.V < 1 || c.V > WireVersion {
		return nil, fmt.Errorf("model: ciphertext 版本 %d 非法（本地支持 1..%d）", c.V, WireVersion)
	}
	if c.Alg != CiphertextAlg {
		return nil, fmt.Errorf("model: 不支持的算法 %q", c.Alg)
	}
	return &c, nil
}

// KeyEnvelope 密钥信封（§4.3 key_envelopes.wrapped_dek 列）。
type KeyEnvelope struct {
	V   int    `json:"v"`
	Alg string `json:"alg"`
	// Data 为 base64(SM2-C1C3C2 加密 DEK 的结果)，密文最小长度见 crypto 层校验。
	Data []byte `json:"data"`
}

// NewKeyEnvelope 构造信封。
func NewKeyEnvelope(data []byte) *KeyEnvelope {
	return &KeyEnvelope{
		V:    WireVersion,
		Alg:  EnvelopeAlg,
		Data: data,
	}
}

// MarshalEnvelope 序列化信封为 JSON 字节。
func MarshalEnvelope(e *KeyEnvelope) ([]byte, error) {
	if e == nil {
		return nil, errors.New("model: envelope 为 nil")
	}
	return json.Marshal(e)
}

// ParseEnvelope 解析信封 JSON（前向兼容未知字段）。
func ParseEnvelope(data []byte) (*KeyEnvelope, error) {
	var e KeyEnvelope
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("model: 解析 envelope: %w", err)
	}
	if e.V < 1 || e.V > WireVersion {
		return nil, fmt.Errorf("model: envelope 版本 %d 非法（本地支持 1..%d）", e.V, WireVersion)
	}
	if e.Alg != EnvelopeAlg {
		return nil, fmt.Errorf("model: 不支持的包裹算法 %q", e.Alg)
	}
	return &e, nil
}
