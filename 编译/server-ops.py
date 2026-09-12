#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
服务端运维脚本：查看 / 重置部署密钥、下载 CA 证书（在线密码本）

连接信息全部来自环境变量，脚本内不写任何主机、账号、路径默认值
（避免真实部署信息随代码入库；请在本地 shell 或**未入库**的 .env 中设置）：

  PB_OPS_HOST        部署机地址（IP 或域名）                              必填
  PB_OPS_USER        SSH 登录用户                                         必填
  PB_OPS_KEY         SSH 私钥路径（支持 ~，如 ~/.ssh/xxx.pem）            必填
  PB_OPS_DEPLOY_DIR  服务端部署目录（内含 docker-compose.yml 与 certs/）  必填
  PB_OPS_CA_OUT      ca 子命令的本地输出路径（可选，默认写到本脚本同目录）

用法:
  python server-ops.py view             # 查看当前部署密钥（默认掩码显示）
  python server-ops.py view --show      # 明文显示（确认周围环境安全再用）
  python server-ops.py reset --yes      # 重置服务端：清数据卷 + 生成新密钥 + 重启
  python server-ops.py reset --yes --show   # 同上，并把新密钥明文打印出来
  python server-ops.py ca               # 下载服务端 CA 证书

依赖: paramiko（SSH）
安全约定:
  - reset 不带 --yes 时只做提示并退出（fail-closed），避免误触清空生产数据
  - 密钥默认掩码输出；明文需显式 --show
"""
import os
import re
import secrets
import sys
import time

import paramiko

REQUIRED_ENV = ("PB_OPS_HOST", "PB_OPS_USER", "PB_OPS_KEY", "PB_OPS_DEPLOY_DIR")

ENV_HELP = """缺少环境变量 {name}。本脚本不提供任何默认值，请先设置：

  PB_OPS_HOST        部署机地址（IP 或域名）
  PB_OPS_USER        SSH 登录用户
  PB_OPS_KEY         SSH 私钥路径（支持 ~）
  PB_OPS_DEPLOY_DIR  服务端部署目录（内含 docker-compose.yml 与 certs/）
  PB_OPS_CA_OUT      ca 输出路径（可选）

Windows（当前会话临时生效）:
  set PB_OPS_HOST=<部署机地址>
  set PB_OPS_USER=<登录用户>
  set PB_OPS_KEY=%USERPROFILE%\\.ssh\\<私钥文件名>
  set PB_OPS_DEPLOY_DIR=<服务端部署目录>

Linux/macOS:
  export PB_OPS_HOST=<部署机地址> PB_OPS_USER=<登录用户> \\
         PB_OPS_KEY=~/.ssh/<私钥文件名> PB_OPS_DEPLOY_DIR=<服务端部署目录>
"""


def req_env(name: str) -> str:
    """读取必填环境变量；缺失则给出设置指引后退出。"""
    value = os.environ.get(name, "").strip()
    if not value:
        sys.exit(ENV_HELP.format(name=name))
    return value


def mask(value: str) -> str:
    """密钥掩码：保留前 4 后 4，中间以 * 填充；过短则整体掩码。"""
    if len(value) <= 8:
        return "*" * len(value)
    return f"{value[:4]}{'*' * (len(value) - 8)}{value[-4:]}"


def normalize_path(path: str) -> str:
    """Windows 上兼容 Git Bash / MSYS 风格路径：/c/Users/x -> C:\\Users\\x。

    Git Bash 会在赋值时把 `~` 展开成 `/c/...`，Windows 版 python 打不开这种路径；
    统一归一到本机形式，使同一份脚本在 cmd（%USERPROFILE% 风格）与 Git Bash 下都能用。
    """
    if os.name != "nt":
        return path
    matched = re.match(r"^/([A-Za-z])/(.+)$", path)
    if matched:
        return f"{matched.group(1).upper()}:\\{matched.group(2).replace('/', os.sep)}"
    return path


class ServerOps:
    """一次运维会话：连接信息全部由环境变量注入。"""

    def __init__(self) -> None:
        self.host = req_env("PB_OPS_HOST")
        self.user = req_env("PB_OPS_USER")
        self.key_file = normalize_path(os.path.expanduser(req_env("PB_OPS_KEY")))
        self.deploy_dir = req_env("PB_OPS_DEPLOY_DIR").rstrip("/")
        # 可选：ca 输出路径（默认写到脚本同目录，非机器相关的硬编码路径）
        self.ca_out = os.environ.get("PB_OPS_CA_OUT", "").strip() or os.path.join(
            os.path.dirname(os.path.abspath(__file__)), "ca.crt"
        )

    # ---- 基础连接 ----

    def connect(self) -> paramiko.SSHClient:
        if not os.path.isfile(self.key_file):
            sys.exit(f"私钥不存在: {self.key_file}（请检查 PB_OPS_KEY）")
        key = paramiko.RSAKey.from_private_key_file(self.key_file)
        client = paramiko.SSHClient()
        client.set_missing_host_key_policy(paramiko.AutoAddPolicy())
        client.connect(self.host, username=self.user, pkey=key, timeout=15)
        return client

    def run(self, client: paramiko.SSHClient, cmd: str, timeout: int = 90) -> None:
        _, out, err = client.exec_command(cmd, timeout=timeout)
        stdout = out.read().decode("utf-8", "replace")
        stderr = err.read().decode("utf-8", "replace")
        if stdout.strip():
            print(stdout, end="")
        if stderr.strip():
            print("[stderr]", stderr[:500])

    # ---- 子命令 ----

    def view(self, client: paramiko.SSHClient, show: bool) -> None:
        print(f"=== 读取服务端部署密钥（只读）: {self.user}@{self.host} -> {self.deploy_dir}/.env ===")
        env_file = f"{self.deploy_dir}/.env"
        sftp = client.open_sftp()
        try:
            with sftp.open(env_file) as handle:
                content = handle.read().decode("utf-8", "replace")
        except FileNotFoundError:
            print("服务端未找到 .env（可能尚未部署）")
            return
        finally:
            sftp.close()

        for line in content.splitlines():
            if not line.startswith("PB_"):
                continue
            key, _, value = line.partition("=")
            print(f"  {key}={value if show else mask(value)}")
        if not show:
            print("\n（默认掩码显示；需要在别处录入时加 --show 查看明文，注意周围环境）")
        print("提示：bootstrap token 一次性有效，部署成功即失效；需要重新部署请用 reset --yes。")

    def reset(self, client: paramiko.SSHClient, show: bool, yes: bool) -> None:
        target = f"{self.user}@{self.host}  {self.deploy_dir}"
        if not yes:
            # fail-closed：不带 --yes 只提示，不做任何破坏性动作
            print("reset 将执行以下动作（**破坏性**）：")
            print(f"  1. 在 {target} 执行 `docker compose down -v` —— 删除服务端数据卷")
            print("     （用户、条目变更、设备 token、审计记录都会清空；客户端本地副本不受影响）")
            print("  2. 生成新的 PB_REG_SECRET / PB_BOOTSTRAP_CODE 并写入 .env")
            print("  3. `docker compose up -d` 重启并探活")
            print("\n确认无误后重跑：python server-ops.py reset --yes")
            sys.exit(1)

        reg_secret = secrets.token_urlsafe(32)
        bootstrap = secrets.token_hex(12)

        print(f"=== 重置服务端: {target} ===")
        print("[1/3] 停容器 + 删数据卷（清旧数据 + 重置 token 已用标志）...")
        self.run(client, f"cd {self.deploy_dir} && sudo docker compose down -v 2>&1 | tail -3")

        print("[2/3] 写入新 .env ...")
        sftp = client.open_sftp()
        with sftp.open(f"{self.deploy_dir}/.env", "w") as handle:
            handle.write(f"PB_REG_SECRET={reg_secret}\nPB_BOOTSTRAP_CODE={bootstrap}\n")
        sftp.close()

        print("[3/3] 启动服务端 ...")
        self.run(client, f"cd {self.deploy_dir} && sudo docker compose up -d 2>&1 | tail -3")
        time.sleep(3)
        self.run(client, "curl -sk https://localhost:8443/healthz; echo")

        print()
        if show:
            print("===== 新部署密钥（请立即记录到安全位置，勿提交入库）=====")
            print(f"  PB_REG_SECRET     = {reg_secret}")
            print(f"  PB_BOOTSTRAP_CODE = {bootstrap}")
            print("======================================================")
        else:
            print("新密钥已写入服务端 .env（此处掩码显示）：")
            print(f"  PB_REG_SECRET     = {mask(reg_secret)}")
            print(f"  PB_BOOTSTRAP_CODE = {mask(bootstrap)}")
            print("\n需要录入明文时执行：python server-ops.py view --show")

    def fetch_ca(self, client: paramiko.SSHClient) -> None:
        remote = f"{self.deploy_dir}/certs/ca.crt"
        print(f"=== 下载服务端 CA 证书: {remote} -> {self.ca_out} ===")
        sftp = client.open_sftp()
        try:
            sftp.get(remote, self.ca_out)
        except FileNotFoundError:
            print(f"服务端未找到 {remote}（可能尚未生成证书，先执行部署）")
            return
        finally:
            sftp.close()
        print(f"已保存: {self.ca_out}")
        print("客户端解锁页「自签 CA 证书路径」填这个文件（证书变更后需重新下载）。")


def main() -> None:
    args = sys.argv[1:]
    action = args[0] if args and not args[0].startswith("-") else "view"
    show = "--show" in args
    yes = "--yes" in args
    if action not in ("view", "reset", "ca"):
        print("用法: python server-ops.py [view|reset|ca] [--show] [--yes]")
        sys.exit(1)

    ops = ServerOps()
    client = ops.connect()
    try:
        if action == "reset":
            ops.reset(client, show=show, yes=yes)
        elif action == "ca":
            ops.fetch_ca(client)
        else:
            ops.view(client, show=show)
    finally:
        client.close()


if __name__ == "__main__":
    main()
