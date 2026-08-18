package proto

// ---- admin 接口类型（§6.3） ----

// CreateUserRequest POST /admin/users（§6.3：导入 pub.json，校验 attestation）。
type CreateUserRequest struct {
	Username     string `json:"username"` // 工号（唯一、不可改、登录标识）
	Name         string `json:"name"`
	SM2PublicKey string `json:"sm2_public_key"` // base64(DER)
	Attestation  string `json:"attestation"`    // base64(HMAC-SM3)
}

// CreateUserResponse 响应。
type CreateUserResponse struct {
	UserID string `json:"user_id"`
}

// RevokeRequest POST /admin/users/:uid/revoke（需成员名二次确认）。
type RevokeRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// RevokeResponse 响应。
type RevokeResponse struct {
	UserID string `json:"user_id"`
	Status string `json:"status"`
	// EmptyGroups 被吊销者是组内最后 active 成员的组（吊销后该组已无成员）。
	EmptyGroups []string `json:"empty_groups,omitempty"`
}

// KeyfileResetRequest POST /admin/users/:uid/keyfile-reset（keyfile 丢失找回）。
type KeyfileResetRequest struct {
	SM2PublicKey string `json:"sm2_public_key"`
	Attestation  string `json:"attestation"`
}

// KeyfileResetResponse 响应（反映该用户所在任一组的当前状态）。
type KeyfileResetResponse struct {
	UserID       string `json:"user_id"`
	KeyVersion   int    `json:"key_version"`
	PendingRekey bool   `json:"pending_rekey"`
}

// GroupCreateRequest POST /admin/groups（建组）。
type GroupCreateRequest struct {
	Name string `json:"name"`
}

// GroupCreateResponse 响应。
type GroupCreateResponse struct {
	GroupID    string `json:"group_id"`
	Name       string `json:"name"`
	KeyVersion int    `json:"key_version"`
}

// GroupInfo GET /admin/groups 元素（仅元数据，不含密文/信封）。
type GroupInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	KeyVersion   int    `json:"key_version"`
	PendingRekey bool   `json:"pending_rekey"`
	Archived     bool   `json:"archived"`
	ArchivedAt   int64  `json:"archived_at,omitempty"`
	MemberCount  int    `json:"member_count"`
	CreatedAt    int64  `json:"created_at"`
}

// GroupsResponse GET /admin/groups 响应。
type GroupsResponse struct {
	Groups []GroupInfo `json:"groups"`
}

// MemberAddRequest PUT /admin/groups/:gid/members（加入组成员）。
type MemberAddRequest struct {
	UserID string `json:"user_id"`
}

// MemberAddResponse 响应。
type MemberAddResponse struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
}

// MemberRemoveRequest DELETE /admin/groups/:gid/members/:uid（移出成员）。
type MemberRemoveRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// MemberRemoveResponse 响应。
type MemberRemoveResponse struct {
	GroupID string `json:"group_id"`
	UserID  string `json:"user_id"`
	Removed bool   `json:"removed"`
}

// DeviceBrief 组成员清单中的设备摘要（§6.3）。
type DeviceBrief struct {
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"last_seen"`
}

// GroupMemberInfo 组成员清单元素（用户名/在线状态/在线 IP/设备名·机器名，§6.3）。
type GroupMemberInfo struct {
	UserID  string        `json:"user_id"`
	Name    string        `json:"name"`
	Online  bool          `json:"online"`
	Devices []DeviceBrief `json:"devices"`
}

// GroupMembersResponse GET /admin/groups/:gid/members 响应。
type GroupMembersResponse struct {
	Members []GroupMemberInfo `json:"members"`
}

// AdminDevice GET /admin/devices 元素（含在线状态/last_ip/hostname，§6.3）。
type AdminDevice struct {
	DeviceID string `json:"device_id"`
	UserID   string `json:"user_id"`
	UserName string `json:"user_name"`
	Name     string `json:"name"`
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"`
	LastSeen int64  `json:"last_seen"`
	Status   string `json:"status"`
}

// DevicesResponse GET /admin/devices 响应。
type DevicesResponse struct {
	Devices []AdminDevice `json:"devices"`
}

// DisableDeviceRequest POST /admin/devices/:did/disable（需设备名二次确认）。
type DisableDeviceRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// DisableDeviceResponse 响应。
type DisableDeviceResponse struct {
	DeviceID string `json:"device_id"`
	Status   string `json:"status"`
}

// RekeyResponse POST /admin/groups/:gid/rekey 响应（触发=置位，执行由在线成员完成）。
type RekeyResponse struct {
	GroupID      string `json:"group_id"`
	KeyVersion   int    `json:"key_version"`
	PendingRekey bool   `json:"pending_rekey"`
}

// ArchiveRequest POST /admin/groups/:gid/archive（需组名二次确认）。
type ArchiveRequest struct {
	ConfirmName string `json:"confirm_name"`
}

// ArchiveResponse 响应。
type ArchiveResponse struct {
	GroupID    string `json:"group_id"`
	Archived   bool   `json:"archived"`
	ArchivedAt int64  `json:"archived_at,omitempty"`
}

// UnarchiveResponse POST /admin/groups/:gid/unarchive 响应。
type UnarchiveResponse struct {
	GroupID  string `json:"group_id"`
	Archived bool   `json:"archived"`
}

// AuditEventOut GET /admin/audit 元素（§6.3：按 ts 倒序，上限 500）。
type AuditEventOut struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	UserID     string `json:"user_id"`
	UserName   string `json:"user_name"`
	Action     string `json:"action"`
	DeviceID   string `json:"device_id"`
	DeviceName string `json:"device_name"`
	Hostname   string `json:"hostname"`
	IP         string `json:"ip"`
	EntryID    string `json:"entry_id,omitempty"`
	Detail     string `json:"detail"`
}

// AuditResponse GET /admin/audit 响应。
type AuditResponse struct {
	Events []AuditEventOut `json:"events"`
}
