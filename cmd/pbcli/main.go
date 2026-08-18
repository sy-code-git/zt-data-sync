// pbcli 客户端核心库无 UI 命令行 harness（§14.1 步骤 1.7 产出物）。
// 驱动 client/core（vault + syncer + local store），供测试/脚本/无 UI 场景使用。
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/term"

	"passbook/client/core/api"
	"passbook/client/core/store"
	"passbook/client/core/syncer"
	"passbook/client/core/vault"
	"passbook/internal/model"
)

// ctx 运行时上下文（解锁态）。
type ctx struct {
	vault  *vault.Vault
	local  store.LocalStore
	engine *syncer.Engine
	hc     *api.HTTPClient
}

var app = &ctx{}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	var err error
	switch os.Args[1] {
	case "unlock":
		err = cmdUnlock(os.Args[2:])
	case "bootstrap":
		err = cmdBootstrap(os.Args[2:])
	case "admin":
		err = cmdAdmin(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
	case "put":
		err = cmdPut(os.Args[2:])
	case "delete":
		err = cmdDelete(os.Args[2:])
	case "sync":
		err = cmdSync(os.Args[2:])
	case "genpass":
		err = cmdGenPass(os.Args[2:])
	case "autounlock":
		err = cmdAutoUnlock(os.Args[2:])
	case "sse-listen":
		err = cmdSSEListen(os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pbcli: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `pbcli — 客户端核心库无 UI harness（§14.1 1.7）

用法:
  pbcli unlock --keyfile <xxx.key> --server <url> [--user <user_id>] [--device <设备名>] [--data <dir>]
        导入 keyfile 解锁（交互输口令），连接服务端启动同步；
        首次注册设备时需 --user（admin 建用户返回的 user_id）
  pbcli bootstrap --server <url> --token <bootstrap> --keyfile <xxx.key> --name <管理员名> [--data <dir>]
        首启引导：用 bootstrap token + keyfile 公钥建立首个 admin
  pbcli admin <sub> ...
        管理操作（需先 unlock 用 admin keyfile）：
        create-user --pub <pub.json> | create-group --name <n> |
        add-member --group <gid> --user <uid> | revoke --user <uid> --confirm <name> |
        rekey --group <gid> | archive --group <gid> --confirm <name> | unarchive --group <gid> |
        members --group <gid> | devices | groups | audit [--action <a>]
  pbcli list
        列出已解密条目（树形：project 节点）
  pbcli put --group <gid> --title <title> [--type project]
        新建条目（本地加密 + 待推送）
  pbcli sync
        手动触发一轮同步
  pbcli genpass [--length 16]
        生成随机密码
  pbcli autounlock <enable|disable|status|try> [--data <dir>]
        DPAPI 自动解锁管理（Windows）：enable 口令解锁后开启 / disable 关闭 /
        status 查询 / try 免口令解锁（重启免输口令闭环）
`)
}

// readPass 读取口令（可注入：测试替换用）。
// 非交互脚本可通过 PB_PASSWORD 环境变量提供口令（e2e 验收脚本用），
// 否则回退 term.ReadPassword 交互输入（不回显）。
var readPass = func(prompt string) ([]byte, error) {
	if p := os.Getenv("PB_PASSWORD"); p != "" {
		return []byte(p), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return pw, err
}

func cmdUnlock(args []string) error {
	fs := flag.NewFlagSet("unlock", flag.ExitOnError)
	keyfile := fs.String("keyfile", "", "keyfile 路径")
	server := fs.String("server", "", "服务端地址（如 https://host:8443；留空则用本地已存配置）")
	reinit := fs.Bool("reinit", false, "重启初始化配置引导：忽略已存服务端地址，重新填写验证")
	dataDir := fs.String("data", "", "本地数据目录（默认 ~/.passbook）")
	user := fs.String("username", "", "工号（首次注册设备时必填：admin 建用户时分配的工号）")
	deviceName := fs.String("device", "", "设备名（首次注册时上报；默认取主机名）")
	ca := fs.String("ca", "", "自签 CA 证书路径（PEM；内网部署 pinning 用，§8.3）")
	_ = fs.Parse(args)
	if *keyfile == "" {
		return fmt.Errorf("--keyfile 必填")
	}
	if *dataDir == "" {
		home, _ := os.UserHomeDir()
		*dataDir = filepath.Join(home, ".passbook")
	}
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return err
	}

	local, err := store.OpenLocal(filepath.Join(*dataDir, "local.db"))
	if err != nil {
		return err
	}
	defer local.Close()
	if err := local.Migrate(); err != nil {
		return err
	}
	app.local = local
	app.vault = vault.New(local)

	// §9.2 服务端地址：--server 优先；--reinit 忽略已存配置（重启引导）；否则读库自动带出
	if *server == "" {
		if !*reinit {
			if saved, err := local.GetServerURL(); err == nil && saved != "" {
				*server = saved
				fmt.Printf("使用已存服务端地址: %s（--server 覆盖修改，--reinit 重启引导）\n", saved)
			}
		}
		if *server == "" {
			return fmt.Errorf("未配置服务端地址——首次使用请执行: pbcli unlock --keyfile <xxx.key> --server <url>（填写后验证通过即写入本地配置，后续不必再填）")
		}
	}

	pass, err := readPass("输入 keyfile 口令: ")
	if err != nil {
		return err
	}
	ds, err := app.vault.ImportKeyfile(*keyfile, pass)
	if err != nil {
		return err
	}

	// 解密设备 token（若有）
	token := ""
	if ds != nil && len(ds.TokenEnc) > 0 {
		token, err = app.vault.DecryptToken(ds.TokenEnc)
		if err != nil {
			return fmt.Errorf("解密设备 token 失败: %w（请重新解锁或重新注册设备）", err)
		}
	}

	// 无 token → 设备注册（§9.1：无设备 token 时先注册设备）
	if token == "" {
		if *user == "" {
			return fmt.Errorf("未注册设备且未提供 --username——首次使用请执行: pbcli unlock --keyfile <xxx.key> --server <url> --username <工号> --device <设备名>")
		}
		token, _, err = registerDevice(app.vault, local, *server, *user, *deviceName, *ca)
		if err != nil {
			return err
		}
	}

	hc, err := newClient(*server, token, *ca)
	if err != nil {
		return err
	}
	// §9.2 初始化配置验证：token 有效时用心跳探活，确认地址可达且鉴权有效后才进入
	if token != "" {
		if err := hc.Heartbeat(""); err != nil {
			return fmt.Errorf("服务端连接验证失败（检查地址/端口或 token 有效性）: %w", err)
		}
	}
	// 验证通过 → 持久化服务端地址（§9.2）
	if err := local.SetServerURL(*server); err != nil {
		return fmt.Errorf("持久化服务端地址失败: %w", err)
	}

	app.hc = hc
	app.engine = syncer.New(app.vault, local, hc, hc.SSEBaseClient(), *server, token, nil)
	app.engine.Start()

	fmt.Println("已解锁并启动同步。")
	return nil
}

func cmdList(args []string) error {
	if err := ensureUnlocked(); err != nil {
		return err
	}
	if app.vault == nil || !app.vault.IsUnlocked() {
		return fmt.Errorf("请先 unlock")
	}
	entries, err := app.local.ListLocalEntries()
	if err != nil {
		return err
	}
	for i := range entries {
		le := &entries[i]
		if len(le.PlaintextCache) == 0 {
			fmt.Printf("  [无明文] %s (seq=%d, kv=%d)\n", le.ID, le.Seq, le.KeyVersion)
			continue
		}
		plain, err := app.vault.DecryptCache(le.PlaintextCache)
		if err != nil {
			fmt.Printf("  [解密失败] %s: %v\n", le.ID, err)
			continue
		}
		var entry model.Entry
		if err := json.Unmarshal(plain, &entry); err != nil {
			continue
		}
		dirty := ""
		if le.Dirty {
			dirty = " [未推送]"
		}
		conflict := ""
		if le.ConflictOf != "" {
			conflict = " [冲突]"
		}
		fmt.Printf("  %s  %s (type=%s)%s%s\n", entry.Title, le.ID, entry.Type, dirty, conflict)
	}
	return nil
}

func cmdPut(args []string) error {
	if err := ensureUnlocked(); err != nil {
		return err
	}
	if app.vault == nil || !app.vault.IsUnlocked() {
		return fmt.Errorf("请先 unlock")
	}
	fs := flag.NewFlagSet("put", flag.ExitOnError)
	group := fs.String("group", "", "组 ID")
	title := fs.String("title", "", "标题")
	typ := fs.String("type", "project", "类型（project/env/ip_type/acc_type/account/custom）")
	_ = fs.Parse(args)
	if *group == "" || *title == "" {
		return fmt.Errorf("--group 与 --title 必填")
	}
	if !model.ValidType(*typ) {
		return fmt.Errorf("非法 type %q", *typ)
	}

	entry := &model.Entry{
		SchemaVersion: model.SchemaVersion,
		Type:          *typ,
		Title:         *title,
		Fields:        model.Fields{},
		CustomFields:  map[string]json.RawMessage{},
	}
	if *typ != model.TypeProject {
		// 非 project 需 parent_id；harness 简化：挂在占位父节点
		pid := "root"
		entry.ParentID = &pid
	}
	entryID := newUUID()

	// 获取组当前 kv（从本地 group_state，§9.1）；rekey 后本地已存最新 kv（P2 fix）
	kv := 1
	if gs, err := app.local.GetGroupState(*group); err == nil && gs != nil && gs.KeyVersion > 0 {
		kv = gs.KeyVersion
	}

	// 明文序列化为 JSON 字节（pbcli 扮演 UI 角色，构造明文；core 只加密不解析）
	plaintext, err := entry.Marshal()
	if err != nil {
		return err
	}
	ct, err := app.vault.EncryptPlaintext(*group, entryID, plaintext, kv)
	if err != nil {
		return fmt.Errorf("加密失败（确认该组已获取 DEK）: %w", err)
	}
	if err := app.local.UpsertLocalEntry(&store.LocalEntry{
		ID: entryID, GroupID: *group, Seq: 0, KeyVersion: kv, Ciphertext: ct, Dirty: true, UpdatedAt: nowUnix(),
	}); err != nil {
		return err
	}
	// 写入明文缓存（解锁态可立即展示）；失败不阻断（条目已入库）
	cache, err := app.vault.EncryptCache(plaintext)
	if err == nil {
		if err := app.local.SetPlaintextCache(entryID, cache); err != nil {
			fmt.Printf("警告: 明文缓存写入失败: %v\n", err)
		}
	}

	fmt.Printf("已创建条目 %s（待推送）\n", entryID)
	return nil
}

func cmdSync(args []string) error {
	if err := ensureUnlocked(); err != nil {
		return err
	}
	if app.engine == nil {
		return fmt.Errorf("请先 unlock")
	}
	if err := app.engine.SyncNow(); err != nil {
		return err
	}
	fmt.Println("同步完成。")
	return nil
}

// cmdDelete 删除条目（墓碑 push，§7.2 4d / §7.3 #5）。
// 用法: pbcli delete --id <entry_id>
func cmdDelete(args []string) error {
	if err := ensureUnlocked(); err != nil {
		return err
	}
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	id := fs.String("id", "", "条目 ID（必填）")
	_ = fs.Parse(args)
	if *id == "" {
		return fmt.Errorf("--id 必填")
	}
	le, err := app.local.GetLocalEntry(*id)
	if err != nil {
		return fmt.Errorf("条目不存在: %w", err)
	}
	// 墓碑：放入回收站 + 置 deleted 待推送（对齐 core.DeleteEntry，§7.2 4d）
	if err := app.local.PutRecycleBin(*id, le.Ciphertext, nowUnix()); err != nil {
		return err
	}
	if err := app.local.UpsertLocalEntry(&store.LocalEntry{
		ID: *id, GroupID: le.GroupID, Seq: le.Seq, KeyVersion: le.KeyVersion, Ciphertext: "",
		Dirty: true, Deleted: true, UpdatedAt: nowUnix(),
	}); err != nil {
		return err
	}
	if app.engine != nil {
		_ = app.engine.SyncNow()
	}
	fmt.Printf("已删除条目 %s（墓碑待推送）\n", *id)
	return nil
}

func cmdGenPass(args []string) error {
	fs := flag.NewFlagSet("genpass", flag.ExitOnError)
	length := fs.Int("length", 16, "长度")
	_ = fs.Parse(args)
	pw, err := generatePassword(*length)
	if err != nil {
		return err
	}
	fmt.Println(pw)
	return nil
}
