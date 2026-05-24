# duck2api

# Web端 

访问http://你的服务器ip:8080/web

![web使用](https://fastly.jsdelivr.net/gh/xiaozhou26/tuph@main/images/%E5%B1%8F%E5%B9%95%E6%88%AA%E5%9B%BE%202024-04-07%20111706.png)

## Deploy


### 编译部署

```bash
git clone https://github.com/aurora-develop/duck2api
cd duck2api
go build -o duck2api
chmod +x ./duck2api
./duck2api
```

### Docker部署
## Docker部署
您需要安装Docker和Docker Compose。

```bash
docker run -d \
  --name duck2api \
  -p 8080:8080 \
  ghcr.io/aurora-develop/duck2api:latest
```

## Docker Compose部署
创建一个新的目录，例如duck2api，并进入该目录：
```bash
mkdir duck2api
cd duck2api
```
在此目录中下载库中的docker-compose.yml文件：

```bash
docker-compose up -d
```

## Usage

```bash
curl --location 'http://你的服务器ip:8080/v1/chat/completions' \
--header 'Content-Type: application/json' \
--data '{
     "model": "gpt-4o-mini",
     "messages": [{"role": "user", "content": "Say this is a test!"}],
     "stream": true
   }'
```

## 支持的模型

- ~~gpt-3.5-turbo~~  duckduckGO官方已移除3.5模型的支持  
- claude-3-haiku
- llama-3.3-70b
- mixtral-8x7b
- gpt-4o-mini
- o3-mini
## 高级设置

默认情况不需要设置，除非你有需求

### 环境变量
运行时实际使用到的环境变量如下。

#### 基础监听

```bash
SERVER_HOST=0.0.0.0      # 监听地址，默认 0.0.0.0
SERVER_PORT=8080         # 监听端口，默认 8080
PORT=8080                # 兼容 PaaS 平台；当 SERVER_PORT 未设置时使用
PREFIX=/api              # 额外注册一组带前缀的路由，例如 /api/v1/...
```

#### 认证与日志

```bash
Authorization=your_authorization  # API 鉴权 key；未设置时不校验
LOG_LEVEL=INFO                    # 日志级别，例如 DEBUG/INFO/WARN/ERROR
```

#### TLS

```bash
TLS_CERT=path_to_your_tls_cert    # TLS 证书路径
TLS_KEY=path_to_your_tls_key      # TLS 私钥路径
```

#### 代理

```bash
PROXY_URL=http://127.0.0.1:7890   # 显式代理地址
http_proxy=http://127.0.0.1:7890  # PROXY_URL 未设置时的后备代理
```

#### Duck.ai / Chrome DevTools

```bash
DEVTOOLS_URL=ws://127.0.0.1:9222  # Chrome DevTools 远程调试地址
DUCKAI_BROWSER_CHAT=1             # 默认 1。启用基于真实浏览器会话的请求路径
DUCKAI_BROWSER_PREWARM=1          # 默认 1。启动后后台预热浏览器 token
DUCKAI_DIRECT_TOKEN_BUILD=0       # 默认 0。实验开关，尝试页面内直接构造 token
BROWSER_TOKEN_SEED_PROMPT=hi      # 浏览器 token 预热时使用的 seed prompt
FE_VERSION=...                    # 可覆盖默认 x-fe-version
```

#### 缓存与性能

```bash
TOKEN_EXPIRATION_SECONDS=1            # challenge/token 本地缓存秒数
SCRIPTS_CACHE_SECONDS=3600            # challenge JS 缓存秒数
SANDBOX_CACHE_SECONDS=86400           # sandbox 页面缓存秒数
BROWSER_TOKEN_EXPIRATION_SECONDS=1800 # 浏览器抓取 token 的缓存秒数
```

#### 启动前提

如果启用了默认的浏览器路径（`DUCKAI_BROWSER_CHAT=1`），需要先启动一个带远程调试端口的 Chrome/Chromium，例如：

```bash
/Applications/Google\ Chrome.app/Contents/MacOS/Google\ Chrome \
  --remote-debugging-port=9222 \
  --user-data-dir=/tmp/duck2api-chrome
```

## 鸣谢

感谢各位大佬的pr支持，感谢。


## 参考项目


https://github.com/xqdoo00o/ChatGPT-to-API

## License

MIT License
