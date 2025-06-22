# 阶段 1: 构建 Go 应用
FROM golang:1.23 AS builder

ENV CGO_ENABLED=0
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-s -w" -o /app/duck2api .

# 阶段 2: 构建最终的运行镜像
FROM debian:stable-slim

# 安装 Chrome 及其运行时依赖
# 确保所有依赖列表中的行都以反斜杠 '\' 结尾，除了最后一行
RUN apt-get update && apt-get install -y \
    wget \
    curl \
    gnupg \
    # FIX: Add ca-certificates to ensure curl can perform HTTPS requests
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
    --no-install-recommends && \
    \
    # *** 修复：使用 curl 和 gpg --dearmor 来安全添加 Google Chrome GPG 密钥 ***
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /etc/apt/keyrings/google-chrome-archive-keyring.gpg && \
    \
    # 添加 Google Chrome 仓库到 APT 源列表，并使用 signed-by 明确指定密钥文件
    echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome-archive-keyring.gpg] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list

# 再次更新包列表以识别新添加的仓库 (独立 RUN 命令，以利用 Docker 缓存)
RUN apt-get update

# 安装 Google Chrome 稳定版 (独立 RUN 命令)
RUN apt-get install -y google-chrome-stable

# 清理 APT 缓存以减小镜像大小 (独立 RUN 命令)
RUN apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的 Go 应用
COPY --from=builder /app/duck2api /app/duck2api

# 暴露应用端口
EXPOSE 8080

# 启动 Go 应用
CMD ["/app/duck2api"]