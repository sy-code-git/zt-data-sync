package api

import (
	"net/http"
	"net/url"

	"passbook/internal/proto"
)

// ---- 注册申请（免登录，§6.3 方案 C）----

// RegisterRequest POST /auth/register-request（凭邀请码提交注册申请）。
func (c *HTTPClient) RegisterRequest(req *proto.RegisterRequestRequest) (*proto.RegisterRequestResponse, error) {
	var resp proto.RegisterRequestResponse
	if err := c.do(http.MethodPost, "/auth/register-request", nil, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RegisterStatus GET /auth/register-status?invite_code=xxx（查询审核结果）。
func (c *HTTPClient) RegisterStatus(inviteCode string) (*proto.RegisterStatusResponse, error) {
	var resp proto.RegisterStatusResponse
	if err := c.do(http.MethodGet, "/auth/register-status?invite_code="+url.QueryEscape(inviteCode), nil, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---- admin：邀请码 + 审核（§6.3 方案 C）----

// AdminCreateInvite POST /admin/invites。
func (c *HTTPClient) AdminCreateInvite(req *proto.CreateInviteRequest) (*proto.InviteResponse, error) {
	var resp proto.InviteResponse
	if err := c.do(http.MethodPost, "/admin/invites", nil, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminListInvites GET /admin/invites。
func (c *HTTPClient) AdminListInvites() (*proto.InvitesResponse, error) {
	var resp proto.InvitesResponse
	if err := c.do(http.MethodGet, "/admin/invites", nil, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminListRegisterRequests GET /admin/register-requests?status=xxx。
func (c *HTTPClient) AdminListRegisterRequests(status string) (*proto.RegisterRequestsResponse, error) {
	q := ""
	if status != "" {
		q = "?status=" + url.QueryEscape(status)
	}
	var resp proto.RegisterRequestsResponse
	if err := c.do(http.MethodGet, "/admin/register-requests"+q, nil, nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminApproveRegisterRequest POST /admin/register-requests/:id/approve。
func (c *HTTPClient) AdminApproveRegisterRequest(id, name string) (*proto.RegisterStatusResponse, error) {
	var resp proto.RegisterStatusResponse
	if err := c.do(http.MethodPost, "/admin/register-requests/"+url.PathEscape(id)+"/approve",
		nil, &proto.ReviewRequestRequest{Name: name}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// AdminRejectRegisterRequest POST /admin/register-requests/:id/reject。
func (c *HTTPClient) AdminRejectRegisterRequest(id string) (*proto.RegisterStatusResponse, error) {
	var resp proto.RegisterStatusResponse
	if err := c.do(http.MethodPost, "/admin/register-requests/"+url.PathEscape(id)+"/reject",
		nil, &proto.ReviewRequestRequest{}, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
