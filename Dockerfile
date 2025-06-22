# 阶段 1: 构建 Go 应用
FROM golang:1.21 AS builder

ENV CGO_ENABLED=0
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -ldflags "-s -w" -o /app/duck2api .

# 阶段 2: 构建最终的运行镜像
FROM debian:stable-slim

# 安装 Chrome 及其运行时依赖
# 增加 curl 以便更灵活地获取密钥，并确保所有核心依赖都在一个步骤中安装
RUN apt-get update && apt-get install -y \
    wget \
    curl \                  # 添加 curl
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
    --no-install-recommends && \
    \
    # *** 修复：使用 curl 和 APT 的 add-apt-repository 替代方法 ***
    # 1. 下载 Google Chrome 的官方 GPG 密钥，并直接导入到 /etc/apt/trusted.gpg.d/
    #    虽然 apt-key 弃用，但这种直接保存到 trusted.gpg.d 的方式是允许的，
    #    且通常比 gpg --dearmor + signed-by 更简单，尤其适用于单个密钥文件。
    #    或者，如果需要更严格的 signed-by 方式，我们使用不同的方法获取密钥。
    #    我们使用更通用的 apt-key 命令，但在某些环境中它可能已被完全禁用。
    #    因此，更现代和推荐的做法是如下面的 curl + gpg --dearmor + .list 方式。

    # 更为现代和推荐的方式 (Debian 11+ / Ubuntu 20.04+):
    # 1. 创建 APT 密钥存储目录 (如果不存在)
    # 2. 使用 curl 下载 GPG 密钥，并直接通过 gpg --dearmor 处理并保存到 /etc/apt/keyrings/
    #    注意：curl -fsSL 是更安全的下载方式 (follow redirects, silent, show errors)
    mkdir -p /etc/apt/keyrings && \
    curl -fsSL https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /etc/apt/keyrings/google-chrome-archive-keyring.gpg && \
    \
    # 3. 添加 Google Chrome 仓库到 APT 源列表，并使用 signed-by 明确指定密钥文件
    echo "deb [arch=amd64 signed-by=/etc/apt/keyrings/google-chrome-archive-keyring.gpg] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list && \
    \
    # 再次更新包列表以识别新添加的仓库
    apt-get update && \
    \
    # 安装 Google Chrome 稳定版
    apt-get install -y google-chrome-stable && \
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