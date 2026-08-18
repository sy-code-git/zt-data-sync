package vault

import (
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/tjfoc/gmsm/sm2"

	"passbook/internal/crypto"
	"passbook/internal/model"
)

// envelopeJSON 信封 wire format（§4.3，与 model.KeyEnvelope 对齐）。
type envelopeJSON struct {
	V    int    `json:"v"`
	Alg  string `json:"alg"`
	Data []byte `json:"data"`
}

func (e *envelopeJSON) marshal() (string, error) {
	if e.V == 0 {
		e.V = 1
	}
	if e.Alg == "" {
		e.Alg = "SM2-C1C3C2"
	}
	b, err := json.Marshal(e)
	return string(b), err
}

func parseEnvelopeJSON(s string) (*envelopeJSON, error) {
	var e envelopeJSON
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return nil, err
	}
	if e.V != 1 || e.Alg != "SM2-C1C3C2" {
		return nil, errors.New("vault: 信封格式不支持")
	}
	return &e, nil
}

// parsePubKeyB64 解析 base64(DER) 公钥（crypto 层已校验点在曲线上）。
func parsePubKeyB64(b64 string) (*sm2.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, errors.New("vault: 公钥 base64 解码失败")
	}
	return crypto.UnmarshalSM2PublicKey(der)
}

// ---- 条目加解密（§9.1） ----

// EncryptPlaintext 加密条目明文（§4.2/§4.3）：
//   - 明文 JSON 字节 → SM4-GCM(DEK, nonce, AAD=长度前缀编码(entry_id|group_id)) → HMAC-SM3
//   - 返回密文包 JSON 字符串
// core 对明文内容不解析（[]byte 透传），业务结构由 UI 负责。
func (v *Vault) EncryptPlaintext(groupID, entryID string, plaintext []byte, kv int) (string, error) {
	dek, err := v.GetDEK(groupID, kv)
	if err != nil {
		return "", err
	}
	defer crypto.Wipe(dek)

	aad := crypto.LengthPrefixed([]byte(entryID), []byte(groupID))
	nonce, ct, err := crypto.SM4GCMSeal(dek, aad, plaintext)
	if err != nil {
		return "", err
	}
	hmac := crypto.EntryHMAC(dek, entryID, groupID, kv, nonce, ct)

	pack := &model.Ciphertext{V: 1, Alg: model.CiphertextAlg, KV: kv, Nonce: nonce, CT: ct, HMAC: hmac}
	b, err := model.MarshalCiphertext(pack)
	return string(b), err
}

// DecryptPlaintext 解密条目（§9.1：验 HMAC → GCM 解密 → 返回明文 JSON 字节）。
// core 不解析明文内容（返回 []byte 给 UI/上层解析）。
func (v *Vault) DecryptPlaintext(groupID, entryID, ciphertext string) ([]byte, error) {
	pack, err := model.ParseCiphertext([]byte(ciphertext))
	if err != nil {
		return nil, err
	}
	dek, err := v.GetDEK(groupID, pack.KV)
	if err != nil {
		return nil, err
	}
	defer crypto.Wipe(dek)

	// 验 HMAC（常量时间，§4.2）
	want := crypto.EntryHMAC(dek, entryID, groupID, pack.KV, pack.Nonce, pack.CT)
	if !crypto.ConstantTimeEqual(want, pack.HMAC) {
		return nil, errors.New("vault: 条目 HMAC 校验失败（可能被篡改）")
	}
	aad := crypto.LengthPrefixed([]byte(entryID), []byte(groupID))
	pt, err := crypto.SM4GCMOpen(dek, pack.Nonce, aad, pack.CT)
	if err != nil {
		return nil, err
	}
	return pt, nil
}
