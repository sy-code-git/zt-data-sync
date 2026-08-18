//go:build !windows

package vault

import "errors"

// errDPAPIUnavailable 非 Windows 平台无 DPAPI，自动解锁不可用（§9.1 自动解锁 Windows 专属）。
var errDPAPIUnavailable = errors.New("自动解锁仅支持 Windows（DPAPI）")

func dpapiProtect(data []byte) ([]byte, error) {
	return nil, errDPAPIUnavailable
}

func dpapiUnprotect(blob []byte) ([]byte, error) {
	return nil, errDPAPIUnavailable
}
