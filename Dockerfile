# syntax=docker/dockerfile:1.7

# 使用 Go 1.24 官方镜像作为构建环境
FROM golang:1.24 AS builder

# 禁用 CGO
ENV CGO_ENABLED=0

# 设置工作目录
WORKDIR /app

# 先复制依赖描述文件，尽可能复用模块下载缓存
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# 复制源代码并构建应用，保留 Go 编译缓存以加速重复构建
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /app/duck2api .

# 使用 Alpine Linux 作为最终镜像
FROM alpine:3.22

RUN apk add --no-cache tzdata \
    && ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo Asia/Shanghai > /etc/timezone

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的应用和资源
COPY --from=builder /app/duck2api /app/duck2api

# 暴露端口
EXPOSE 8080

CMD ["/app/duck2api"]
