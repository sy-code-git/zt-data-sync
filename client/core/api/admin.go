// admin.go — 管理端与设备认证的 HTTP 方法（§6.2/§6.3）。
// 复用 HTTPClient.do（token 锁 + 统一错误映射）；Bootstrap/Challenge/Register
// 走服务端无认证组（token 为空时 Authorization 头为空，服务端不校验）。
package api

import (
	"net/http"
	"net/url"

	"passbook/internal/proto"
)

// ---- 设备认证（§6.3，无鉴权组） ----

// Bootstrap 首启引导（首个 admin，一次性 bootstrap token）。
func (c *HTTPClient) Bootstrap(req *proto.BootstrapRequest) (*proto.BootstrapResponse, error) {
	var resp proto.BootstrapResponse
	if err := c.do(http.MethodPost, "/auth/bootstrap", nil, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeviceChallenge 获取设备注册挑战（绑定工号 username，5 分钟一次性）。
func (c *HTTPClient) DeviceChallenge(username string) (*proto.DeviceChallengeResponse, error) {
	var resp proto.DeviceChallengeResponse
	if err := c.do(http.MethodPost, "/auth/device-challenge", nil, &proto.DeviceChallengeRequest{Username: username}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeviceRegister 设备注册（challenge 签名核销，签发 token）。
func (c *HTTPClient) DeviceRegister(req *proto.DeviceRegisterRequest) (*proto.DeviceRegisterResponse, error) {
	var resp proto.DeviceRegisterResponse
	if err := c.do(http.MethodPost, "/auth/device", nil, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---- admin 接口（§6.3，需 RequireAdmin） ----

// AdminListGroups GET /admin/groups（组列表与状态，仅元数据）。
func (c *HTTPClient) AdminListGroups() ([]proto.GroupInfo, error) {
	var resp proto.GroupsResponse
	if err := c.do(http.MethodGet, "/admin/groups", nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Groups, nil
}

// AdminCreateGroup POST /admin/groups（建组，kv=1）。
func (c *HTTPClient) AdminCreateGroup(name string) (*proto.GroupCreateResponse, error) {
	var resp proto.GroupCreateResponse
	if err := c.do(http.MethodPost, "/admin/groups", nil, &proto.GroupCreateRequest{Name: name}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminListMembers GET /admin/groups/{gid}/members（成员清单，含在线状态/设备）。
func (c *HTTPClient) AdminListMembers(gid string) ([]proto.GroupMemberInfo, error) {
	var resp proto.GroupMembersResponse
	if err := c.do(http.MethodGet, "/admin/groups/"+url.PathEscape(gid)+"/members", nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Members, nil
}

// AdminAddMember PUT /admin/groups/{gid}/members（加组成员，幂等）。
func (c *HTTPClient) AdminAddMember(gid, uid string) error {
	return c.do(http.MethodPut, "/admin/groups/"+url.PathEscape(gid)+"/members", nil, &proto.MemberAddRequest{UserID: uid}, nil)
}

// AdminRemoveMember DELETE /admin/groups/{gid}/members/{uid}（移出成员，二次确认）。
func (c *HTTPClient) AdminRemoveMember(gid, uid, confirmName string) error {
	return c.do(http.MethodDelete, "/admin/groups/"+url.PathEscape(gid)+"/members/"+url.PathEscape(uid),
		nil, &proto.MemberRemoveRequest{ConfirmName: confirmName}, nil)
}

// AdminCreateUser POST /admin/users（导入 pub.json，校验 attestation）。
func (c *HTTPClient) AdminCreateUser(req *proto.CreateUserRequest) (*proto.CreateUserResponse, error) {
	var resp proto.CreateUserResponse
	if err := c.do(http.MethodPost, "/admin/users", nil, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminRevoke POST /admin/users/{uid}/revoke（吊销成员，二次确认）。
// 返回吊销后已无成员的组名列表（EmptyGroups，供管理端告警）。
func (c *HTTPClient) AdminRevoke(uid, confirmName string) ([]string, error) {
	var resp proto.RevokeResponse
	if err := c.do(http.MethodPost, "/admin/users/"+url.PathEscape(uid)+"/revoke",
		nil, &proto.RevokeRequest{ConfirmName: confirmName}, &resp); err != nil {
		return nil, err
	}
	return resp.EmptyGroups, nil
}

// AdminKeyfileReset POST /admin/users/{uid}/keyfile-reset（keyfile 丢失找回/换绑公钥）。
func (c *HTTPClient) AdminKeyfileReset(uid string, req *proto.KeyfileResetRequest) (*proto.KeyfileResetResponse, error) {
	var resp proto.KeyfileResetResponse
	if err := c.do(http.MethodPost, "/admin/users/"+url.PathEscape(uid)+"/keyfile-reset", nil, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminRekey POST /admin/groups/{gid}/rekey（触发=置位 pending_rekey）。
func (c *HTTPClient) AdminRekey(gid string) (*proto.RekeyResponse, error) {
	var resp proto.RekeyResponse
	if err := c.do(http.MethodPost, "/admin/groups/"+url.PathEscape(gid)+"/rekey", nil, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminArchive POST /admin/groups/{gid}/archive（归档组，二次确认）。
func (c *HTTPClient) AdminArchive(gid, confirmName string) (*proto.ArchiveResponse, error) {
	var resp proto.ArchiveResponse
	if err := c.do(http.MethodPost, "/admin/groups/"+url.PathEscape(gid)+"/archive",
		nil, &proto.ArchiveRequest{ConfirmName: confirmName}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminUnarchive POST /admin/groups/{gid}/unarchive（重启组）。
func (c *HTTPClient) AdminUnarchive(gid string) error {
	return c.do(http.MethodPost, "/admin/groups/"+url.PathEscape(gid)+"/unarchive", nil, nil, nil)
}

// AdminListDevices GET /admin/devices（设备列表，含在线状态/last_ip/hostname）。
func (c *HTTPClient) AdminListDevices() ([]proto.AdminDevice, error) {
	var resp proto.DevicesResponse
	if err := c.do(http.MethodGet, "/admin/devices", nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

// AdminDisableDevice POST /admin/devices/{did}/disable（禁用设备，二次确认）。
func (c *HTTPClient) AdminDisableDevice(did, confirmName string) error {
	return c.do(http.MethodPost, "/admin/devices/"+url.PathEscape(did)+"/disable",
		nil, &proto.DisableDeviceRequest{ConfirmName: confirmName}, nil)
}

// AdminListAudit GET /admin/audit（审计日志，倒序，上限 500）。
func (c *HTTPClient) AdminListAudit(query string) ([]proto.AuditEventOut, error) {
	var resp proto.AuditResponse
	path := "/admin/audit"
	if query != "" {
		path += "?" + query
	}
	if err := c.do(http.MethodGet, path, nil, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Events, nil
}
