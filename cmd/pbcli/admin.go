package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"passbook/client/core/api"
	"passbook/client/core/store"
	"passbook/client/core/syncer"
	"passbook/client/core/vault"
	"passbook/internal/proto"
)

// newClient 构造 HTTP 客户端：caPath 非空时信任自签 CA（§8.3 pinning），否则默认验证。
func newClient(serverURL, token, caPath string) (*api.HTTPClient, error) {
	if caPath != "" {
		return api.NewHTTPClientWithCA(serverURL, token, caPath)
	}
	return api.NewHTTPClient(serverURL, token), nil
}

// ensureUnlocked 供 list/put/sync 等单进程命令在未解锁时，从环境变量自动解锁
// （PB_KEYFILE/PB_SERVER/PB_USERNAME/PB_CA/PB_DATA/PB_PASSWORD，§9.1）。
// 使 pbcli 每个命令可独立运行（进程隔离下无需跨进程共享内存态）。
func ensureUnlocked() error {
	if app.vault != nil && app.vault.IsUnlocked() {
		return nil
	}
	keyfile := os.Getenv("PB_KEYFILE")
	server := os.Getenv("PB_SERVER")
	if keyfile == "" || server == "" {
		return fmt.Errorf("未解锁：请设置 PB_KEYFILE 与 PB_SERVER 环境变量，或先执行 unlock")
	}
	user := os.Getenv("PB_USERNAME")
	deviceName := os.Getenv("PB_DEVICE")
	ca := os.Getenv("PB_CA")
	dataDir := os.Getenv("PB_DATA")
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".passbook")
	}

	if err := os.MkdirAll(dataDir, 0o700); err != nil { // #nosec G703 -- 本地数据目录，路径来自本机用户环境变量，非不可信输入
		return err
	}
	local, err := store.OpenLocal(filepath.Join(dataDir, "local.db"))
	if err != nil {
		return err
	}
	// 注意：local 不 Close，进程结束自然回收；保持与 unlock 一致的生命周期
	if err := local.Migrate(); err != nil {
		return err
	}
	app.local = local
	app.vault = vault.New(local)

	pass, err := readPass("输入 keyfile 口令: ")
	if err != nil {
		return err
	}
	ds, err := app.vault.ImportKeyfile(keyfile, pass)
	if err != nil {
		return err
	}
	token := ""
	if ds != nil && len(ds.TokenEnc) > 0 {
		token, err = app.vault.DecryptToken(ds.TokenEnc)
		if err != nil {
			return fmt.Errorf("解密设备 token 失败: %w", err)
		}
	}
	if token == "" {
		if user == "" {
			return fmt.Errorf("未注册设备且未设置 PB_USERNAME——请先执行 unlock --username <工号> 注册")
		}
		token, _, err = registerDevice(app.vault, local, server, user, deviceName, ca)
		if err != nil {
			return err
		}
	}

	hc, err := newClient(server, token, ca)
	if err != nil {
		return err
	}
	if token != "" {
		if err := hc.Heartbeat(""); err != nil {
			return fmt.Errorf("服务端连接验证失败: %w", err)
		}
	}
	if err := local.SetServerURL(server); err != nil {
		return err
	}
	app.hc = hc
	app.engine = syncer.New(app.vault, local, hc, hc.SSEBaseClient(), server, token, nil)
	app.engine.Start()
	return nil
}

// registerDevice 设备注册（§9.1/§6.3）：取 challenge → SM2 签名 → 注册 → token 落盘。
// 返回新 token 与更新后的 device_state。username 为工号（登录标识）。
func registerDevice(v *vault.Vault, local store.LocalStore, serverURL, username, deviceName, caPath string) (string, *store.DeviceState, error) {
	if deviceName == "" {
		h, _ := os.Hostname()
		deviceName = h
	}
	hostname, _ := os.Hostname()
	hc, err := newClient(serverURL, "", caPath)
	if err != nil {
		return "", nil, err
	}

	// 1. 取一次性挑战
	chResp, err := hc.DeviceChallenge(username)
	if err != nil {
		return "", nil, fmt.Errorf("获取设备挑战失败: %w", err)
	}
	// 2. 签名 challenge（msg = challenge 字符串原文，§6.3 验签同此）
	sig, err := v.SignChallenge(chResp.Challenge)
	if err != nil {
		return "", nil, err
	}
	// 3. 注册设备
	regResp, err := hc.DeviceRegister(&proto.DeviceRegisterRequest{
		Username:   username,
		DeviceName: deviceName,
		Hostname:   hostname,
		Challenge:  chResp.Challenge,
		Signature:  base64.StdEncoding.EncodeToString(sig),
	})
	if err != nil {
		return "", nil, fmt.Errorf("设备注册失败: %w", err)
	}
	// 4. token 加密落盘 device_state
	enc, err := v.EncryptToken(regResp.Token)
	if err != nil {
		return "", nil, err
	}
	ds := &store.DeviceState{DeviceID: regResp.DeviceID, TokenEnc: enc, ExpiresAt: time.Now().Unix() + regResp.ExpiresIn}
	if err := local.SetDeviceState(ds); err != nil {
		return "", nil, err
	}
	fmt.Printf("设备注册成功: device_id=%s\n", regResp.DeviceID)
	return regResp.Token, ds, nil
}

// cmdBootstrap 首启引导：用 bootstrap token + keyfile 公钥建立首个 admin（§6.3/§9.3）。
// 用法: pbcli bootstrap --server <url> --token <bootstrap> --keyfile <xxx.key> [--name <管理员名>] [--device <设备名>] [--data <dir>]
func cmdBootstrap(args []string) error {
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	server := fs.String("server", "", "服务端地址（必填）")
	btoken := fs.String("token", "", "一次性 bootstrap token（必填）")
	keyfile := fs.String("keyfile", "", "admin keyfile 路径（必填，已由 keytool genuser 生成）")
	username := fs.String("username", "", "管理员工号（唯一、不可改、登录标识，必填）")
	name := fs.String("name", "", "管理员显示名（必填）")
	deviceName := fs.String("device", "", "设备名（可选，默认主机名）")
	dataDir := fs.String("data", "", "本地数据目录（默认 ~/.passbook）")
	ca := fs.String("ca", "", "自签 CA 证书路径（PEM；内网部署 pinning 用，§8.3）")
	_ = fs.Parse(args)
	if *server == "" || *btoken == "" || *keyfile == "" || *username == "" || *name == "" {
		return fmt.Errorf("--server/--token/--keyfile/--username/--name 必填")
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

	pass, err := readPass("输入 admin keyfile 口令: ")
	if err != nil {
		return err
	}
	if _, err := app.vault.ImportKeyfile(*keyfile, pass); err != nil {
		return err
	}
	pubB64, err := app.vault.PublicKeyB64()
	if err != nil {
		return err
	}

	hc, err := newClient(*server, "", *ca)
	if err != nil {
		return err
	}
	resp, err := hc.Bootstrap(&proto.BootstrapRequest{
		BootstrapToken: *btoken,
		Username:       *username,
		Name:           *name,
		DeviceName:     *deviceName,
		SM2PublicKey:   pubB64,
	})
	if err != nil {
		return fmt.Errorf("bootstrap 失败: %w", err)
	}

	// token 加密落盘 device_state
	enc, err := app.vault.EncryptToken(resp.Token)
	if err != nil {
		return err
	}
	if err := local.SetDeviceState(&store.DeviceState{DeviceID: resp.DeviceID, TokenEnc: enc, ExpiresAt: time.Now().Unix() + resp.ExpiresIn}); err != nil {
		return err
	}
	if err := local.SetServerURL(*server); err != nil {
		return err
	}

	// 输出结果（user_id 供后续 admin 操作/成员注册引用）
	out, _ := json.Marshal(map[string]string{
		"user_id":   resp.UserID,
		"device_id": resp.DeviceID,
		"role":      resp.Role,
	})
	fmt.Println(string(out))
	return nil
}

// cmdAdmin admin 操作（§9.3/§6.3）：公共上下文经环境变量传递
// （PB_SERVER / PB_CA / PB_KEYFILE / PB_DATA），子命令 flag 只解析自身参数。
// 每个子命令内部先解锁 admin（读 device_state token）再执行对应 admin API。
// 支持子命令: create-user / create-group / add-member / revoke / rekey /
// archive / unarchive / members / devices / groups / audit
func cmdAdmin(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: PB_SERVER=<url> PB_KEYFILE=<admin.key> pbcli admin <create-user|create-group|add-member|revoke|rekey|archive|unarchive|members|devices|groups|audit|create-invite|invites|requests|approve|reject> ...")
	}
	sub := args[0]
	rest := args[1:]

	server := os.Getenv("PB_SERVER")
	keyfile := os.Getenv("PB_KEYFILE")
	ca := os.Getenv("PB_CA")
	dataDir := os.Getenv("PB_DATA")
	if keyfile == "" || server == "" {
		return fmt.Errorf("PB_SERVER 与 PB_KEYFILE 环境变量必填")
	}
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".passbook")
	}

	// 解锁 admin（读 device_state token）
	hc, err := unlockForAdmin(keyfile, server, ca, dataDir)
	if err != nil {
		return err
	}

	switch sub {
	case "create-user":
		return adminCreateUser(hc, rest)
	case "create-group":
		return adminCreateGroup(hc, rest)
	case "add-member":
		return adminAddMember(hc, rest)
	case "revoke":
		return adminRevoke(hc, rest)
	case "rekey":
		return adminRekey(hc, rest)
	case "archive":
		return adminArchive(hc, rest)
	case "unarchive":
		return adminUnarchive(hc, rest)
	case "members":
		return adminMembers(hc, rest)
	case "devices":
		return adminDevices(hc, rest)
	case "groups":
		return adminGroups(hc, rest)
	case "audit":
		return adminAudit(hc, rest)
	case "create-invite":
		return adminCreateInvite(hc, rest)
	case "invites":
		return adminInvites(hc, rest)
	case "requests":
		return adminRequests(hc, rest)
	case "approve":
		return adminApprove(hc, rest)
	case "reject":
		return adminReject(hc, rest)
	default:
		return fmt.Errorf("未知 admin 子命令 %q", sub)
	}
}

// unlockForAdmin 解锁 admin：导入 keyfile → 解密 device_state token → 构造 hc。
func unlockForAdmin(keyfile, server, caPath, dataDir string) (*api.HTTPClient, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil { // #nosec G703 -- 本地数据目录，路径来自本机用户环境变量，非不可信输入
		return nil, err
	}
	local, err := store.OpenLocal(filepath.Join(dataDir, "local.db"))
	if err != nil {
		return nil, err
	}
	defer local.Close()
	if err := local.Migrate(); err != nil {
		return nil, err
	}
	v := vault.New(local)

	pass, err := readPass("输入 admin keyfile 口令: ")
	if err != nil {
		return nil, err
	}
	ds, err := v.ImportKeyfile(keyfile, pass)
	if err != nil {
		return nil, err
	}
	if ds == nil || len(ds.TokenEnc) == 0 {
		return nil, fmt.Errorf("admin 设备尚未注册（请先 bootstrap）")
	}
	token, err := v.DecryptToken(ds.TokenEnc)
	if err != nil {
		return nil, fmt.Errorf("解密 admin token 失败: %w", err)
	}
	hc, err := newClient(server, token, caPath)
	if err != nil {
		return nil, err
	}
	// 验证连接（§9.2：admin token 可能已过期/被吊销）
	if err := hc.Heartbeat(""); err != nil {
		return nil, fmt.Errorf("服务端连接验证失败（admin token 可能已过期）: %w", err)
	}
	return hc, nil
}

func adminCreateUser(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("create-user", flag.ExitOnError)
	pub := fs.String("pub", "", "pub.json 路径（keytool genuser 生成，必填）")
	username := fs.String("username", "", "工号（唯一、不可改、登录标识，必填）")
	_ = fs.Parse(args)
	if *pub == "" || *username == "" {
		return fmt.Errorf("--pub 与 --username 必填")
	}
	data, err := os.ReadFile(*pub)
	if err != nil {
		return err
	}
	var p struct {
		Name         string `json:"name"`
		SM2PublicKey string `json:"sm2_public_key"`
		Attestation  string `json:"attestation"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("pub.json 解析失败: %w", err)
	}
	resp, err := hc.AdminCreateUser(&proto.CreateUserRequest{
		Username: *username, Name: p.Name, SM2PublicKey: p.SM2PublicKey, Attestation: p.Attestation,
	})
	if err != nil {
		return err
	}
	fmt.Printf("username=%s user_id=%s\n", *username, resp.UserID)
	return nil
}

func adminCreateGroup(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("create-group", flag.ExitOnError)
	name := fs.String("name", "", "组名（必填）")
	_ = fs.Parse(args)
	if *name == "" {
		return fmt.Errorf("--name 必填")
	}
	resp, err := hc.AdminCreateGroup(*name)
	if err != nil {
		return err
	}
	fmt.Printf("group_id=%s key_version=%d\n", resp.GroupID, resp.KeyVersion)
	return nil
}

func adminAddMember(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("add-member", flag.ExitOnError)
	gid := fs.String("group", "", "组 ID（必填）")
	uid := fs.String("user", "", "用户 ID（必填）")
	_ = fs.Parse(args)
	if *gid == "" || *uid == "" {
		return fmt.Errorf("--group 与 --user 必填")
	}
	if err := hc.AdminAddMember(*gid, *uid); err != nil {
		return err
	}
	fmt.Println("已加入成员。")
	return nil
}

func adminRevoke(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	uid := fs.String("user", "", "用户 ID（必填）")
	confirm := fs.String("confirm", "", "成员名（二次确认，必填）")
	_ = fs.Parse(args)
	if *uid == "" || *confirm == "" {
		return fmt.Errorf("--user 与 --confirm 必填")
	}
	emptyGroups, err := hc.AdminRevoke(*uid, *confirm)
	if err != nil {
		return err
	}
	if len(emptyGroups) > 0 {
		fmt.Printf("已吊销。⚠ 以下组已无成员: %s（组密钥重加密将无人执行）\n", strings.Join(emptyGroups, ", "))
	} else {
		fmt.Println("已吊销。")
	}
	return nil
}

func adminRekey(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("rekey", flag.ExitOnError)
	gid := fs.String("group", "", "组 ID（必填）")
	_ = fs.Parse(args)
	if *gid == "" {
		return fmt.Errorf("--group 必填")
	}
	resp, err := hc.AdminRekey(*gid)
	if err != nil {
		return err
	}
	fmt.Printf("pending_rekey=%v key_version=%d\n", resp.PendingRekey, resp.KeyVersion)
	return nil
}

func adminArchive(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("archive", flag.ExitOnError)
	gid := fs.String("group", "", "组 ID（必填）")
	confirm := fs.String("confirm", "", "组名（二次确认，必填）")
	_ = fs.Parse(args)
	if *gid == "" || *confirm == "" {
		return fmt.Errorf("--group 与 --confirm 必填")
	}
	resp, err := hc.AdminArchive(*gid, *confirm)
	if err != nil {
		return err
	}
	fmt.Printf("archived=%v\n", resp.Archived)
	return nil
}

func adminUnarchive(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("unarchive", flag.ExitOnError)
	gid := fs.String("group", "", "组 ID（必填）")
	_ = fs.Parse(args)
	if *gid == "" {
		return fmt.Errorf("--group 必填")
	}
	if err := hc.AdminUnarchive(*gid); err != nil {
		return err
	}
	fmt.Println("已重启组。")
	return nil
}

func adminMembers(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("members", flag.ExitOnError)
	gid := fs.String("group", "", "组 ID（必填）")
	_ = fs.Parse(args)
	if *gid == "" {
		return fmt.Errorf("--group 必填")
	}
	members, err := hc.AdminListMembers(*gid)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(members, "", "  ")
	fmt.Println(string(out))
	return nil
}

func adminDevices(hc *api.HTTPClient, args []string) error {
	devs, err := hc.AdminListDevices()
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(devs, "", "  ")
	fmt.Println(string(out))
	return nil
}

func adminGroups(hc *api.HTTPClient, args []string) error {
	groups, err := hc.AdminListGroups()
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(groups, "", "  ")
	fmt.Println(string(out))
	return nil
}

func adminAudit(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("audit", flag.ExitOnError)
	action := fs.String("action", "", "按 action 过滤（可选）")
	_ = fs.Parse(args)
	query := ""
	if *action != "" {
		query = "action=" + *action
	}
	events, err := hc.AdminListAudit(query)
	if err != nil {
		return err
	}
	out, _ := json.MarshalIndent(events, "", "  ")
	fmt.Println(string(out))
	return nil
}

// adminCreateInvite 生成邀请码（绑定工号）。
func adminCreateInvite(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("create-invite", flag.ExitOnError)
	username := fs.String("username", "", "绑定工号（必填）")
	autoApprove := fs.Bool("auto-approve", false, "免审核：提交申请即自动开户")
	ttl := fs.Int("ttl", 0, "有效期天数（0=默认3天）")
	_ = fs.Parse(args)
	if *username == "" {
		return fmt.Errorf("--username 必填")
	}
	resp, err := hc.AdminCreateInvite(&proto.CreateInviteRequest{Username: *username, AutoApprove: *autoApprove, TTLDays: *ttl})
	if err != nil {
		return err
	}
	inv := resp.Invite
	flag := ""
	if inv.AutoApprove {
		flag = "（免审核）"
	}
	fmt.Printf("邀请码: %s  工号: %s%s  有效期至 %s\n", inv.Code, inv.Username, flag, time.Unix(inv.ExpiresAt, 0).Format("2006-01-02"))
	return nil
}

// adminInvites 邀请码列表。
func adminInvites(hc *api.HTTPClient, _ []string) error {
	resp, err := hc.AdminListInvites()
	if err != nil {
		return err
	}
	for _, inv := range resp.Invites {
		status := "未使用"
		if inv.Status == "used" {
			status = "已使用"
		}
		extra := ""
		if inv.AutoApprove {
			extra = " 免审核"
		}
		fmt.Printf("%s  %s%s  %s  过期 %s\n", inv.Code, inv.Username, extra, status, time.Unix(inv.ExpiresAt, 0).Format("2006-01-02"))
	}
	return nil
}

// adminRequests 注册申请列表（--status 过滤，默认 pending）。
func adminRequests(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("requests", flag.ExitOnError)
	status := fs.String("status", "pending", "pending|approved|rejected|空=全部")
	_ = fs.Parse(args)
	if *status == "all" {
		*status = ""
	}
	resp, err := hc.AdminListRegisterRequests(*status)
	if err != nil {
		return err
	}
	for i := range resp.Requests {
		rq := &resp.Requests[i]
		fmt.Printf("%s  工号 %s  设备 %s  IP %s  状态 %s  申请 %s\n",
			rq.ID, rq.Username, rq.DeviceName, rq.IP, rq.Status, time.Unix(rq.CreatedAt, 0).Format("01-02 15:04"))
	}
	return nil
}

// adminApprove 审核通过（=开户）。
func adminApprove(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("approve", flag.ExitOnError)
	id := fs.String("id", "", "申请 id（必填）")
	name := fs.String("name", "", "显示名（默认=工号）")
	_ = fs.Parse(args)
	if *id == "" {
		return fmt.Errorf("--id 必填")
	}
	resp, err := hc.AdminApproveRegisterRequest(*id, *name)
	if err != nil {
		return err
	}
	fmt.Printf("已开户（申请 %s → %s）\n", *id, resp.Status)
	return nil
}

// adminReject 拒绝申请。
func adminReject(hc *api.HTTPClient, args []string) error {
	fs := flag.NewFlagSet("reject", flag.ExitOnError)
	id := fs.String("id", "", "申请 id（必填）")
	_ = fs.Parse(args)
	if *id == "" {
		return fmt.Errorf("--id 必填")
	}
	resp, err := hc.AdminRejectRegisterRequest(*id)
	if err != nil {
		return err
	}
	fmt.Printf("已拒绝（申请 %s → %s）\n", *id, resp.Status)
	return nil
}
