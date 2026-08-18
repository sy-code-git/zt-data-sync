package store

import (
	"context"
	"errors"
	"testing"
)

// 1.4 新增方法的 CRUD 与约束映射测试（覆盖 store 层直接行为）。

func TestUserCRUD(t *testing.T) {
	s := newTestStore(t)
	u := &User{ID: "u1", Name: "zhangsan", SM2PublicKey: "pub", Attestation: "att",
		Role: RoleMember, Status: StatusActive, CreatedAt: 100}

	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateUser(u) }); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := s.GetUserByID("u1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "zhangsan" || got.Role != RoleMember || got.Attestation != "att" {
		t.Fatalf("GetUserByID 结果不符: %+v", got)
	}

	// 按 name / 公钥查询
	if _, err := s.GetUserByName("zhangsan"); err != nil {
		t.Fatalf("GetUserByName: %v", err)
	}
	if _, err := s.GetUserByPublicKey("pub"); err != nil {
		t.Fatalf("GetUserByPublicKey: %v", err)
	}

	// 公钥查重是应用层职责（DDL 无 UNIQUE，§6.3 admin 建用户前 GetUserByPublicKey 检查）：
	// 同公钥可插入（DB 允许），GetUserByPublicKey 返回首个
	dup := &User{ID: "u2", Name: "lisi", SM2PublicKey: "pub", Attestation: "att2",
		Role: RoleMember, Status: StatusActive, CreatedAt: 101}
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateUser(dup) }); err != nil {
		t.Fatalf("同公钥插入应被 DB 允许（应用层查重）: %v", err)
	}
	first, _ := s.GetUserByPublicKey("pub")
	if first.ID != "u1" {
		t.Fatalf("GetUserByPublicKey 应返回首个: %+v", first)
	}

	// 吊销
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.SetUserRevoked("u1", 200) }); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUserByID("u1")
	if got.Status != StatusRevoked || got.RevokedAt != 200 {
		t.Fatalf("吊销未生效: %+v", got)
	}

	// 换绑公钥
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.ReplaceUserPublicKey("u1", "pub-new", "att-new")
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetUserByID("u1")
	if got.SM2PublicKey != "pub-new" || got.Attestation != "att-new" {
		t.Fatalf("换绑未生效: %+v", got)
	}
}

func TestDeviceCRUD(t *testing.T) {
	s := newTestStore(t)
	// 先建用户（外键）
	u := &User{ID: "u1", Name: "a", SM2PublicKey: "p", Attestation: "x", Role: RoleMember, Status: StatusActive, CreatedAt: 1}
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateUser(u) }); err != nil {
		t.Fatal(err)
	}

	d := &Device{ID: "d1", UserID: "u1", Name: "mbp", Hostname: "WIN", TokenHash: "h1",
		Status: DeviceActive, CreatedAt: 2}
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateDevice(d) }); err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}

	// token_hash 唯一
	d2 := &Device{ID: "d2", UserID: "u1", Name: "pc", TokenHash: "h1", Status: DeviceActive, CreatedAt: 3}
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.CreateDevice(d2) }); !errors.Is(err, ErrConstraintUnique) {
		t.Fatalf("重复 token_hash 应 ErrConstraintUnique, got %v", err)
	}

	// 查询
	got, err := s.GetDeviceByTokenHash("h1")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "d1" || got.Hostname != "WIN" {
		t.Fatalf("GetDeviceByTokenHash: %+v", got)
	}
	if _, err := s.GetDeviceByID("d1"); err != nil {
		t.Fatal(err)
	}
	devs, err := s.ListDevicesByUser("u1")
	if err != nil || len(devs) != 1 {
		t.Fatalf("ListDevicesByUser: %v, %+v", err, devs)
	}

	// 心跳更新
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.UpdateDeviceSeen("d1", "WIN-NEW", 999)
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDeviceByID("d1")
	if got.Hostname != "WIN-NEW" || got.LastSeen != 999 {
		t.Fatalf("心跳更新未生效: %+v", got)
	}

	// IP 更新
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.UpdateDeviceIP("d1", "10.0.1.9")
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDeviceByID("d1")
	if got.LastIP != "10.0.1.9" {
		t.Fatalf("IP 更新未生效: %+v", got)
	}

	// 禁用单台
	if err := s.WithTx(context.Background(), func(tx Tx) error { return tx.DisableDevice("d1") }); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDeviceByID("d1")
	if got.Status != DeviceDisabled {
		t.Fatalf("禁用未生效: %+v", got)
	}

	// 作废用户全部设备（先恢复 d1 active 再作废）
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.DisableUserDevices("u1")
	}); err != nil {
		t.Fatal(err)
	}
	devs, _ = s.ListDevicesByUser("u1")
	for _, dv := range devs {
		if dv.Status != DeviceDisabled {
			t.Fatalf("DisableUserDevices 后仍有 active: %+v", dv)
		}
	}
}

func TestAuditWrite(t *testing.T) {
	s := newTestStore(t)
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.Audit(&AuditEvent{TS: 1, DeviceID: "d1", UserID: "u1", Action: "create_user",
			IP: "1.2.3.4", DeviceName: "mbp", Hostname: "WIN"})
	}); err != nil {
		t.Fatalf("Audit: %v", err)
	}
	// 空字符串字段转 NULL 不应报错
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.Audit(&AuditEvent{TS: 2, DeviceID: "d1", UserID: "u1", Action: "push"})
	}); err != nil {
		t.Fatalf("Audit(空字段): %v", err)
	}
}

func TestGetUserByIDNoRows(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.GetUserByID("nope"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("不存在用户应 ErrNoRows, got %v", err)
	}
	if _, err := s.GetDeviceByTokenHash("nope"); !errors.Is(err, ErrNoRows) {
		t.Fatalf("不存在设备应 ErrNoRows, got %v", err)
	}
}

func TestGetUserCount(t *testing.T) {
	s := newTestStore(t)
	n, err := s.GetUserCount()
	if err != nil || n != 0 {
		t.Fatalf("初始用户数 = %d, err=%v", n, err)
	}
	if err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.CreateUser(&User{ID: "u1", Name: "a", SM2PublicKey: "p", Attestation: "x",
			Role: RoleMember, Status: StatusActive, CreatedAt: 1})
	}); err != nil {
		t.Fatal(err)
	}
	n, _ = s.GetUserCount()
	if n != 1 {
		t.Fatalf("用户数 = %d, want 1", n)
	}
}

func TestCreateUserFK(t *testing.T) {
	// devices 引用不存在的 user → 外键冲突
	s := newTestStore(t)
	err := s.WithTx(context.Background(), func(tx Tx) error {
		return tx.CreateDevice(&Device{ID: "d1", UserID: "no-user", Name: "x", TokenHash: "h",
			Status: DeviceActive, CreatedAt: 1})
	})
	if !errors.Is(err, ErrConstraintFK) {
		t.Fatalf("悬空 user 引用应 ErrConstraintFK, got %v", err)
	}
}
