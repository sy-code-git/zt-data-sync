# passbook 服务端 Dockerfile（§12.1 多阶段构建，distroless 基础镜像，~20MB）
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
# 国内网络：Go module 走国内代理（proxy.golang.org 不可达）
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .
# 构建信息注入（/healthz 响应头 + /version + 启动日志）：默认 dev/unknown，
# 部署时用 --build-arg 传入，例如：
#   docker compose build --build-arg VERSION=v0.5 --build-arg COMMIT=$(git rev-parse --short HEAD) \
#                        --build-arg BUILD_TIME=$(date -u +%FT%TZ)
# 注意：构建上下文来自 git archive，无 .git 目录，故此处无法自动取 VCS 信息，必须显式传入
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w \
        -X passbook/internal/buildinfo.Version=${VERSION} \
        -X passbook/internal/buildinfo.Commit=${COMMIT} \
        -X passbook/internal/buildinfo.BuildTime=${BUILD_TIME}" \
      -o /out/server ./cmd/server

# 运行镜像用 alpine（gcr.io/distroless 在国内网络不可达）；Go 静态编译无需 libc
FROM alpine:3.20
COPY --from=build /out/server /server
VOLUME ["/data"]
EXPOSE 8443
# 配置全部走环境变量（PB_DATA_DIR/PB_ADDR 等），见 docker-compose.yml
ENTRYPOINT ["/server"]
