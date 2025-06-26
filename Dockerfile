# 使用 Go 1.23 官方镜像作为构建环境
FROM golang:1.23 AS builder

# 禁用 CGO，生成静态链接的二进制文件
ENV CGO_ENABLED=0

# 设置工作目录
WORKDIR /app

# 复制 go.mod 和 go.sum 并下载依赖
COPY go.mod go.sum ./
RUN go mod download

# 复制源代码并构建应用
COPY . .
RUN go build -ldflags "-s -w" -o /app/duckapi .

# --- 最终镜像阶段 ---
# 使用 chromedp/headless-shell 作为最终镜像
FROM chromedp/headless-shell:latest

# 设置工作目录
WORKDIR /app

# 从构建阶段复制编译好的应用
COPY --from=builder /app/duckapi /app/duckapi

# 复制启动脚本并赋予执行权限
COPY start.sh /app/start.sh
RUN chmod +x /app/start.sh

# 暴露端口 (如果 duckapi 在 8080 监听)
# 还需要暴露 9222 (socat) 和 9223 (headless-shell) 如果需要从外部访问调试接口
EXPOSE 8080
#EXPOSE 9222 
#EXPOSE 9223 

# 将启动脚本设置为容器的 ENTRYPOINT
# 这会完全覆盖父镜像的 ENTRYPOINT (run.sh)，确保 /app/start.sh 是容器启动时执行的第一个进程。
ENTRYPOINT ["/app/start.sh"]

# CMD 可以为空，或者为你的 duckapi 提供默认参数。
# 例如：CMD ["--default-arg-for-duckapi"]
CMD []