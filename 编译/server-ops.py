#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
服务端运维脚本：查看 / 重置部署密钥、下载 CA 证书（在线密码本）

用法:
  python server-ops.py view     # 拉取当前部署密钥（只读，不修改）
  python server-ops.py reset    # 重置服务端：清数据卷 + 生成新密钥 + 重启
  python server-ops.py ca       # 下载服务端 CA 证书到本脚本同目录 ca.crt

依赖: paramiko（SSH），密钥 ~/.ssh/sy_key.pem
"""
import os
import secrets
import sys
import time

import paramiko

# ---- 服务器连接信息（按需修改） ----
HOST = "43.157.23.147"
USER = "ubuntu"
KEY_FILE = os.path.expanduser("~/.ssh/sy_key.pem")
DEPLOY_DIR = "/home/ubuntu/passbook/deployments"
ENV_FILE = f"{DEPLOY_DIR}/.env"
CA_FILE = f"{DEPLOY_DIR}/certs/ca.crt"


def connect() -> paramiko.SSHClient:
    key = paramiko.RSAKey.from_private_key_file(KEY_FILE)
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(HOST, username=USER, pkey=key, timeout=15)
    return c


def run(c: paramiko.SSHClient, cmd: str, timeout: int = 90) -> None:
    _, out, err = c.exec_command(cmd, timeout=timeout)
    o = out.read().decode("utf-8", "replace")
    e = err.read().decode("utf-8", "replace")
    if o.strip():
        print(o, end="")
    if e.strip():
        print("[stderr]", e[:500])


def view(c: paramiko.SSHClient) -> None:
    print("=== 连接服务端，拉取当前部署密钥 ... ===")
    sftp = c.open_sftp()
    try:
        with sftp.open(ENV_FILE) as f:
            content = f.read().decode("utf-8", "replace")
    except FileNotFoundError:
        print("服务端未配置 .env（可能尚未部署）")
        return
    finally:
        sftp.close()

    print("=== 当前服务端部署密钥（deployments/.env）===")
    for line in content.splitlines():
        if line.startswith("PB_"):
            print("  " + line)
    print()
    print("提示：bootstrap token 是一次性的，部署成功一次即失效；需要重新部署时执行 reset。")


def reset(c: paramiko.SSHClient) -> None:
    print("=== 重置服务端：清数据卷 + 生成新密钥 + 重启 ===")
    reg = secrets.token_urlsafe(32)
    boot = secrets.token_hex(12)

    print("[1/3] 停容器 + 删数据卷（清旧数据 + 重置 token 已用标志）...")
    run(c, f"cd {DEPLOY_DIR} && sudo docker compose down -v 2>&1 | tail -3")

    print("[2/3] 写入新 .env ...")
    sftp = c.open_sftp()
    with sftp.open(ENV_FILE, "w") as f:
        f.write(f"PB_REG_SECRET={reg}\nPB_BOOTSTRAP_CODE={boot}\n")
    sftp.close()

    print("[3/3] 启动服务端 ...")
    run(c, f"cd {DEPLOY_DIR} && sudo docker compose up -d 2>&1 | tail -3")
    time.sleep(3)
    run(c, "curl -sk https://localhost:8443/healthz; echo")

    print()
    print("===== 新部署密钥（请记录，勿外泄）=====")
    print(f"  PB_REG_SECRET     = {reg}")
    print(f"  PB_BOOTSTRAP_CODE = {boot}")
    print("======================================")


def fetch_ca(c: paramiko.SSHClient) -> None:
    print("=== 下载服务端 CA 证书到脚本同目录 ca.crt ... ===")
    dest = os.path.join(os.path.dirname(os.path.abspath(__file__)), "ca.crt")
    try:
        sftp = c.open_sftp()
        try:
            sftp.get(CA_FILE, dest)
        except FileNotFoundError:
            print(f"服务端未找到 {CA_FILE}（可能尚未生成证书，先执行部署）")
            return
        finally:
            sftp.close()
    except Exception as e:
        print(f"下载失败: {e}")
        return
    print(f"已保存: {dest}")
    print("客户端解锁页「自签 CA 证书路径」填这个文件即可（证书变更后需重新下载）。")


def main() -> None:
    action = sys.argv[1] if len(sys.argv) > 1 else "view"
    if action not in ("view", "reset", "ca"):
        print("用法: python server-ops.py [view|reset|ca]")
        sys.exit(1)
    c = connect()
    try:
        if action == "reset":
            reset(c)
        elif action == "ca":
            fetch_ca(c)
        else:
            view(c)
    finally:
        c.close()


if __name__ == "__main__":
    main()
