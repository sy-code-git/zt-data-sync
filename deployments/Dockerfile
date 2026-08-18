# passbook 服务端 Dockerfile（§12.1 多阶段构建，distroless 基础镜像，~20MB）
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
# 国内网络：Go module 走国内代理（proxy.golang.org 不可达）
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# 运行镜像用 alpine（gcr.io/distroless 在国内网络不可达）；Go 静态编译无需 libc
FROM alpine:3.20
COPY --from=build /out/server /server
VOLUME ["/data"]
EXPOSE 8443
# 配置全部走环境变量（PB_DATA_DIR/PB_ADDR 等），见 docker-compose.yml
ENTRYPOINT ["/server"]
