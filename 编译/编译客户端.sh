#!/usr/bin/env bash
# ============================================================
#  编译客户端产物（钥匙工具 + 密码本命令行，linux + windows amd64；
#  GUI 客户端需 wails CLI，有则一并编译）
#  产物输出到 编译/客户端产物/
# ============================================================
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

mkdir -p "编译/客户端产物"
LDFLAGS="-s -w"

echo "编译 钥匙工具（linux amd64）..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "编译/客户端产物/钥匙工具" ./cmd/keytool

echo "编译 密码本命令行（linux amd64）..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "编译/客户端产物/密码本命令行" ./cmd/pbcli

echo "编译 钥匙工具（windows amd64）..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "编译/客户端产物/钥匙工具.exe" ./cmd/keytool

echo "编译 密码本命令行（windows amd64）..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "编译/客户端产物/密码本命令行.exe" ./cmd/pbcli

# GUI 客户端（可选：需 wails CLI + Node）
if command -v wails >/dev/null 2>&1; then
    echo "编译 GUI 客户端（wails build）..."
    (cd "$ROOT/client/app" && wails build)
    if [ -f "$ROOT/client/app/build/bin/app" ]; then
        cp "$ROOT/client/app/build/bin/app" "编译/客户端产物/在线密码本"
    fi
else
    echo "跳过 GUI 客户端（未安装 wails CLI）"
fi

echo ""
echo "客户端编译完成，产物在 编译/客户端产物/"
