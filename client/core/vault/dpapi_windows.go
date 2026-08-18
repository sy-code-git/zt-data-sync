//go:build windows

package vault

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// maxDPAPIBlobSize DPAPI 输入/输出上限（KEK 16B、blob 数 KB，远小于 4GB）。
// 防御 int→uint32 溢出（gosec G115），超限即拒绝。
const maxDPAPIBlobSize = 1 << 20 // 1 MiB

// dpapiProtect 用 Windows DPAPI（用户级）保护数据（§9.1 自动解锁）。
// CryptProtectData 默认绑定"本机 + 当前 Windows 账户"，经 TPM（若有）加固。
// UI 弹窗被禁止（CRYPTPROTECT_UI_FORBIDDEN），失败即返回错误供调用方回退口令。
func dpapiProtect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("dpapi: 空数据")
	}
	if len(data) > maxDPAPIBlobSize {
		return nil, errors.New("dpapi: 数据超限")
	}
	// #nosec G115 -- 上方已校验 len(data) ≤ 1MiB，int→uint32 转换安全
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) }() // #nosec G103 -- DPAPI 系统调用必须用 unsafe
	blob := make([]byte, out.Size)
	copy(blob, unsafe.Slice(out.Data, out.Size)) // #nosec G103 -- 见上
	return blob, nil
}

// dpapiUnprotect 解开 DPAPI 保护的 blob，取回原始数据（§9.1 自动解锁取回 KEK）。
// 换机器/换账户/数据损坏均返回错误，调用方回退 keyfile 口令解锁。
func dpapiUnprotect(blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, errors.New("dpapi: 空 blob")
	}
	if len(blob) > maxDPAPIBlobSize {
		return nil, errors.New("dpapi: blob 超限")
	}
	// #nosec G115 -- 上方已校验 len(blob) ≤ 1MiB，int→uint32 转换安全
	in := windows.DataBlob{Size: uint32(len(blob)), Data: &blob[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return nil, err
	}
	defer func() { _, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data))) }() // #nosec G103 -- DPAPI 系统调用必须用 unsafe
	data := make([]byte, out.Size)
	copy(data, unsafe.Slice(out.Data, out.Size)) // #nosec G103 -- 见上
	return data, nil
}
