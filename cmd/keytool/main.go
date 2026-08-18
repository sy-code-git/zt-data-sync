// keytool 管理端密钥工具 CLI（§10）。
// 全系统唯一的密钥生成路径：genuser 生成 keyfile + pub.json（含注册凭证 attestation）。
// 铁律：不联网（CI 静态检查 import 无网络包）；输出文件权限 0600。
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "genuser":
		err = runGenUser(os.Args[2:])
	case "pubkey":
		err = runPubKey(os.Args[2:])
	case "inspect":
		err = runInspect(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "keytool: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `keytool — passbook 管理端密钥工具（§10）

用法:
  keytool genuser --name zhangsan [--out ./zhangsan]
        生成 SM2 密钥对；交互式设置 keyfile 口令（不回显、二次确认、<12 位强度警告）
        输出 zhangsan.key（口令加密私钥）+ zhangsan.pub.json（{name, sm2_public_key, attestation}）
        需要环境变量 PB_REG_SECRET（与服务端一致的注册凭证密钥，§4.4）

  keytool pubkey --key zhangsan.key [--out pub.der]
        输入口令，显示/导出公钥（用于登记核对）

  keytool inspect --key zhangsan.key
        校验 keyfile 完整性（不解密内容，仅校验格式与 kdf 参数）

环境变量:
  PB_REG_SECRET   注册凭证密钥（genuser 计算 attestation 用，必配）
`)
}
