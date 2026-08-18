#!/usr/bin/env bash
# ============================================================
#  编译服务端产物（linux + windows amd64）
#  产物输出到 编译/服务端产物/
# ============================================================
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

mkdir -p "编译/服务端产物"
LDFLAGS="-s -w"

echo "编译 密码本服务端（linux amd64）..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "编译/服务端产物/密码本服务端" ./cmd/server

echo "编译 密码本服务端（windows amd64）..."
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "$LDFLAGS" -o "编译/服务端产物/密码本服务端.exe" ./cmd/server

echo ""
echo "服务端编译完成，产物在 编译/服务端产物/"
