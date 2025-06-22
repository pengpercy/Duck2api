FROM golang:1.21 AS builder

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

FROM debian:stable-slim

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
    libappindicator1 \
    libfontconfig1 \
    fonts-liberation \
    xdg-utils \
    --no-install-recommends && \
    wget -q -O - https://dl-ssl.google.com/linux/linux_signing_key.pub | apt-key add - && \
    echo "deb http://dl.google.com/linux/chrome/deb/ stable main" > /etc/apt/sources.list.d/google-chrome.list && \
    apt-get update && \
    apt-get install -y google-chrome-stable && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的 Go 应用
COPY --from=builder /app/duck2api /app/duck2api


# 暴露应用端口
EXPOSE 8080

CMD ["/app/duck2api"]