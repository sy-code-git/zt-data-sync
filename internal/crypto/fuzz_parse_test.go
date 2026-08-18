package crypto

import (
	"crypto/rand"
	"testing"
)

// 网络输入面回归测试：SM2 DER 反序列化与验签对任意畸形输入必须返回 error / false，
// 绝不 panic（服务端设备注册 / 建用户时客户端上报公钥与签名）。

func TestUnmarshalSM2NeverPanics(t *testing.T) {
	// 随机字节（含各种长度）
	for _, n := range []int{0, 1, 2, 10, 32, 64, 96, 128, 200} {
		buf := make([]byte, n)
		_, _ = rand.Read(buf)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("UnmarshalSM2PublicKey panicked on random %dB input: %v", n, r)
				}
			}()
			_, _ = UnmarshalSM2PublicKey(buf)
		}()
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("UnmarshalSM2PrivateKey panicked on random %dB input: %v", n, r)
				}
			}()
			_, _ = UnmarshalSM2PrivateKey(buf)
		}()
	}
	// 截断的合法 DER：先生成合法公钥 DER 再逐段截断
	priv, _ := GenerateSM2Key()
	der, _ := MarshalSM2PublicKey(&priv.PublicKey)
	for cut := 0; cut < len(der); cut++ {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("UnmarshalSM2PublicKey panicked on truncated DER len=%d: %v", cut, r)
				}
			}()
			_, _ = UnmarshalSM2PublicKey(der[:cut])
		}()
	}
}

func TestSM2VerifyNeverPanics(t *testing.T) {
	priv, _ := GenerateSM2Key()
	msg := []byte("challenge")
	for _, n := range []int{0, 1, 5, 16, 64, 128} {
		buf := make([]byte, n)
		_, _ = rand.Read(buf)
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("SM2VerifyChallenge panicked on %dB signature: %v", n, r)
				}
			}()
			_ = SM2VerifyChallenge(&priv.PublicKey, msg, buf)
		}()
	}
}
