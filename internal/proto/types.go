// Package proto 定义 API 请求/响应结构体与错误码（§6 / §13）。
// 与服务端 HTTP handler 一一对应；三端共享。
package proto

// ---- 认证（§6.3） ----

// BootstrapRequest POST /auth/bootstrap（§6.3）。
type BootstrapRequest struct {
	BootstrapToken string `json:"bootstrap_token"`
	Username       string `json:"username"` // 工号（唯一、不可改、登录标识）
	Name           string `json:"name"`
	DeviceName     string `json:"device_name"`
	SM2PublicKey   string `json:"sm2_public_key"` // base64(DER)
}

// BootstrapResponse POST /auth/bootstrap 响应。
type BootstrapResponse struct {
	UserID    string `json:"user_id"`
	Username  string `json:"username,omitempty"` // 工号回显
	DeviceID  string `json:"device_id"`
	Token     string `json:"token"`
	Role      string `json:"role"`
	ExpiresIn int64  `json:"expires_in"` // 秒
}

// DeviceChallengeRequest POST /auth/device-challenge（§6.3）。
// Username 为工号（登录标识）；服务端按工号查用户生成一次性 challenge。
type DeviceChallengeRequest struct {
	Username string `json:"username"`
}

// DeviceChallengeResponse 响应。
type DeviceChallengeResponse struct {
	Challenge string `json:"challenge"` // base64(32B)
	ExpiresIn int64  `json:"expires_in"`
}

// DeviceRegisterRequest POST /auth/device（§6.3）。
// Username 为工号；服务端按工号查用户 + 库中公钥验签（SM3withSM2）。
type DeviceRegisterRequest struct {
	Username   string `json:"username"`
	DeviceName string `json:"device_name"`
	Hostname   string `json:"hostname,omitempty"` // 客户端上报 os.Hostname()（§8.2）
	Challenge  string `json:"challenge"`          // base64
	Signature  string `json:"signature"`          // base64(SM3withSM2(challenge))
}

// DeviceRegisterResponse 响应。
type DeviceRegisterResponse struct {
	DeviceID  string `json:"device_id"`
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expires_in"`
}

// TokenRefreshResponse POST /auth/refresh 响应（请求体为空）。
type TokenRefreshResponse struct {
	Token     string `json:"token"`
	ExpiresIn int64  `json:"expires_in"`
}

// HeartbeatRequest POST /auth/heartbeat（§6.3）。
type HeartbeatRequest struct {
	Hostname string `json:"hostname"`
}

// HeartbeatResponse 响应。
type HeartbeatResponse struct {
	OK bool `json:"ok"`
}

// ---- 用户列表（§6.3） ----

// UserInfo GET /users 元素。
type UserInfo struct {
	UserID       string `json:"user_id"`
	Username     string `json:"username,omitempty"` // 工号（登录标识）
	Name         string `json:"name"`
	SM2PublicKey string `json:"sm2_public_key"`
	Status       string `json:"status"`
	Role         string `json:"role,omitempty"` // admin | member（前端过滤管理员显示用）
}

// UsersResponse GET /users 响应。
type UsersResponse struct {
	Users []UserInfo `json:"users"`
}

// ---- 同步（§6.3） ----

// Change GET /sync 变更条目。
type Change struct {
	EntryID    string `json:"entry_id"`
	GroupID    string `json:"group_id"`
	Seq        int64  `json:"seq"`
	KeyVersion int    `json:"key_version"`
	Ciphertext string `json:"ciphertext"`          // 4.3 密文包 JSON（墓碑为空）
	Deleted    bool   `json:"deleted"`
	UpdatedAt  int64  `json:"updated_at"`
}

// KeyEnvelopeInfo GET /sync key_envelopes 元素。
type KeyEnvelopeInfo struct {
	GroupID     string `json:"group_id"`
	KeyVersion  int    `json:"key_version"`
	WrappedDEK  string `json:"wrapped_dek"` // 4.3 信封 JSON
}

// GroupState GET /sync groups 元素（协同状态，§6.3）。
type GroupState struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	KeyVersion       int      `json:"key_version"`
	PendingRekey     bool     `json:"pending_rekey"`
	Archived         bool     `json:"archived"`
	MissingEnvelopes []string `json:"missing_envelopes"`
	// ActiveMembers 该组当前全部 active 成员 user_id（§7.2 auto-rekey 包裹对象）。
	// 归档组为空。用于 auto-rekey 精确包裹"该组 active 成员"（区别于 GET /users 的全局 active 用户）。
	ActiveMembers []string `json:"active_members,omitempty"`
}

// SyncResponse GET /sync 响应。
type SyncResponse struct {
	ServerSeq    int64             `json:"server_seq"`
	Changes      []Change          `json:"changes"`
	KeyEnvelopes []KeyEnvelopeInfo `json:"key_envelopes"`
	Groups       []GroupState      `json:"groups"`
}

// Mutation POST /sync/push 单条变更。
type Mutation struct {
	EntryID    string `json:"entry_id"`
	GroupID    string `json:"group_id"`
	BaseSeq    int64  `json:"base_seq"`
	KeyVersion int    `json:"key_version"`
	Ciphertext string `json:"ciphertext"`
	Deleted    bool   `json:"deleted"`
}

// PushRequest POST /sync/push 请求。
type PushRequest struct {
	Mutations []Mutation `json:"mutations"`
}

// PushResult POST /sync/push 单条结果。
type PushResult struct {
	EntryID string `json:"entry_id"`
	OK      bool   `json:"ok"`
	NewSeq  int64  `json:"new_seq,omitempty"`
	Error   int    `json:"error,omitempty"` // 错误码（§13）
	// 40901 时携带服务端当前版（theirs，§7.3 三路合并输入）
	Current *Change `json:"current,omitempty"`
}

// PushResponse POST /sync/push 响应。
type PushResponse struct {
	Results []PushResult `json:"results"`
}

// ---- 信封集合（§6.3） ----

// KeysUploadRequest POST /groups/:gid/keys（入伙追加 / rekey 替换）。
type KeysUploadRequest struct {
	KeyVersion int `json:"key_version"`
	// Envelopes 元素 user_id 为成员 UUID，wrapped_dek 为 4.3 信封 JSON。
	Envelopes []EnvelopeUpload `json:"envelopes"`
}

// EnvelopeUpload 信封集合元素。
type EnvelopeUpload struct {
	UserID     string `json:"user_id"`
	WrappedDEK string `json:"wrapped_dek"`
}

// KeysUploadResponse 响应。
type KeysUploadResponse struct {
	KeyVersion int  `json:"key_version"`
	OK         bool `json:"ok"`
}
