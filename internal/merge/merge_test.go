package merge

import (
	"encoding/json"
	"testing"
)

// 测试用：JSON 字符串转 json.RawMessage。
func raw(t *testing.T, s string) json.RawMessage {
	t.Helper()
	if s == "" {
		return nil
	}
	var v json.RawMessage
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad json %q: %v", s, err)
	}
	return v
}

func TestMergeJSON_OneSideChange(t *testing.T) {
	base := []byte(`{"type":"account","title":"root","fields":{"password":"a","note":"n"}}`)
	ours := []byte(`{"type":"account","title":"root","fields":{"password":"b","note":"n"}}`)
	theirs := []byte(`{"type":"account","title":"root","fields":{"password":"a","note":"n2"}}`)
	// ours 改 password，theirs 改 note → 应自动合并
	res, err := MergeJSON(base, ours, theirs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.HasConflict {
		t.Fatalf("不应有冲突，got %v", res.Conflicts)
	}
	var m map[string]any
	if err := json.Unmarshal(res.Merged, &m); err != nil {
		t.Fatal(err)
	}
	fields := m["fields"].(map[string]any)
	if fields["password"] != "b" {
		t.Errorf("password 应取 ours=b，got %v", fields["password"])
	}
	if fields["note"] != "n2" {
		t.Errorf("note 应取 theirs=n2，got %v", fields["note"])
	}
}

func TestMergeJSON_Conflict(t *testing.T) {
	base := []byte(`{"type":"account","title":"root","fields":{"password":"a"}}`)
	ours := []byte(`{"type":"account","title":"root","fields":{"password":"b"}}`)
	theirs := []byte(`{"type":"account","title":"root","fields":{"password":"c"}}`)
	res, err := MergeJSON(base, ours, theirs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.HasConflict {
		t.Fatal("应检测到冲突")
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Key != "fields.password" {
		t.Fatalf("冲突路径应为 fields.password，got %v", res.Conflicts)
	}
	// 冲突字段应有标记
	var m map[string]map[string]json.RawMessage
	_ = json.Unmarshal(res.Merged, &m)
	if !HasConflictMark(m["fields"]["password"]) {
		t.Error("冲突字段应含标记")
	}
}

func TestMergeJSON_Immutable(t *testing.T) {
	base := []byte(`{"type":"account","title":"x"}`)
	ours := []byte(`{"type":"env","title":"x"}`)
	theirs := []byte(`{"type":"account","title":"y"}`)
	_, err := MergeJSON(base, ours, theirs, Options{ImmutableFields: []string{"type"}})
	if err == nil {
		t.Fatal("不可变字段 type 被变更应报错")
	}
}

func TestMergeJSON_NestedRecursive(t *testing.T) {
	// 深层嵌套对象也应递归合并
	base := []byte(`{"custom_fields":{"a":{"x":1,"y":2}}}`)
	ours := []byte(`{"custom_fields":{"a":{"x":9,"y":2}}}`)
	theirs := []byte(`{"custom_fields":{"a":{"x":1,"y":8}}}`)
	res, err := MergeJSON(base, ours, theirs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.HasConflict {
		t.Fatalf("嵌套对象不同键应自动合并，got %v", res.Conflicts)
	}
	var m map[string]map[string]map[string]int
	_ = json.Unmarshal(res.Merged, &m)
	if m["custom_fields"]["a"]["x"] != 9 || m["custom_fields"]["a"]["y"] != 8 {
		t.Errorf("嵌套合并结果错误: %v", m["custom_fields"]["a"])
	}
}

func TestMergeJSON_BaseNil(t *testing.T) {
	ours := []byte(`{"type":"account","fields":{"password":"b"}}`)
	theirs := []byte(`{"type":"account","fields":{"password":"c"}}`)
	res, err := MergeJSON(nil, ours, theirs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	// base 为空：ours/theirs 视为各自都"改"，password 不同 → 冲突
	if !res.HasConflict {
		t.Fatal("base 为空且双方 password 不同应冲突")
	}
}

func TestResolveField(t *testing.T) {
	base := []byte(`{"fields":{"password":"a"}}`)
	ours := []byte(`{"fields":{"password":"b"}}`)
	theirs := []byte(`{"fields":{"password":"c"}}`)
	res, _ := MergeJSON(base, ours, theirs, Options{})

	// 采用本地
	out, err := ResolveField(res.Merged, "fields.password", ChoiceLocal, nil)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]string
	_ = json.Unmarshal(out, &m)
	if m["fields"]["password"] != "b" {
		t.Errorf("采用本地应=b，got %v", m["fields"]["password"])
	}

	// 采用服务端
	out, err = ResolveField(res.Merged, "fields.password", ChoiceServer, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(out, &m)
	if m["fields"]["password"] != "c" {
		t.Errorf("采用服务端应=c，got %v", m["fields"]["password"])
	}

	// 手动
	out, err = ResolveField(res.Merged, "fields.password", ChoiceManual, raw(t, `"manual"`))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(out, &m)
	if m["fields"]["password"] != "manual" {
		t.Errorf("手动应=manual，got %v", m["fields"]["password"])
	}
}
