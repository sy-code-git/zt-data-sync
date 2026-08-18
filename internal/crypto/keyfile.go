package crypto

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// KeyfileAAD keyfile 私钥加密的附加认证数据（§4.2 本地私钥加密 AAD="passbook-keyfile-v1"）。
const KeyfileAAD = "passbook-keyfile-v1"

// KeyfileKDF keyfile 的 KDF 参数（§4.3）。
type KeyfileKDF struct {
	Alg  string `json:"alg"`            // "PBKDF2-SM3"
	Salt []byte `json:"salt"`           // base64(16B)
	Iter int    `json:"iter"`           // 100000
}

// Keyfile keyfile 文件结构（§4.3 keyfile，xxx.key，JSON 序列化）。
type Keyfile struct {
	V     int        `json:"v"`     // 1
	KDF   KeyfileKDF `json:"kdf"`   //
	Nonce []byte     `json:"nonce"` // base64(12B)
	CT    []byte     `json:"ct"`    // base64(SM4-GCM 密文，明文为 SM2 私钥 DER)
}

// NewKeyfile 用口令保护私钥 DER，生成 keyfile（§4.3）。
// 流程：随机 salt → PBKDF2-SM3 派生 KEK → SM4-GCM(KEK, AAD="passbook-keyfile-v1") 加密私钥 DER。
// 返回的 Keyfile 不持有 KEK/私钥明文；调用方负责 Wipe 输入。
func NewKeyfile(privDER, password []byte) (*Keyfile, error) {
	salt, err := Random(SaltSize)
	if err != nil {
		return nil, err
	}
	kek := DeriveKEK(password, salt, PBKDF2Iter)
	defer Wipe(kek)

	nonce, ct, err := SM4GCMSeal(kek, []byte(KeyfileAAD), privDER)
	if err != nil {
		return nil, err
	}

	return &Keyfile{
		V:     1,
		KDF:   KeyfileKDF{Alg: "PBKDF2-SM3", Salt: salt, Iter: PBKDF2Iter},
		Nonce: nonce,
		CT:    ct,
	}, nil
}

// DecryptPrivateKey 用口令解出私钥 DER（§4.3）。
// 口令错误或数据被篡改均返回错误（SM4-GCM 认证失败）。
// 返回的 DER 由调用方负责 Wipe。
func (k *Keyfile) DecryptPrivateKey(password []byte) ([]byte, error) {
	if err := k.Validate(); err != nil {
		return nil, err
	}
	kek := DeriveKEK(password, k.KDF.Salt, k.KDF.Iter)
	defer Wipe(kek)
	return SM4GCMOpen(kek, k.Nonce, []byte(KeyfileAAD), k.CT)
}

// DecryptPrivateKeyWithKEK 用已派生的 KEK 直接解出私钥 DER（§9.1 自动解锁免口令路径）。
// KEK 由调用方持有（DPAPI 取回），用后由调用方负责 Wipe；口令错误路径不适用。
func (k *Keyfile) DecryptPrivateKeyWithKEK(kek []byte) ([]byte, error) {
	if err := k.Validate(); err != nil {
		return nil, err
	}
	return SM4GCMOpen(kek, k.Nonce, []byte(KeyfileAAD), k.CT)
}

// Validate 校验 keyfile 结构完整性（不解密内容，供 keytool inspect，§10）。
func (k *Keyfile) Validate() error {
	if k == nil {
		return errors.New("keyfile: nil")
	}
	if k.V != 1 {
		return fmt.Errorf("keyfile: 不支持的版本 %d", k.V)
	}
	if k.KDF.Alg != "PBKDF2-SM3" {
		return fmt.Errorf("keyfile: 不支持的 KDF %q", k.KDF.Alg)
	}
	if len(k.KDF.Salt) != SaltSize {
		return fmt.Errorf("keyfile: salt 长度 %d != %d", len(k.KDF.Salt), SaltSize)
	}
	if k.KDF.Iter <= 0 {
		return errors.New("keyfile: 无效迭代次数")
	}
	if len(k.Nonce) != SM4NonceSize {
		return fmt.Errorf("keyfile: nonce 长度 %d != %d", len(k.Nonce), SM4NonceSize)
	}
	if len(k.CT) == 0 {
		return errors.New("keyfile: 密文为空")
	}
	return nil
}

// MarshalJSON 序列化为字节（wire format §4.3）。
// 注意：必须用别名类型调 json.Marshal，否则会递归调用本方法导致栈溢出。
func (k *Keyfile) MarshalJSON() ([]byte, error) {
	type alias Keyfile
	return json.Marshal((*alias)(k))
}

// ParseKeyfile 从字节解析 keyfile（§4.3）。
func ParseKeyfile(data []byte) (*Keyfile, error) {
	var k Keyfile
	if err := json.Unmarshal(data, &k); err != nil {
		return nil, fmt.Errorf("keyfile: 解析失败: %w", err)
	}
	if err := k.Validate(); err != nil {
		return nil, err
	}
	return &k, nil
}

// SaveToFile 落盘 keyfile，权限 0600（§4.3 / §10 keytool 输出文件权限）。
func (k *Keyfile) SaveToFile(path string) error {
	data, err := k.MarshalJSON()
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// LoadKeyfile 从文件读取 keyfile（§4.3）。
// 路径来自用户明确指定（keytool inspect / 客户端导入），非不可信输入。
func LoadKeyfile(path string) (*Keyfile, error) {
	// #nosec G304 -- 用户显式指定路径的本地文件读取，见函数注释
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseKeyfile(data)
}
