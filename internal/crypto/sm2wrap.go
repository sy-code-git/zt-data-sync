package crypto

import (
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/tjfoc/gmsm/sm2"
	"github.com/tjfoc/gmsm/x509"
)

// sm2C1C3C2 输出模式（§4.2 DEK 包裹：SM2 加密，C1C3C2 模式，曲线 sm2p256v1）。
// gmsm 中 C1C3C2 为包级变量，此处保留数值约定（sm2.C1C3C2 == 0）。
const sm2C1C3C2 = 0

// GenerateSM2Key 生成 SM2 密钥对（曲线 sm2p256v1，随机源 crypto/rand）。
func GenerateSM2Key() (*sm2.PrivateKey, error) {
	priv, err := sm2.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("crypto: sm2.GenerateKey: %w", err)
	}
	return priv, nil
}

// SM2WrapDEK 用公钥包裹数据（§4.2 DEK 包裹：SM2 加密 C1C3C2）。
// 密文为非 ASN.1 的 C1C3C2 原始字节。
func SM2WrapDEK(pub *sm2.PublicKey, dek []byte) ([]byte, error) {
	if pub == nil {
		return nil, errors.New("crypto: sm2 公钥为空")
	}
	ct, err := sm2.Encrypt(pub, dek, rand.Reader, sm2C1C3C2)
	if err != nil {
		return nil, fmt.Errorf("crypto: sm2.Encrypt: %w", err)
	}
	return ct, nil
}

// sm2MinCipherLen SM2-C1C3C2 密文最小长度。
// gmsm Encrypt 输出 = 0x04 前缀(1B) || C1(x1||y1, 64B) || C3(SM3, 32B) || C2(明文)。
// 因此最小合法长度 = 1 + 64 + 32 = 97（加密空数据时）。低于此值必为非法密文，
// 直接拒绝——避免进入 gmsm Decrypt 后 len(data)-96 为负导致负数 make/slice panic。
const sm2MinCipherLen = 97

// SM2UnwrapDEK 用私钥解包数据（§4.2）。
func SM2UnwrapDEK(priv *sm2.PrivateKey, wrapped []byte) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("crypto: sm2 私钥为空")
	}
	if len(wrapped) < sm2MinCipherLen {
		return nil, fmt.Errorf("crypto: sm2 密文长度 %d 非法（最短 %d）", len(wrapped), sm2MinCipherLen)
	}
	pt, err := sm2.Decrypt(priv, wrapped, sm2C1C3C2)
	if err != nil {
		return nil, fmt.Errorf("crypto: sm2.Decrypt: %w", err)
	}
	return pt, nil
}

// SM2SignChallenge 用私钥签名（SM3withSM2，uid 用国标默认值，§4.2 设备注册签名）。
// 返回 ASN.1 DER 编码签名（gmsm sm2Signature 结构）。
func SM2SignChallenge(priv *sm2.PrivateKey, msg []byte) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("crypto: sm2 私钥为空")
	}
	sig, err := priv.Sign(rand.Reader, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: sm2 sign: %w", err)
	}
	return sig, nil
}

// SM2VerifyChallenge 用公钥验签（§4.2）。签名须为 ASN.1 DER 编码。
func SM2VerifyChallenge(pub *sm2.PublicKey, msg, sig []byte) bool {
	if pub == nil {
		return false
	}
	return pub.Verify(msg, sig)
}

// MarshalSM2PublicKey 序列化公钥为 DER（base64(DER) 为 wire format，§4.3/§6.3）。
func MarshalSM2PublicKey(pub *sm2.PublicKey) ([]byte, error) {
	if pub == nil {
		return nil, errors.New("crypto: sm2 公钥为空")
	}
	der, err := x509.MarshalSm2PublicKey(pub)
	if err != nil {
		return nil, fmt.Errorf("crypto: marshal sm2 pubkey: %w", err)
	}
	return der, nil
}

// UnmarshalSM2PublicKey 反序列化公钥 DER。
// 额外校验"点在曲线上"：gmsm ParseSm2PublicKey 不验证点合法性，畸形公钥
// （X/Y 为 nil 或不在曲线上）会导致后续 SM2 运算 panic 或包裹出不可解密信封。
// 本函数是服务端网络输入面（设备注册 / 建用户），必须严格校验。
func UnmarshalSM2PublicKey(der []byte) (*sm2.PublicKey, error) {
	pub, err := x509.ParseSm2PublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("crypto: parse sm2 pubkey: %w", err)
	}
	if pub.X == nil || pub.Y == nil || !pub.Curve.IsOnCurve(pub.X, pub.Y) {
		return nil, errors.New("crypto: sm2 公钥点不在曲线上")
	}
	return pub, nil
}

// MarshalSM2PrivateKey 序列化私钥为 DER（SEC1 未加密；keyfile 中由 KEK 加密存储，§4.3）。
func MarshalSM2PrivateKey(priv *sm2.PrivateKey) ([]byte, error) {
	if priv == nil {
		return nil, errors.New("crypto: sm2 私钥为空")
	}
	der, err := x509.MarshalSm2UnecryptedPrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("crypto: marshal sm2 privkey: %w", err)
	}
	return der, nil
}

// UnmarshalSM2PrivateKey 反序列化私钥 DER（PKCS8，与 MarshalSM2PrivateKey 配对）。
func UnmarshalSM2PrivateKey(der []byte) (*sm2.PrivateKey, error) {
	priv, err := x509.ParsePKCS8UnecryptedPrivateKey(der)
	if err != nil {
		return nil, fmt.Errorf("crypto: parse sm2 privkey: %w", err)
	}
	return priv, nil
}
