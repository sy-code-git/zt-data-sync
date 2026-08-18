package vault

import (
	"errors"

	"passbook/internal/crypto"
)

// ---- DEK 缓存管理（§9.1） ----

// GetDEK 取组 DEK（内存缓存优先；未命中查本地 key_cache）。
func (v *Vault) GetDEK(groupID string, kv int) ([]byte, error) {
	v.mu.Lock()
	if e, ok := v.deks[groupID]; ok && e.KV == kv {
		dek := make([]byte, len(e.DEK))
		copy(dek, e.DEK)
		v.mu.Unlock()
		return dek, nil
	}
	v.mu.Unlock()

	// 本地 key_cache（KEK 加密）
	enc, err := v.local.GetDEK(groupID, kv)
	if err != nil {
		return nil, errors.New("vault: 本地无该组该 kv 的 DEK（等待信封）")
	}
	dek, err := v.UnmarshalDEKFromCache(enc)
	if err != nil {
		return nil, err
	}
	if err := v.SetDEK(groupID, kv, dek); err != nil {
		crypto.Wipe(dek)
		return nil, err
	}
	return dek, nil
}

// SetDEK 缓存组 DEK（内存 + 本地 key_cache）。
// 拷贝入参：调用方随后 Wipe 不影响缓存（§4.2 返回密钥的新拷贝）。
func (v *Vault) SetDEK(groupID string, kv int, dek []byte) error {
	cp := make([]byte, len(dek))
	copy(cp, dek)

	v.mu.Lock()
	// 替换旧 DEK 时先清零
	if e, ok := v.deks[groupID]; ok {
		crypto.Wipe(e.DEK)
	}
	v.deks[groupID] = dekEntry{KV: kv, DEK: cp}
	v.mu.Unlock()

	enc, err := v.MarshalDEKForCache(cp)
	if err != nil {
		return err
	}
	return v.local.PutDEK(groupID, kv, enc)
}

// UnwrapDEK 用私钥解信封得 DEK，并缓存（§9.1 解锁流程 / §7.2 同步顺序：信封先处理）。
func (v *Vault) UnwrapDEK(groupID string, kv int, wrappedDEK string) ([]byte, error) {
	env, err := parseEnvelopeJSON(wrappedDEK)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	priv := v.priv
	v.mu.Unlock()
	if priv == nil {
		return nil, errors.New("vault: 未解锁")
	}
	dek, err := crypto.SM2UnwrapDEK(priv, env.Data)
	if err != nil {
		return nil, err
	}
	if err := v.SetDEK(groupID, kv, dek); err != nil {
		crypto.Wipe(dek)
		return nil, err
	}
	return dek, nil
}

// NewDEK 生成新 DEK（rekey 用，§7.2 auto-rekey）。
func (v *Vault) NewDEK() ([]byte, error) {
	return crypto.Random(crypto.SM4KeySize)
}

// WrapDEKFor 用公钥包裹 DEK（auto-wrap / auto-rekey 上传信封，§7.2）。
func (v *Vault) WrapDEKFor(pubKeyB64 string, dek []byte) (string, error) {
	pub, err := parsePubKeyB64(pubKeyB64)
	if err != nil {
		return "", err
	}
	wrapped, err := crypto.SM2WrapDEK(pub, dek)
	if err != nil {
		return "", err
	}
	env := envelopeJSON{Alg: "SM2-C1C3C2", Data: wrapped}
	return env.marshal()
}

// HasAnyDEK 组是否有 DEK（内存或本地 key_cache）。
func (v *Vault) HasAnyDEK(groupID string) bool {
	v.mu.Lock()
	_, ok := v.deks[groupID]
	v.mu.Unlock()
	if ok {
		return true
	}
	groups, err := v.local.ListDEKGroupIDs()
	if err != nil {
		return false
	}
	for _, g := range groups {
		if g == groupID {
			return true
		}
	}
	return false
}

// InvalidateGroupDEKs 清除组全部 DEK（组移除/退出时，§9.1）。
func (v *Vault) InvalidateGroupDEKs(groupID string) {
	v.mu.Lock()
	if e, ok := v.deks[groupID]; ok {
		crypto.Wipe(e.DEK)
		delete(v.deks, groupID)
	}
	v.mu.Unlock()
	_ = v.local.DeleteGroupDEKs(groupID)
}
