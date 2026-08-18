// Package crypto 国密原语封装（§4 加密体系）。
// 三端共享：server / client/core / keytool / 管理端均 import 本包，不包含任何网络代码。
//
// 全部随机数一律来自 crypto/rand（§14.1 安全不变量 #3）。
package crypto

// Wipe 将字节切片内容清零（内存密钥擦拭）。
// 调用后调用方仍应置 nil / 让切片离开作用域。
func Wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// WipeAll 清零多个字节切片。
func WipeAll(bufs ...[]byte) {
	for _, b := range bufs {
		Wipe(b)
	}
}
