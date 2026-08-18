package syncer

import (
	"passbook/internal/crypto"
	"passbook/internal/proto"
)

// autoWrap 为组内无信封成员包裹当前 DEK 并上传（§7.2 3a auto-wrap）。
// 含冷启动：组当前 kv 无任何信封 → 生成 DEK(kv=1) 并为全部 active 成员包裹（§7.2 3a）。
func (e *Engine) autoWrap(g *proto.GroupState) error {
	// 获取 active 用户公钥（GET /users，§7.2 3a：公钥经 GET /users 获取并本地缓存）
	users, err := e.api.ListUsers()
	if err != nil {
		return err
	}
	byID := map[string]string{}
	for _, u := range users {
		byID[u.UserID] = u.SM2PublicKey
	}

	dek, err := e.vault.GetDEK(g.ID, g.KeyVersion)
	if err != nil {
		// 冷启动：组当前 kv 无任何信封 → 本端生成 DEK(kv=1)（§7.2 3a 冷启动分支）
		if !e.vault.HasAnyDEK(g.ID) {
			dek, err = e.vault.NewDEK()
			if err != nil {
				return err
			}
			if err := e.vault.SetDEK(g.ID, g.KeyVersion, dek); err != nil {
				crypto.Wipe(dek)
				return err
			}
			defer crypto.Wipe(dek) // 冷启动生成的 DEK 用后清零（§4.2 内存卫生）
			// 冷启动：为全部 active 成员包裹（missing==全部，提交全集合法，§7.2 3a）
			envs := make([]proto.EnvelopeUpload, 0, len(g.MissingEnvelopes))
			for _, uid := range g.MissingEnvelopes {
				pub, ok := byID[uid]
				if !ok {
					continue
				}
				wrapped, err := e.vault.WrapDEKFor(pub, dek)
				if err != nil {
					return err
				}
				envs = append(envs, proto.EnvelopeUpload{UserID: uid, WrappedDEK: wrapped})
			}
			if len(envs) == 0 {
				return nil
			}
			return e.api.UploadKeys(g.ID, &proto.KeysUploadRequest{KeyVersion: g.KeyVersion, Envelopes: envs})
		}
		return err
	}
	defer crypto.Wipe(dek)

	// 常规：为 missing_envelopes 中成员包裹当前 DEK（kv 不变，仅追加，§6.3）
	envs := make([]proto.EnvelopeUpload, 0, len(g.MissingEnvelopes))
	for _, uid := range g.MissingEnvelopes {
		pub, ok := byID[uid]
		if !ok {
			continue
		}
		wrapped, err := e.vault.WrapDEKFor(pub, dek)
		if err != nil {
			return err
		}
		envs = append(envs, proto.EnvelopeUpload{UserID: uid, WrappedDEK: wrapped})
	}
	if len(envs) == 0 {
		return nil
	}
	return e.api.UploadKeys(g.ID, &proto.KeysUploadRequest{KeyVersion: g.KeyVersion, Envelopes: envs})
}
