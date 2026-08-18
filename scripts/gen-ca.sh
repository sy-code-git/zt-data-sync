#!/usr/bin/env bash
# 自签 CA 与服务端证书生成脚本（本地/内网部署用）
# 产出（默认到 ./certs）：
#   ca.crt / ca.key        —— 自签 CA（私钥妥善保管；客户端可内置 ca.crt 做指纹固定 §8.3）
#   server.crt / server.key —— 服务端 TLS 证书（SAN 含 localhost 与常见内网地址）
# 用法：scripts/gen-ca.sh [输出目录] [IP...]
set -euo pipefail

OUT_DIR="${1:-./certs}"
shift || true
SAN_IPS=("$@")
[ ${#SAN_IPS[@]} -eq 0 ] && SAN_IPS=(127.0.0.1 10.0.1.20 192.168.1.100)

mkdir -p "$OUT_DIR"
cd "$OUT_DIR"

# 1. CA 私钥 + 证书（带 CA 扩展：basicConstraints CA:TRUE + keyUsage keyCertSign，
#   否则 Python/部分 TLS 客户端会报 "CA cert does not include key usage extension"）
openssl genrsa -out ca.key 3072 2>/dev/null
openssl req -x509 -new -key ca.key -sha256 -days 3650 \
  -subj "/CN=passbook-local-ca" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -out ca.crt

# 2. 服务端私钥 + CSR
openssl genrsa -out server.key 3072 2>/dev/null

# 3. SAN 配置文件
SAN_LIST="DNS:localhost,DNS:passbook.local"
for ip in "${SAN_IPS[@]}"; do
  SAN_LIST="$SAN_LIST,IP:$ip"
done
cat > san.cnf <<EOF
[req]
distinguished_name = dn
req_extensions = v3_req
[dn]
[v3_req]
subjectAltName = $SAN_LIST
EOF

openssl req -new -key server.key -sha256 -subj "/CN=passbook-server" \
  -config san.cnf -out server.csr

# 4. CA 签发服务端证书
cat > ext.cnf <<EOF
[v3_ca]
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = $SAN_LIST
EOF

openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days 825 -sha256 -extfile ext.cnf -extensions v3_ca -out server.crt

rm -f server.csr san.cnf ext.cnf ca.srl

# 权限收紧
chmod 600 ca.key server.key
chmod 644 ca.crt server.crt

echo "OK: 证书已生成于 $OUT_DIR"
echo "  ca.crt / ca.key       （CA，客户端内置 ca.crt 做 pinning）"
echo "  server.crt/server.key （服务端 TLS）"
