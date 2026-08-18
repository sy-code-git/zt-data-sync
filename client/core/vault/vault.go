// Package vault 客户端解锁、DEK 缓存、加解密、keyfile 导入（§9.1）。
// 私钥/KEK/DEK 一律 []byte 驻留内存，锁定/退出时覆写清零（§14.1 不变量 #2）。
package vault

import (
	"encoding/base64"
	"errors"
	"sync"

	"github.com/tjfoc/gmsm/sm2"

	"passbook/client/core/store"
	"passbook/internal/crypto"
)

// dekEntry 组 DEK 缓存项（内存态，§9.1 解锁流程）。
type dekEntry struct {
	KV  int
	DEK []byte
}

// Vault 解锁与加解密（§9.1）。
type Vault struct {
	mu sync.Mutex

	local store.LocalStore

	// 内存密钥（锁定清零）
	kek      []byte              // 由 keyfile 口令派生
	priv     *sm2.PrivateKey     // 解信封用
	privDER  []byte              // 私钥 DER（Wipe 用）
	deks     map[string]dekEntry // groupID → DEK
	unlocked bool

	// 明文缓存派生密钥（KEK 派生，§9.1）
	cacheKey []byte
}

// New 构造 Vault（依赖本地存储）。
func New(local store.LocalStore) *Vault {
	return &Vault{
		local: local,
		deks:  map[string]dekEntry{},
	}
}

// ImportKeyfile 导入 keyfile 并验证口令（§9.1 首启与 keyfile 导入）。
// 成功即处于解锁态（派生 KEK、解私钥、载入本地 DEK 缓存）。
func (v *Vault) ImportKeyfile(path string, password []byte) (*store.DeviceState, error) {
	kf, err := crypto.LoadKeyfile(path)
	if err != nil {
		return nil, err
	}
	privDER, err := kf.DecryptPrivateKey(password)
	if err != nil {
		return nil, errors.New("口令错误或 keyfile 损坏")
	}
	kek := crypto.DeriveKEK(password, kf.KDF.Salt, kf.KDF.Iter)
	return v.setUnlocked(kek, privDER)
}

// Unlock 解锁（已有 keyfile 落盘时的再次解锁；当前实现同 ImportKeyfile）。
func (v *Vault) Unlock(path string, password []byte) (*store.DeviceState, error) {
	return v.ImportKeyfile(path, password)
}

// GenerateKeypair 生成本地密钥对（方案 A）：生成 SM2 密钥对 → 口令加密私钥（keyfile 格式）→ 设置解锁态。
// 返回公钥 base64 与加密的 keyfile blob（调用方负责存入 identity 表）。
// 成功时 vault 处于解锁态（私钥驻留内存，供后续注册设备签名）。
func (v *Vault) GenerateKeypair(password []byte) (pubB64 string, blob []byte, err error) {
	priv, err := crypto.GenerateSM2Key()
	if err != nil {
		return "", nil, err
	}
	privDER, err := crypto.MarshalSM2PrivateKey(priv)
	if err != nil {
		return "", nil, err
	}
	// 口令加密私钥（keyfile 格式，§4.3）
	kf, err := crypto.NewKeyfile(privDER, password)
	if err != nil {
		crypto.Wipe(privDER)
		return "", nil, err
	}
	blob, err = kf.MarshalJSON()
	if err != nil {
		crypto.Wipe(privDER)
		return "", nil, err
	}
	// 公钥（base64 DER，导出给管理员开户）
	pubDER, err := crypto.MarshalSM2PublicKey(&priv.PublicKey)
	if err != nil {
		crypto.Wipe(privDER)
		return "", nil, err
	}
	pubB64 = base64.StdEncoding.EncodeToString(pubDER)
	// 设置解锁态（kek 与 privDER 所有权转移给 vault）
	kek := crypto.DeriveKEK(password, kf.KDF.Salt, kf.KDF.Iter)
	if _, err := v.setUnlocked(kek, privDER); err != nil {
		return "", nil, err
	}
	return pubB64, blob, nil
}

// UnlockWithKeyfileBlob 从 keyfile blob（本地库 identity 存储）解私钥并设置解锁态。
// 口令错误或 blob 损坏均返回错误。
func (v *Vault) UnlockWithKeyfileBlob(blob, password []byte) (*store.DeviceState, error) {
	kf, err := crypto.ParseKeyfile(blob)
	if err != nil {
		return nil, err
	}
	privDER, err := kf.DecryptPrivateKey(password)
	if err != nil {
		return nil, errors.New("口令错误或私钥损坏")
	}
	kek := crypto.DeriveKEK(password, kf.KDF.Salt, kf.KDF.Iter)
	return v.setUnlocked(kek, privDER)
}

// UnlockWithKEK 用已派生的 KEK 免口令解锁（§9.1 自动解锁路径：KEK 来自 DPAPI 取回）。
// 传入的 kek 由本方法接管：成功时所有权转移给 vault（锁定/退出时 Wipe），
// 失败时本方法立即 Wipe（密钥卫生 §14.1 不变量 #2，不依赖调用方兜底）。
func (v *Vault) UnlockWithKEK(path string, kek []byte) (*store.DeviceState, error) {
	kf, err := crypto.LoadKeyfile(path)
	if err != nil {
		crypto.Wipe(kek)
		return nil, err
	}
	privDER, err := kf.DecryptPrivateKeyWithKEK(kek)
	if err != nil {
		crypto.Wipe(kek)
		return nil, errors.New("KEK 与 keyfile 不匹配（可能已换 keyfile 或口令修改）")
	}
	return v.setUnlocked(kek, privDER)
}

// setUnlocked 设置解锁态并载入缓存（kek 与 privDER 由调用方转移所有权，锁定/退出时 Wipe）。
func (v *Vault) setUnlocked(kek, privDER []byte) (*store.DeviceState, error) {
	priv, err := crypto.UnmarshalSM2PrivateKey(privDER)
	if err != nil {
		crypto.Wipe(privDER)
		crypto.Wipe(kek)
		return nil, err
	}
	v.mu.Lock()
	v.wipeLocked()
	v.kek = kek
	v.priv = priv
	v.privDER = privDER
	v.cacheKey = crypto.DeriveKEKSubkey(kek, "plaintext-cache")
	v.deks = map[string]dekEntry{}
	v.unlocked = true
	v.mu.Unlock()

	// 载入本地 DEK 缓存（解锁态缓存，§9.1 key_cache）
	_ = v.loadDEKCache()
	ds, err := v.loadDeviceState()
	if err != nil {
		// 本地库异常无法读取设备状态：回滚解锁，避免半解锁态（§9.1 密钥卫生）
		v.Lock()
		return nil, err
	}
	return ds, nil
}

// EnableAutoUnlock 开启自动解锁（§9.1）：把当前内存 KEK 用 DPAPI 保护后落盘，
// 并记录 keyfile 路径供下次免口令定位。须处于解锁态（KEK 已在内存）。
func (v *Vault) EnableAutoUnlock(keyfilePath string) error {
	v.mu.Lock()
	if !v.unlocked || len(v.kek) == 0 {
		v.mu.Unlock()
		return errors.New("vault: 未解锁，无法开启自动解锁")
	}
	// 锁内拷贝 KEK，避免释放锁后并发 Lock() 清零底层数组导致 DPAPI 保护脏数据
	kek := make([]byte, len(v.kek))
	copy(kek, v.kek)
	v.mu.Unlock()

	defer crypto.Wipe(kek)
	blob, err := dpapiProtect(kek)
	if err != nil {
		return err
	}
	return v.local.SetAutoUnlock(&store.AutoUnlockConfig{
		KeyfilePath: keyfilePath,
		Enabled:     true,
		KEKBlob:     blob,
	})
}

// DisableAutoUnlock 关闭自动解锁（§9.1：关闭后立即失效，清除 DPAPI blob）。
func (v *Vault) DisableAutoUnlock() error {
	return v.local.SetAutoUnlock(&store.AutoUnlockConfig{})
}

// AutoUnlockEnabled 是否已开启自动解锁。
func (v *Vault) AutoUnlockEnabled() bool {
	cfg, err := v.local.GetAutoUnlock()
	return err == nil && cfg != nil && cfg.Enabled
}

// TryAutoUnlock 尝试用 DPAPI 取回 KEK 免口令解锁（§9.1）。
// 未开启 / 非 Windows / DPAPI 取回失败均返回错误，调用方回退 keyfile 口令。
func (v *Vault) TryAutoUnlock() (*store.DeviceState, error) {
	cfg, err := v.local.GetAutoUnlock()
	if err != nil {
		return nil, err
	}
	if cfg == nil || !cfg.Enabled || len(cfg.KEKBlob) == 0 || cfg.KeyfilePath == "" {
		return nil, errors.New("vault: 未开启自动解锁")
	}
	kek, err := dpapiUnprotect(cfg.KEKBlob)
	if err != nil {
		return nil, err
	}
	// kek 所有权交给 UnlockWithKEK（成功由 vault 持有、失败由其 Wipe，此处不再碰）
	return v.UnlockWithKEK(cfg.KeyfilePath, kek)
}

// Lock 锁定并清零内存密钥（§9.1 自动锁定/手动锁定）。
func (v *Vault) Lock() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.wipeLocked()
}

func (v *Vault) wipeLocked() {
	crypto.Wipe(v.kek)
	crypto.Wipe(v.privDER)
	crypto.Wipe(v.cacheKey)
	for _, e := range v.deks {
		crypto.Wipe(e.DEK)
	}
	if v.priv != nil {
		wipeSM2PrivateKey(v.priv)
	}
	v.kek, v.priv, v.privDER, v.cacheKey = nil, nil, nil, nil
	v.deks = map[string]dekEntry{}
	v.unlocked = false
}

// wipeSM2PrivateKey 清零私钥 D（§14.1 不变量 #2：锁定/退出覆写私钥，此前仅置 nil 未擦 D）。
func wipeSM2PrivateKey(priv *sm2.PrivateKey) {
	if priv == nil || priv.D == nil {
		return
	}
	bits := priv.D.Bits()
	for i := range bits {
		bits[i] = 0
	}
	priv.D = nil
}

// IsUnlocked 是否解锁态。
func (v *Vault) IsUnlocked() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.unlocked
}

// PrivateKey 返回私钥（解信封用；调用方不得持有）。
func (v *Vault) PrivateKey() *sm2.PrivateKey {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.unlocked {
		return nil
	}
	return v.priv
}

// loadDEKCache 从本地 key_cache 载入 DEK（解锁后调用）。
func (v *Vault) loadDEKCache() error {
	// 本地 store 无枚举 key_cache 方法，通过 GetDEK 按需；此处预留
	return nil
}

// loadDeviceState 读取本地设备状态（供 UI 展示）。
// TokenEnc 保持密文（KEK 派生密钥加密），由调用方 DecryptToken 统一解密——
// 若在此就地解密，调用方再 DecryptToken 会二次解密失败（P0）。
func (v *Vault) loadDeviceState() (*store.DeviceState, error) {
	ds, err := v.local.GetDeviceState()
	if err != nil {
		if errors.Is(err, store.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return ds, nil
}

// splitCipher 拆分 nonce||ct 组合（本地列存储格式）。
func splitCipher(data []byte) (nonce, ct []byte) {
	if len(data) <= crypto.SM4NonceSize {
		return nil, data
	}
	return data[:crypto.SM4NonceSize], data[crypto.SM4NonceSize:]
}

// joinCipher 组合 nonce||ct。
func joinCipher(nonce, ct []byte) []byte {
	out := make([]byte, 0, len(nonce)+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out
}

// encryptLocal 用 KEK 派生密钥加密本地敏感数据。
func (v *Vault) encryptLocal(label string, plaintext []byte) ([]byte, error) {
	v.mu.Lock()
	kek := v.kek
	v.mu.Unlock()
	if kek == nil {
		return nil, errors.New("vault: 未解锁")
	}
	key := crypto.DeriveKEKSubkey(kek, label)
	nonce, ct, err := crypto.SM4GCMSeal(key, nil, plaintext)
	if err != nil {
		return nil, err
	}
	return joinCipher(nonce, ct), nil
}

// decryptLocal 用 KEK 派生密钥解密本地敏感数据。
func (v *Vault) decryptLocal(label string, data []byte) ([]byte, error) {
	v.mu.Lock()
	kek := v.kek
	v.mu.Unlock()
	if kek == nil {
		return nil, errors.New("vault: 未解锁")
	}
	key := crypto.DeriveKEKSubkey(kek, label)
	nonce, ct := splitCipher(data)
	return crypto.SM4GCMOpen(key, nonce, nil, ct)
}

// DecryptToken 解密设备 token（解锁态）。
func (v *Vault) DecryptToken(enc []byte) (string, error) {
	pt, err := v.decryptLocal("device-token", enc)
	if err != nil {
		return "", err
	}
	defer crypto.Wipe(pt)
	return string(pt), nil
}

// EncryptToken 加密设备 token。
func (v *Vault) EncryptToken(raw string) ([]byte, error) {
	return v.encryptLocal("device-token", []byte(raw))
}

// MarshalDEKForCache 加密 DEK 供本地 key_cache 存储。
func (v *Vault) MarshalDEKForCache(dek []byte) ([]byte, error) {
	return v.encryptLocal("dek-cache", dek)
}

// UnmarshalDEKFromCache 解密本地 key_cache 的 DEK。
func (v *Vault) UnmarshalDEKFromCache(enc []byte) ([]byte, error) {
	pt, err := v.decryptLocal("dek-cache", enc)
	if err != nil {
		return nil, err
	}
	return pt, nil
}

// PublicKeyB64 当前私钥对应公钥的 base64(DER)（设备注册用）。
func (v *Vault) PublicKeyB64() (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if !v.unlocked || v.priv == nil {
		return "", errors.New("vault: 未解锁")
	}
	der, err := crypto.MarshalSM2PublicKey(&v.priv.PublicKey)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(der), nil
}

// SignChallenge 对设备注册挑战签名（§6.3 设备注册）。
func (v *Vault) SignChallenge(challenge string) ([]byte, error) {
	v.mu.Lock()
	priv := v.priv
	v.mu.Unlock()
	if priv == nil {
		return nil, errors.New("vault: 未解锁")
	}
	return crypto.SM2SignChallenge(priv, []byte(challenge))
}

// EncryptCache 加密条目明文缓存（§9.1 plaintext_cache_enc，KEK 派生密钥 "plaintext-cache"）。
func (v *Vault) EncryptCache(plaintext []byte) ([]byte, error) {
	return v.encryptLocal("plaintext-cache", plaintext)
}

// DecryptCache 解密条目明文缓存（§9.1）。
func (v *Vault) DecryptCache(enc []byte) ([]byte, error) {
	return v.decryptLocal("plaintext-cache", enc)
}
