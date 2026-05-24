#!/bin/bash
set -ex # 复制父级脚本的行为，有助于调试

# 1. 启动 socat 进程 (复制父级 run.sh 的第一行)
# 注意：这里我们使用 exec socat ... & 确保 socat 进程作为独立的后台进程运行，
# 并且不会阻塞后续命令。如果不需要socat替换当前shell，也可以不用exec。
exec socat TCP4-LISTEN:9222,fork TCP4:127.0.0.1:9223 &

# 2. 启动 headless-shell 进程 (复制父级 run.sh 的第二部分)
# 注意：这里我们不再使用 exec，因为我们需要 start.sh 继续执行。
# 我们将它的输出重定向到 /dev/null 并放到后台运行 (&)。
# 这里的参数列表需要与父级 run.sh 保持一致，但 $1，$2 等参数我们希望留给 duckapi。
/headless-shell/headless-shell \
  --no-sandbox \
  --use-gl=angle \
  --use-angle=swiftshader \
  --remote-debugging-address=0.0.0.0 \
  --remote-debugging-port=9223 \
  > /dev/null 2>&1 & # 放入后台并丢弃输出

# 可选：等待 headless-shell 启动。这可以防止 duckapi 在 Chrome 还没完全准备好时尝试连接。
# 在生产环境中，你可能希望实现一个更健壮的健康检查，而不是简单的 sleep。
sleep 2

# 3. 启动你的 Go 应用
# 使用 exec 确保你的 Go 应用成为容器的 PID 1。
# 将所有传递给容器的参数 ($@) 传递给你的 duckapi 应用。
exec /app/duckapi "$@"