// gen-setup：Windows 本地/测试环境初始化工具
// 生成自签 CA + 服务端 TLS 证书 + 随机 REG_SECRET（写入 setup.env）。
// 全部输出到「当前目录（或参数指定根目录）」的相对子路径，不超出该目录。
//
// 用法: gen-setup.exe [根目录，默认 .]
// 产出:
//   certs/ca.crt   certs/ca.key    —— 自签 CA（客户端 pinning 用 ca.crt）
//   certs/server.crt server.key    —— 服务端 TLS 证书（SAN: localhost + 127.0.0.1 + ::1）
//   setup.env                       —— PB_REG_SECRET/PB_BOOTSTRAP_CODE/PB_ADDR/PB_DATA_DIR/PB_TLS_CERT/PB_TLS_KEY（PB_ 前缀）
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fatal(err)
	}
	certsDir := filepath.Join(root, "certs")
	if err := os.MkdirAll(certsDir, 0o755); err != nil {
		fatal(err)
	}

	// 1. CA（EC P-256，CA:TRUE + keyCertSign，客户端校验必需）
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "passbook-local-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		fatal(err)
	}
	writePEM(filepath.Join(certsDir, "ca.crt"), "CERTIFICATE", caDER)
	writeECKey(filepath.Join(certsDir, "ca.key"), caKey)

	// 2. 服务端证书（SAN: localhost + 回环地址，Windows 本地测试连 127.0.0.1）
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fatal(err)
	}
	srvTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "passbook-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost", "passbook.local"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTpl, caTpl, &srvKey.PublicKey, caKey)
	if err != nil {
		fatal(err)
	}
	writePEM(filepath.Join(certsDir, "server.crt"), "CERTIFICATE", srvDER)
	writeECKey(filepath.Join(certsDir, "server.key"), srvKey)

	// 3. 随机 REG_SECRET + bootstrap token + 端口 → setup.env（PB_ 前缀，服务端直接读取）
	sec := make([]byte, 16)
	if _, err := rand.Read(sec); err != nil {
		fatal(err)
	}
	boot := make([]byte, 32)
	if _, err := rand.Read(boot); err != nil {
		fatal(err)
	}
	bootCode := base64.RawURLEncoding.EncodeToString(boot)

	// 服务端从环境变量读取（server/config.go），KEY 必须 PB_ 前缀；
	// 证书/数据目录用相对包根目录的路径（部署.bat 会 cd 到 %~dp0），均在包内。
	env := fmt.Sprintf(
		"PB_REG_SECRET=%s\n"+
			"PB_BOOTSTRAP_CODE=%s\n"+
			"PB_ADDR=:8443\n"+
			"PB_DATA_DIR=data\n"+
			"PB_TLS_CERT=certs/server.crt\n"+
			"PB_TLS_KEY=certs/server.key\n",
		hex.EncodeToString(sec), bootCode)
	if err := os.WriteFile(filepath.Join(root, "setup.env"), []byte(env), 0o600); err != nil {
		fatal(err)
	}

	fmt.Printf("OK: 证书 + 配置已生成于 %s（certs/ + setup.env）\n", abs)
}

func writePEM(path, typ string, der []byte) {
	f, err := os.Create(path)
	if err != nil {
		fatal(err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: typ, Bytes: der}); err != nil {
		fatal(err)
	}
}

func writeECKey(path string, key *ecdsa.PrivateKey) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		fatal(err)
	}
	writePEM(path, "EC PRIVATE KEY", der)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "gen-setup:", err)
	os.Exit(1)
}
