package proto

import (
	"encoding/json"
	"testing"
)

func TestHTTPStatusMapping(t *testing.T) {
	cases := map[int]int{
		ErrBadRequest:        400,
		ErrUnauthorized:      401,
		ErrBadSignature:      401,
		ErrBadBootstrap:      401,
		ErrBadChallenge:      401,
		ErrUserRevoked:       403,
		ErrNotMember:         403,
		ErrNotAdmin:          403,
		ErrConflict:          409,
		ErrKeyVersionStale:   409,
		ErrEntryState:        409,
		ErrBadEnvelopes:      409,
		ErrGroupArchived:     409,
		ErrTooLarge:          413,
		ErrRateLimited:       429,
		ErrInternal:          500,
	}
	for code, wantHTTP := range cases {
		if got := HTTPStatus(code); got != wantHTTP {
			t.Errorf("HTTPStatus(%d) = %d, want %d", code, got, wantHTTP)
		}
	}
	// 未知错误码默认 500
	if HTTPStatus(99999) != 500 {
		t.Fatal("未知错误码应映射 500")
	}
}

func TestErrorBodyJSON(t *testing.T) {
	body := ErrorBody{Code: ErrConflict, Message: "seq 冲突", Detail: "extra"}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("序列化结果为空")
	}
}

func TestSyncResponseEmpty(t *testing.T) {
	r := SyncResponse{}
	if r.ServerSeq != 0 || len(r.Changes) != 0 {
		t.Fatal("零值 SyncResponse 应合法")
	}
}
