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
# 注意：我们将安装和密钥管理拆分成更小的步骤，确保每一步的成功
RUN apt-get update && apt-get install -y \
    wget \
    gnupg \
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
    --no-install-recommends

RUN mkdir -p /etc/apt/keyrings && \
    wget -q -O /tmp/google_chrome_signing_key.pub https://dl.google.com/linux/linux_signing_key.pub && \
    gpg --dearmor -o /etc/apt/keyrings/google-chrome-archive-keyring.gpg /tmp/google_chrome_signing_key.pub && \
    rm /tmp/google_chrome_signing_key.pub && \
    echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome-archive-keyring.gpg] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list

# 再次更新包列表以识别新添加的仓库
RUN apt-get update

# 安装 Google Chrome 稳定版
RUN apt-get install -y google-chrome-stable

# 清理 APT 缓存以减小镜像大小
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