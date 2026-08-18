package proto

// ---- 邀请码 + 注册申请（方案 C：邀请码绑定工号 + 审核制，§6.3） ----

// CreateInviteRequest POST /admin/invites（生成邀请码，绑定工号）。
type CreateInviteRequest struct {
	Username    string `json:"username"` // 绑定工号（唯一、不可改）
	AutoApprove bool   `json:"auto_approve"` // 1=免审核（提交申请即自动开户）
	TTLDays     int    `json:"ttl_days"`     // 有效期天数（默认 3，0=用默认）
}

// InviteOut 邀请码（列表/生成响应）。
type InviteOut struct {
	Code        string `json:"code"`
	Username    string `json:"username"`
	AutoApprove bool   `json:"auto_approve"`
	Status      string `json:"status"` // unused | used
	ExpiresAt   int64  `json:"expires_at"`
	CreatedAt   int64  `json:"created_at"`
	UsedAt      int64  `json:"used_at,omitempty"`
}

// InviteResponse 生成邀请码响应。
type InviteResponse struct {
	Invite InviteOut `json:"invite"`
}

// InvitesResponse GET /admin/invites 响应。
type InvitesResponse struct {
	Invites []InviteOut `json:"invites"`
}

// RegisterRequestRequest POST /auth/register-request（用户提交注册申请，免登录）。
type RegisterRequestRequest struct {
	InviteCode   string `json:"invite_code"`
	Username     string `json:"username"` // 工号（须与邀请码绑定一致）
	SM2PublicKey string `json:"sm2_public_key"`
	DeviceName   string `json:"device_name"`
}

// RegisterRequestOut 注册申请（审核列表/状态查询响应）。
type RegisterRequestOut struct {
	ID           string `json:"id"`
	InviteCode   string `json:"invite_code"`
	Username     string `json:"username"`
	SM2PublicKey string `json:"sm2_public_key"`
	DeviceName   string `json:"device_name"`
	IP           string `json:"ip"`
	Status       string `json:"status"` // pending | approved | rejected
	CreatedAt    int64  `json:"created_at"`
	ReviewedBy   string `json:"reviewed_by,omitempty"`
	ReviewedAt   int64  `json:"reviewed_at,omitempty"`
}

// RegisterRequestResponse POST /auth/register-request 响应。
type RegisterRequestResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"` // pending（待审核）或 approved（免审核码自动开户）
}

// RegisterStatusRequest GET /auth/register-status?invite_code=xxx。
type RegisterStatusRequest struct {
	InviteCode string `json:"invite_code"`
}

// RegisterStatusResponse 状态查询响应（客户端轮询）。
type RegisterStatusResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"` // pending | approved | rejected
}

// RegisterRequestsResponse GET /admin/register-requests 响应。
type RegisterRequestsResponse struct {
	Requests []RegisterRequestOut `json:"requests"`
}

// ReviewRequestRequest POST /admin/register-requests/:id/approve|reject。
type ReviewRequestRequest struct {
	Name   string `json:"name"`   // approve 时填显示名（默认用工号）
	Reason string `json:"reason"` // reject 时备注（可选）
}
