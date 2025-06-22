# 阶段 1: 构建 Go 应用
# 使用 Go 1.23 官方镜像作为构建环境
FROM golang:1.23 AS builder

# 禁用 CGO (如果你的 Go 应用不依赖 C 库，这有助于构建静态链接的二进制文件)
ENV CGO_ENABLED=0

# 设置工作目录
WORKDIR /app

# 复制 go.mod 和 go.sum 并下载依赖，利用 Docker 缓存层
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码并构建 Go 应用
COPY . .
# -ldflags "-s -w" 用于减小二进制文件大小
# -o /app/duck2api 指定输出路径和名称
RUN go build -ldflags "-s -w" -o /app/duck2api .

# 阶段 2: 构建最终的运行镜像
# 基于 Debian Buster Slim (一个更小但包含足够运行 Chrome 的基础镜像)
FROM debian:stable-slim

# 安装 Chrome 及其运行时依赖
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
    --no-install-recommends && \
    \
    # *** 修复：使用更现代的 GPG 密钥管理方式 ***
    # 1. 下载 Google Chrome 的官方 GPG 密钥
    # 2. 通过 gpg --dearmor 将其转换为二进制格式
    # 3. 将二进制密钥文件放置在 /usr/share/keyrings/ 目录下
    wget -q -O - https://dl.google.com/linux/linux_signing_key.pub | gpg --dearmor -o /usr/share/keyrings/google-chrome-archive-keyring.gpg && \
    \
    # 4. 添加 Google Chrome 仓库到 APT 源列表，并使用 signed-by 明确指定密钥文件
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/google-chrome-archive-keyring.gpg] http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list && \
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