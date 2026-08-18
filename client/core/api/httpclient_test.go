package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"passbook/internal/proto"
)

func TestHTTPClientRoundTrip(t *testing.T) {
	var kvHeader string // 记录首次 /sync 的 X-Key-Versions（后续空声明请求会覆盖，只记第一次）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/sync":
			// X-Key-Versions 头应含声明（B1）
			if kvHeader == "" {
				kvHeader = r.Header.Get("X-Key-Versions")
			}
			_ = json.NewEncoder(w).Encode(proto.SyncResponse{ServerSeq: 9})
		case "/sync/push":
			_ = json.NewEncoder(w).Encode(proto.PushResponse{Results: []proto.PushResult{{EntryID: "e1", OK: true, NewSeq: 10}}})
		case "/users":
			_ = json.NewEncoder(w).Encode(proto.UsersResponse{Users: []proto.UserInfo{{UserID: "u1", Name: "a"}}})
		case "/groups/g1/keys":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "token")

	// Pull（带版本声明 → X-Key-Versions 头）
	resp, err := c.Pull(0, map[string]int{"g1": 1, "g2": 2})
	if err != nil || resp.ServerSeq != 9 {
		t.Fatalf("Pull: %v %+v", err, resp)
	}
	if kvHeader != "g1:1,g2:2" {
		t.Fatalf("X-Key-Versions = %q, want g1:1,g2:2", kvHeader)
	}
	// 空声明 → 无 header（不报错）
	if _, err := c.Pull(0, nil); err != nil {
		t.Fatalf("Pull empty: %v", err)
	}
	// Push
	pr, err := c.Push([]proto.Mutation{{EntryID: "e1"}})
	if err != nil || !pr.Results[0].OK || pr.Results[0].NewSeq != 10 {
		t.Fatalf("Push: %v %+v", err, pr)
	}
	// ListUsers
	us, err := c.ListUsers()
	if err != nil || len(us) != 1 {
		t.Fatalf("ListUsers: %v %+v", err, us)
	}
	// UploadKeys
	if err := c.UploadKeys("g1", &proto.KeysUploadRequest{KeyVersion: 1}); err != nil {
		t.Fatalf("UploadKeys: %v", err)
	}
}

func TestHTTPClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(proto.ErrorBody{Code: proto.ErrConflict, Message: "conflict"})
	}))
	defer srv.Close()

	c := NewHTTPClient(srv.URL, "token")
	_, err := c.Pull(0, nil)
	if err == nil {
		t.Fatal("应返回错误")
	}
	ae, ok := err.(*APIError)
	if !ok || ae.Code != proto.ErrConflict {
		t.Fatalf("错误类型: %T %v", err, err)
	}
	// 非 JSON 错误
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer srv2.Close()
	c2 := NewHTTPClient(srv2.URL, "token")
	if _, err := c2.Pull(0, nil); err == nil {
		t.Fatal("非 JSON 错误也应返回错误")
	}
}
