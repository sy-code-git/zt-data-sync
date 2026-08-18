package model

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEntryValidate(t *testing.T) {
	// 合法 project
	if err := NewProject("生产环境").Validate(); err != nil {
		t.Fatalf("project 校验失败: %v", err)
	}
	// 合法 account
	p := "parent-uuid"
	acc := &Entry{SchemaVersion: 1, Type: TypeAccount, Title: "root", ParentID: &p, Fields: Fields{}, CustomFields: map[string]json.RawMessage{}}
	if err := acc.Validate(); err != nil {
		t.Fatalf("account 校验失败: %v", err)
	}
	// 非法 type
	bad := &Entry{SchemaVersion: 1, Type: "nope", Title: "x", ParentID: &p}
	if err := bad.Validate(); err == nil {
		t.Fatal("非法 type 应校验失败")
	}
	// project 带 parent_id
	bad2 := &Entry{SchemaVersion: 1, Type: TypeProject, Title: "x", ParentID: &p}
	if err := bad2.Validate(); err == nil {
		t.Fatal("project 带 parent_id 应校验失败")
	}
	// 非 project 缺 parent_id
	bad3 := &Entry{SchemaVersion: 1, Type: TypeEnv, Title: "x"}
	if err := bad3.Validate(); err == nil {
		t.Fatal("非 project 缺 parent_id 应校验失败")
	}
	// 空 title
	bad4 := &Entry{SchemaVersion: 1, Type: TypeProject, Title: ""}
	if err := bad4.Validate(); err == nil {
		t.Fatal("空 title 应校验失败")
	}
	// nil entry
	var nilE *Entry
	if err := nilE.Validate(); err == nil {
		t.Fatal("nil entry 应校验失败")
	}
}

func TestEntryMarshalRoundTrip(t *testing.T) {
	p := "parent-1"
	e := &Entry{
		SchemaVersion: 1,
		Type:          TypeAccount,
		Title:         "prod root",
		ParentID:      &p,
		Fields: Fields{
			"user": json.RawMessage(`"root"`),
		},
		CustomFields: map[string]json.RawMessage{
			"机房": json.RawMessage(`"深圳"`),
		},
		Extra: map[string]json.RawMessage{
			"future_field": json.RawMessage(`{"a":1}`),
		},
	}
	data, err := e.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := UnmarshalEntry(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Type != TypeAccount || parsed.Title != "prod root" {
		t.Fatalf("解析结果不一致: %+v", parsed)
	}
	if *parsed.ParentID != p {
		t.Fatalf("parent_id 不一致: %q", *parsed.ParentID)
	}
	// 未知字段保留
	if _, ok := parsed.Extra["future_field"]; !ok {
		t.Fatal("未知字段未保留进 Extra")
	}
	// 写回后 Extra 仍在
	data2, err := parsed.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data2, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["future_field"]; !ok {
		t.Fatal("写回后未知字段丢失")
	}
}

func TestEntryMarshalKnownFieldPriority(t *testing.T) {
	// 已知字段覆盖 Extra 中同名键
	e := &Entry{
		SchemaVersion: 1, Type: TypeProject, Title: "t",
		Fields: Fields{}, CustomFields: map[string]json.RawMessage{},
		Extra: map[string]json.RawMessage{"title": json.RawMessage(`"wrong"`)},
	}
	data, _ := e.Marshal()
	var m map[string]json.RawMessage
	_ = json.Unmarshal(data, &m)
	var title string
	_ = json.Unmarshal(m["title"], &title)
	if title != "t" {
		t.Fatalf("已知字段应覆盖 Extra，title=%q", title)
	}
}

func TestValidType(t *testing.T) {
	for _, ty := range AllTypes {
		if !ValidType(ty) {
			t.Fatalf("合法类型 %q 被判非法", ty)
		}
	}
	if ValidType("other") {
		t.Fatal("非法类型应 false")
	}
}

func TestUnmarshalEntryError(t *testing.T) {
	if _, err := UnmarshalEntry([]byte("not json")); err == nil {
		t.Fatal("垃圾 JSON 应解析失败")
	}
}

// ---- wire ----

func TestCiphertextRoundTrip(t *testing.T) {
	c := NewCiphertext(2, []byte("nonce012345"), []byte("cipher"), bytes.Repeat([]byte{0xAA}, 32))
	data, err := MarshalCiphertext(c)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseCiphertext(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.V != 1 || parsed.Alg != "SM4-GCM" || parsed.KV != 2 {
		t.Fatalf("解析不一致: %+v", parsed)
	}
	if !bytes.Equal(parsed.Nonce, c.Nonce) || !bytes.Equal(parsed.CT, c.CT) || !bytes.Equal(parsed.HMAC, c.HMAC) {
		t.Fatal("字段不一致")
	}
}

func TestCiphertextErrors(t *testing.T) {
	if _, err := ParseCiphertext([]byte("bad")); err == nil {
		t.Fatal("垃圾 JSON 应失败")
	}
	// 版本过高
	if _, err := ParseCiphertext([]byte(`{"v":99,"alg":"SM4-GCM"}`)); err == nil {
		t.Fatal("过高版本应失败")
	}
	// 版本缺失/为 0（违反"全部带 v 版本字段"铁律）
	if _, err := ParseCiphertext([]byte(`{"v":0,"alg":"SM4-GCM"}`)); err == nil {
		t.Fatal("v=0 应失败")
	}
	// 不支持算法
	if _, err := ParseCiphertext([]byte(`{"v":1,"alg":"AES"}`)); err == nil {
		t.Fatal("不支持算法应失败")
	}
	// nil Marshal
	if _, err := MarshalCiphertext(nil); err == nil {
		t.Fatal("nil 序列化应失败")
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	e := NewKeyEnvelope([]byte("wrapped-dek-bytes"))
	data, err := MarshalEnvelope(e)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.V != 1 || parsed.Alg != "SM2-C1C3C2" || !bytes.Equal(parsed.Data, e.Data) {
		t.Fatalf("解析不一致: %+v", parsed)
	}
}

func TestEnvelopeErrors(t *testing.T) {
	if _, err := ParseEnvelope([]byte("bad")); err == nil {
		t.Fatal("垃圾 JSON 应失败")
	}
	if _, err := ParseEnvelope([]byte(`{"v":99,"alg":"SM2-C1C3C2"}`)); err == nil {
		t.Fatal("过高版本应失败")
	}
	if _, err := ParseEnvelope([]byte(`{"v":0,"alg":"SM2-C1C3C2"}`)); err == nil {
		t.Fatal("v=0 应失败")
	}
	if _, err := ParseEnvelope([]byte(`{"v":1,"alg":"RSA"}`)); err == nil {
		t.Fatal("不支持算法应失败")
	}
	if _, err := MarshalEnvelope(nil); err == nil {
		t.Fatal("nil 序列化应失败")
	}
}

func TestMaxCiphertextBytes(t *testing.T) {
	if MaxCiphertextBytes != 256*1024 {
		t.Fatalf("MaxCiphertextBytes = %d, want 262144", MaxCiphertextBytes)
	}
}

func TestEntryMarshalNilReceiver(t *testing.T) {
	var e *Entry
	if _, err := e.Marshal(); err == nil {
		t.Fatal("nil entry Marshal 应报错")
	}
}
