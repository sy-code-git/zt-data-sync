package proto

// 错误码（§13，与 HTTP 状态一一对应）。
const (
	// 40001 请求格式错误
	ErrBadRequest = 40001
	// 40101 token 缺失/无效/过期（含设备禁用）
	ErrUnauthorized = 40101
	// 40104 签名校验失败
	ErrBadSignature = 40104
	// 40105 bootstrap token 无效/已使用
	ErrBadBootstrap = 40105
	// 40106 设备注册 challenge 无效/过期/已使用
	ErrBadChallenge = 40106
	// 40301 用户已吊销
	ErrUserRevoked = 40301
	// 40302 非该组成员
	ErrNotMember = 40302
	// 40303 需要 admin
	ErrNotAdmin = 40303
	// 40901 seq 冲突（响应携带服务端当前版）
	ErrConflict = 40901
	// 40902 key_version 过期（需先拉新信封/重加密）
	ErrKeyVersionStale = 40902
	// 40903 条目状态不一致（id 不存在但 base_seq≠0；或新条目 id 已存在）
	ErrEntryState = 40903
	// 40904 信封集合不合法（含非 active 成员 / rekey 集合不完整 / 覆盖已有信封）
	ErrBadEnvelopes = 40904
	// 40905 组已归档，推送/信封提交被拒绝（协作冻结）
	ErrGroupArchived = 40905
	// 41301 密文包超过 256KB
	ErrTooLarge = 41301
	// 42901 触发限流
	ErrRateLimited = 42901
	// 50001 服务端内部错误
	ErrInternal = 50001
)

// HTTPStatus 错误码 → HTTP 状态码（§13 错误码表）。
func HTTPStatus(code int) int {
	switch code {
	case ErrBadRequest:
		return 400
	case ErrUnauthorized, ErrBadSignature, ErrBadBootstrap, ErrBadChallenge:
		return 401
	case ErrUserRevoked, ErrNotMember, ErrNotAdmin:
		return 403
	case ErrConflict, ErrKeyVersionStale, ErrEntryState, ErrBadEnvelopes, ErrGroupArchived:
		return 409
	case ErrTooLarge:
		return 413
	case ErrRateLimited:
		return 429
	case ErrInternal:
		return 500
	default:
		return 500
	}
}

// ErrorBody 统一错误响应结构（HTTP 4xx/5xx）。
type ErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	// Detail 可选附加信息（如 40901 的 current 由响应体另行携带）。
	Detail string `json:"detail,omitempty"`
}
