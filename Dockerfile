# 阶段 1: 构建 Go 应用
FROM golang:1.23 AS builder

ENV CGO_ENABLED=0
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-s -w" -o /app/duck2api .

# 阶段 2: 构建最终的运行镜像
# FIX: 将 debian:stable-slim 替换为更完整的 debian:stable
FROM debian:stable

# 安装 Chromium 及其运行时依赖
# 确保所有依赖列表中的行都以反斜杠 '\' 结尾，除了最后一行
RUN apt-get update && apt-get install -y \
    wget \
    curl \
    gnupg \
    ca-certificates \
    libnss3 \
    libatk-bridge2.0-0 \
    libcups2 \
    libgconf-2-4 \
    libgtk-3-0 \
    libnspr4 \
    libxss1 \
    libasound2 \
    libxrandr2 \
    libappindicator3-1 \
    libfontconfig1 \
    fonts-liberation \
    xdg-utils \
    chromium \
    --no-install-recommends && \
    \
    # 清理 APT 缓存以减小镜像大小
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的 Go 应用
COPY --from=builder /app/duck2api /app/duck2api

# 暴露应用端口
EXPOSE 8080

# 启动 Go 应用
CMD ["/app/duck2api"]