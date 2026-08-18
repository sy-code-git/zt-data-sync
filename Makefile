# passbook 工程命令
SHELL := /bin/bash
GO     ?= go

.PHONY: build test test-race lint tidy vet fmt run server health

## 构建
build:
	$(GO) build ./...

## 测试
test:
	$(GO) test ./...

## 竞态检测（CI 必跑）
test-race:
	$(GO) test -race ./...

## 静态检查（golangci-lint，含 govet/staticcheck/errcheck/gosec/gocritic）
lint:
	golangci-lint run ./...

vet:
	$(GO) vet ./...

tidy:
	$(GO) mod tidy

fmt:
	gofmt -l -w .

## 本地运行服务端（默认 8443，需先有证书或使用 -addr 临时调试）
run:
	$(GO) run ./cmd/server

server:
	$(GO) run ./cmd/server
