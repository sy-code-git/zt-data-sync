// Package merge 通用 JSON 三路合并器（§7.3）。
//
// 零业务依赖的纯函数库：对任意 JSON 结构做 Git 式三路合并，
// 不依赖 model.Entry、不硬编码任何字段名（title/parent_id/fields 均不识别）。
// core 冲突合并、UI/CLI 均可复用；未知字段天然递归保留。
package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// FieldConflict 单个字段的冲突（§7.3 冲突标记）。
type FieldConflict struct {
	Key    string          `json:"key"`              // 字段路径，如 "fields.password"
	Base   json.RawMessage `json:"base,omitempty"`   // 共同祖先值
	Ours   json.RawMessage `json:"ours,omitempty"`   // 本地修改值
	Theirs json.RawMessage `json:"theirs,omitempty"` // 服务端当前值
}

// Options 合并选项。
type Options struct {
	// ImmutableFields 不可变字段路径（点分，如 "type"）。
	// 不可变字段不参与三态合并；若检测到本地对其做了变更（base 与 ours 不同）则报错。
	ImmutableFields []string
}

// Result 三路合并结果。
type Result struct {
	Merged      []byte          // 合并后的明文 JSON（冲突字段已标标记）
	Conflicts   []FieldConflict // 冲突字段列表（空 = 自动合并成功）
	HasConflict bool
}

// 冲突标记（Git 式，§7.3）。
const (
	markStart = "<<<<<<< ours"
	markSep   = "======="
	markEnd   = ">>>>>>> theirs"
)

// MergeJSON 执行通用 JSON 三路合并（纯函数，无副作用）。
// base/ours/theirs 为同一 entry 的三种明文 JSON 版本；base 可为 nil（无祖先快照）。
// 顶层必须是 JSON 对象；嵌套对象递归合并，标量/数组整体比较（数组不做行级拼接）。
func MergeJSON(base, ours, theirs []byte, opts Options) (*Result, error) {
	if len(ours) == 0 || len(theirs) == 0 {
		return nil, errors.New("merge: ours/theirs 不能为空")
	}
	om, err := parseObject(ours)
	if err != nil {
		return nil, fmt.Errorf("merge: ours 非 JSON 对象: %w", err)
	}
	tm, err := parseObject(theirs)
	if err != nil {
		return nil, fmt.Errorf("merge: theirs 非 JSON 对象: %w", err)
	}
	var bm map[string]json.RawMessage
	if len(base) > 0 {
		if bm, err = parseObject(base); err != nil {
			return nil, fmt.Errorf("merge: base 非 JSON 对象: %w", err)
		}
	}

	// 不可变字段预校验（仅顶层路径；§7.3 type 不可变）
	for _, f := range opts.ImmutableFields {
		b, o := bm[f], om[f]
		if b != nil && !equalJSON(b, o) {
			return nil, fmt.Errorf("merge: 不可变字段 %q 被变更", f)
		}
	}

	res := &Result{Conflicts: []FieldConflict{}}
	merged := mergeObjects(res, bm, om, tm, "")
	out, err := marshalObject(merged)
	if err != nil {
		return nil, err
	}
	res.Merged = out
	res.HasConflict = len(res.Conflicts) > 0
	return res, nil
}

// parseObject 解析 JSON 对象为 map（保留字段原文，不丢失未知字段）。
func parseObject(data []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func marshalObject(m map[string]json.RawMessage) ([]byte, error) {
	return marshalNoEscape(m), nil
}

// marshalNoEscape 序列化任意值为 JSON 字节（关闭 HTML 转义，保留 < > & 字面）。
// 关键：json.Marshal 默认会把 RawMessage 里的 < 转义成 \u003c，
// 会破坏冲突标记的可读性与 HasConflictMark 匹配，必须用 SetEscapeHTML(false)。
func marshalNoEscape(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// mergeObjects 递归合并三个对象的字段集合。
func mergeObjects(res *Result, base, ours, theirs map[string]json.RawMessage, path string) map[string]json.RawMessage {
	keys := map[string]bool{}
	for k := range base {
		keys[k] = true
	}
	for k := range ours {
		keys[k] = true
	}
	for k := range theirs {
		keys[k] = true
	}
	sorted := make([]string, 0, len(keys))
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	out := make(map[string]json.RawMessage, len(sorted))
	for _, k := range sorted {
		keyPath := k
		if path != "" {
			keyPath = path + "." + k
		}
		out[k] = mergeValue(res, base[k], ours[k], theirs[k], keyPath)
	}
	return out
}

// mergeValue 单个字段三态合并（§7.3：仅一方改→采用；都没改→保留；都改不同→冲突/递归）。
func mergeValue(res *Result, base, ours, theirs json.RawMessage, path string) json.RawMessage {
	switch {
	case equalJSON(ours, base):
		return theirs
	case equalJSON(theirs, base):
		return ours
	case equalJSON(ours, theirs):
		return ours
	default:
		// 双方都改且不同：若三方都是对象则递归合并，否则冲突标记
		if allObjects(base, ours, theirs) {
			bm, _ := parseObject(base)
			om, _ := parseObject(ours)
			tm, _ := parseObject(theirs)
			return marshalMust(mergeObjects(res, bm, om, tm, path))
		}
		res.Conflicts = append(res.Conflicts, FieldConflict{Key: path, Base: base, Ours: ours, Theirs: theirs})
		return markConflict(ours, theirs)
	}
}

// allObjects 判断三个值是否都是 JSON 对象（base 可 nil）。
func allObjects(base, ours, theirs json.RawMessage) bool {
	for _, v := range []json.RawMessage{base, ours, theirs} {
		if len(v) == 0 {
			continue
		}
		t := bytes.TrimSpace(v)
		if len(t) == 0 || t[0] != '{' {
			return false
		}
	}
	return len(ours) > 0 && len(theirs) > 0 && bytes.TrimSpace(ours)[0] == '{' && bytes.TrimSpace(theirs)[0] == '{'
}

func marshalMust(m map[string]json.RawMessage) json.RawMessage {
	return marshalNoEscape(m)
}

// markConflict 生成冲突标记值（§7.3 Git 式）。
// 用 SetEscapeHTML(false) 生成 JSON 字符串字面量，不转义 < > &，
// 保证 HasConflictMark 能直接命中标记、ResolveField 能无损还原 ours/theirs。
func markConflict(ours, theirs json.RawMessage) json.RawMessage {
	mark := markStart + "\n" + string(ours) + "\n" + markSep + "\n" + string(theirs) + "\n" + markEnd
	return json.RawMessage(marshalNoEscape(mark))
}

// parseMark 从冲突标记文本还原 ours/theirs 原始 JSON 原文。
func parseMark(mark string) (ours, theirs string, ok bool) {
	p1 := strings.Index(mark, markStart+"\n")
	if p1 < 0 {
		return "", "", false
	}
	sep := "\n" + markSep + "\n"
	p2 := strings.Index(mark, sep)
	if p2 < 0 {
		return "", "", false
	}
	p3 := strings.Index(mark, "\n"+markEnd)
	if p3 < 0 || p3 < p2 {
		return "", "", false
	}
	ours = mark[p1+len(markStart)+1 : p2]
	theirs = mark[p2+len(sep) : p3]
	return ours, theirs, true
}

// HasConflictMark 判断值是否含冲突标记（UI 冲突解决页用）。
func HasConflictMark(v json.RawMessage) bool {
	return bytes.Contains(v, []byte(markStart))
}

// equalJSON 判断两个 JSON 值是否相等（nil 视为"不存在"）。
func equalJSON(a, b json.RawMessage) bool {
	a, b = normalize(a), normalize(b)
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return bytes.Equal(a, b)
}

func normalize(v json.RawMessage) json.RawMessage {
	if len(v) == 0 {
		return nil
	}
	var buf bytes.Buffer
	_ = json.Compact(&buf, v)
	return buf.Bytes()
}

// ---- 冲突解决（§7.3）----

// Choice 冲突解决选择。
type Choice int

const (
	ChoiceLocal Choice = iota
	ChoiceServer
	ChoiceManual
)

// ResolveField 应用单个字段的冲突解决结果，返回新的合并 JSON。
// merged 为 MergeJSON 产出的合并结果；field 为点分字段路径（如 "fields.password"）。
// ChoiceManual 直接写入手动值；ChoiceLocal/ChoiceServer 从字段当前冲突标记还原对应方原始值。
func ResolveField(merged []byte, field string, choice Choice, manual json.RawMessage) ([]byte, error) {
	obj, err := parseObject(merged)
	if err != nil {
		return nil, fmt.Errorf("merge: merged 非 JSON 对象: %w", err)
	}
	if choice == ChoiceManual {
		if err := setFieldByPath(obj, field, manual); err != nil {
			return nil, err
		}
		return marshalObject(obj)
	}
	cur, err := getFieldByPath(obj, field)
	if err != nil {
		return nil, err
	}
	var s string
	if err := json.Unmarshal(cur, &s); err != nil {
		return nil, errors.New("merge: 字段值非冲突标记字符串")
	}
	ours, theirs, ok := parseMark(s)
	if !ok {
		return nil, errors.New("merge: 字段值非冲突标记，无法采用本地/服务端")
	}
	var v json.RawMessage
	switch choice {
	case ChoiceLocal:
		v = json.RawMessage(ours)
	case ChoiceServer:
		v = json.RawMessage(theirs)
	default:
		return nil, errors.New("merge: 未知解决方式")
	}
	if err := setFieldByPath(obj, field, v); err != nil {
		return nil, err
	}
	return marshalObject(obj)
}

// getFieldByPath 按点分路径取字段值（顶层或一层嵌套 map）。
func getFieldByPath(obj map[string]json.RawMessage, field string) (json.RawMessage, error) {
	parts := strings.Split(field, ".")
	if len(parts) == 0 || parts[0] == "" {
		return nil, errors.New("merge: 空字段路径")
	}
	if len(parts) == 1 {
		v, ok := obj[parts[0]]
		if !ok {
			return nil, fmt.Errorf("merge: 字段 %q 不存在", field)
		}
		return v, nil
	}
	sub, ok := obj[parts[0]]
	if !ok {
		return nil, fmt.Errorf("merge: 字段 %q 不存在", field)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(sub, &m); err != nil {
		return nil, fmt.Errorf("merge: 中间路径 %q 非对象", parts[0])
	}
	v, ok := m[parts[1]]
	if !ok {
		return nil, fmt.Errorf("merge: 字段 %q 不存在", field)
	}
	return v, nil
}

// setFieldByPath 按点分路径写字段值（顶层或一层嵌套 map；中间对象缺失时创建）。
func setFieldByPath(obj map[string]json.RawMessage, field string, v json.RawMessage) error {
	parts := strings.Split(field, ".")
	if len(parts) == 0 || parts[0] == "" {
		return errors.New("merge: 空字段路径")
	}
	if len(parts) == 1 {
		obj[parts[0]] = v
		return nil
	}
	m := map[string]json.RawMessage{}
	if raw, ok := obj[parts[0]]; ok && len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	m[parts[1]] = v
	obj[parts[0]] = marshalMust(m)
	return nil
}
