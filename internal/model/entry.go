// Package model 定义条目信封结构、wire format 与共用类型（§4.3 / §5.1）。
// 三端共享（server / client/core / keytool），不含网络代码。
package model

import (
	"encoding/json"
	"errors"
	"fmt"
)

// 条目类型（§5.1 六选一，type 创建后不可变更）。
const (
	TypeProject  = "project"  // 顶层项目节点，parent_id 为 nil
	TypeEnv      = "env"      // 环境节点（生产/测试/开发）
	TypeIPType   = "ip_type"  // IP 分类节点（如 Web/DB/中间件）
	TypeAccType  = "acc_type" // 账号类型节点（如 OS 账号/数据库账号）
	TypeAccount  = "account"  // 叶子节点，实际账号条目
	TypeCustom   = "custom"   // 万能节点：旁挂各层的补充信息卡片（叶子，不含账号）
)

// SchemaVersion 当前条目明文 schema 版本（§5.1 schema_version 演进规则）。
const SchemaVersion = 1

// MaxCiphertextBytes 单条密文包上限（§5.1 256KB）。
const MaxCiphertextBytes = 256 * 1024

// AllTypes 全部合法类型，供校验用。
var AllTypes = []string{TypeProject, TypeEnv, TypeIPType, TypeAccType, TypeAccount, TypeCustom}

// ValidType 判断 type 是否合法。
func ValidType(t string) bool {
	for _, v := range AllTypes {
		if v == t {
			return true
		}
	}
	return false
}

// Fields 条目字段（账号条目的 user/password/ip/port 等）。
// 为满足"未知字段原样保留"铁律（§5.1），map 值用 json.RawMessage，
// 解析不认识的字段原样保存并在写回时保留。
type Fields map[string]json.RawMessage

// Entry 条目明文结构（§5.1，加密前）。
//
// 铁律（§5.1）：
//   - type 创建后不可变更，不参与冲突合并；
//   - parent_id 存于密文明文里，服务端不可见；
//   - 客户端解析不认识的字段必须原样保留并写回（Extra 兜底）。
type Entry struct {
	SchemaVersion int            `json:"schema_version"`
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	ParentID      *string        `json:"parent_id"` // nil 表示顶层 project
	Fields        Fields         `json:"fields"`
	CustomFields  map[string]json.RawMessage `json:"custom_fields"`
	// Extra 兜底未知顶层字段（§5.1 字段保留铁律）：序列化时先写 Extra 再覆盖已知字段。
	Extra map[string]json.RawMessage `json:"-"`
}

// NewProject 构造顶层项目节点（parent_id=nil）。
func NewProject(title string) *Entry {
	return &Entry{
		SchemaVersion: SchemaVersion,
		Type:          TypeProject,
		Title:         title,
		ParentID:      nil,
		Fields:        Fields{},
		CustomFields:  map[string]json.RawMessage{},
	}
}

// Validate 校验条目明文结构完整性（加密前调用）。
// 注意：服务端不解密，此校验由客户端在加解密时执行。
func (e *Entry) Validate() error {
	if e == nil {
		return errors.New("model: entry 为 nil")
	}
	if !ValidType(e.Type) {
		return fmt.Errorf("model: 非法 type %q", e.Type)
	}
	if e.Type == TypeProject {
		if e.ParentID != nil {
			return errors.New("model: project 节点 parent_id 必须为 nil")
		}
	} else {
		if e.ParentID == nil || *e.ParentID == "" {
			return errors.New("model: 非 project 节点必须指定 parent_id")
		}
	}
	if e.Title == "" {
		return errors.New("model: title 不能为空")
	}
	return nil
}

// Marshal 序列化为 UTF-8 明文 JSON（§9.1 条目加密的输入）。
// 先写 Extra 再覆盖已知字段，保证未知字段原样保留。
func (e *Entry) Marshal() ([]byte, error) {
	if e == nil {
		return nil, errors.New("model: entry 为 nil")
	}
	// 先用临时 map 装入 Extra，再用 json.Marshal 编码结构体字段并覆盖。
	out := make(map[string]json.RawMessage, len(e.Extra)+6)
	for k, v := range e.Extra {
		out[k] = v
	}

	// 显式逐字段写入，确保顺序与类型正确。
	raw, err := e.marshalKnown()
	if err != nil {
		return nil, err
	}
	var known map[string]json.RawMessage
	if err := json.Unmarshal(raw, &known); err != nil {
		return nil, err
	}
	for k, v := range known {
		out[k] = v
	}
	return json.Marshal(out)
}

// marshalKnown 仅编码已知字段（不含 Extra）。
func (e *Entry) marshalKnown() ([]byte, error) {
	type alias Entry
	return json.Marshal((*alias)(e))
}

// UnmarshalEntry 解析条目明文 JSON。
// 未知顶层字段收进 Extra（§5.1 字段保留铁律）；解析失败返回错误。
func UnmarshalEntry(data []byte) (*Entry, error) {
	var e Entry
	if err := json.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("model: 解析 entry: %w", err)
	}
	// 收集未知字段
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	known := map[string]struct{}{
		"schema_version": {},
		"type":           {},
		"title":          {},
		"parent_id":      {},
		"fields":         {},
		"custom_fields":  {},
	}
	for k, v := range m {
		if _, ok := known[k]; !ok {
			if e.Extra == nil {
				e.Extra = map[string]json.RawMessage{}
			}
			e.Extra[k] = v
		}
	}
	return &e, nil
}
