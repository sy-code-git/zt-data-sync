package authn

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"

	"github.com/tjfoc/gmsm/sm2"

	"passbook/internal/crypto"
)

// Error 带错误码的业务错误（供 handler 映射 §13 错误码）。
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return e.Message }

// errCode 构造带错误码的错误。
func errCode(code int, msg string) error {
	return &Error{Code: code, Message: msg}
}

// NewError 导出构造函数（middleware/测试用）。
func NewError(code int, msg string) error {
	return &Error{Code: code, Message: msg}
}

// CodeOf 提取错误码（非 Error 类型返回 50001 内部错误）。
func CodeOf(err error) int {
	if e, ok := err.(*Error); ok {
		return e.Code
	}
	return 50001
}

// newID 生成 UUID v4（随机 16 字节，格式 8-4-4-4-12）。
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("authn: crypto/rand 失败: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]),
		hex.EncodeToString(b[6:8]), hex.EncodeToString(b[8:10]),
		hex.EncodeToString(b[10:16]))
}

// parseSM2PublicKey 解析 base64(DER) 公钥（crypto 层已校验点在曲线上，§4.3/§6.3）。
func parseSM2PublicKey(b64 string) (*sm2.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("authn: 公钥 base64 解码失败: %w", err)
	}
	pub, err := crypto.UnmarshalSM2PublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("authn: 解析公钥: %w", err)
	}
	return pub, nil
}

// verifySM2 验签（SM3withSM2，§4.2 设备注册签名）。
func verifySM2(pub *sm2.PublicKey, msg, sig []byte) bool {
	return crypto.SM2VerifyChallenge(pub, msg, sig)
}

// decodeBase64 标准 base64 解码。
func decodeBase64(s string) ([]byte, error) {
	return base64.StdEncoding.DecodeString(s)
}
