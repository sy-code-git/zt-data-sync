package crypto

import (
	"bytes"
	"testing"
)

// 验证：SM4GCMOpen 收到空/短密文是否 panic（标准库 GCM Open 对短密文直接 panic）
func TestSM4GCMOpenShortCiphertext(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, SM4KeySize)
	nonce := make([]byte, SM4NonceSize)

	for _, ct := range [][]byte{nil, {}, {0x01}, make([]byte, 15)} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("SM4GCMOpen panicked on len=%d ciphertext: %v", len(ct), r)
				}
			}()
			_, err := SM4GCMOpen(key, nonce, nil, ct)
			if err == nil {
				t.Fatalf("len=%d ciphertext should error, got nil", len(ct))
			}
		}()
	}
}
