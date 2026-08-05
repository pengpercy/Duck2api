# Duck2API 接口文档

Duck2API 将 Duck.ai 服务转换为 OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Images、Files 和 Audio 兼容接口。

## 基础信息

- 默认地址：`http://localhost:8080`
- 默认 API 前缀：`/v1`
- 配置 `PREFIX=/api` 后，会额外提供 `/api/v1/...`，原 `/v1/...` 仍然可用。
- JSON 接口使用 `Content-Type: application/json`。
- 文件、图片编辑和语音转写使用 `multipart/form-data`。
- 设置环境变量 `Authorization` 后，请求必须携带 `Authorization: Bearer <key>`；Anthropic 客户端也可使用 `x-api-key: <key>`。未设置时不鉴权。
- 流式接口使用 Server-Sent Events（SSE）。

## 接口一览

| 方法 | 路径 | 协议/用途 |
|---|---|---|
| POST | `/v1/chat/completions` | OpenAI Chat Completions |
| POST | `/v1/responses` | OpenAI Responses API |
| POST | `/v1/messages` | Anthropic Messages API / Claude Code |
| POST | `/v1/images/generations` | 图像生成 |
| POST | `/v1/images/edits` | 图像编辑 |
| POST | `/v1/files` | 上传文件 |
| GET | `/v1/files` | 列出文件 |
| GET | `/v1/files/:file_id` | 获取文件元数据 |
| DELETE | `/v1/files/:file_id` | 删除文件 |
| GET | `/v1/files/:file_id/content` | 下载文件内容 |
| POST | `/v1/audio/transcriptions` | 语音转文字 |
| POST | `/v1/audio/speech` | 文字转语音 |
| GET | `/v1/models` | 模型列表 |
| GET | `/` | 服务检查 |
| GET | `/ping` | 健康检查 |

## 1. Chat Completions

### `POST /v1/chat/completions`

请求字段：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | string | 是 | 模型 ID |
| `messages` | array | 是 | OpenAI 消息数组 |
| `stream` | boolean | 否 | 默认 `false` |
| `reasoning_effort` | string | 否 | `none` 为快速模式；其他非空档位会启用 Duck.ai 推理模式 |
| `web_search` | boolean | 否 | 启用 Duck.ai WebSearch 工具 |
| `file_ids` | string[] | 否 | 注入通过 Files API 上传的文件 |

基础请求：

```bash
curl http://localhost:8080/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"gpt-5.4-nano",
    "messages":[{"role":"user","content":"你好"}],
    "stream":false
  }'
```

联网搜索与推理：

```json
{
  "model": "gpt-5.4-mini",
  "messages": [{"role":"user","content":"查询今天的重要新闻"}],
  "reasoning_effort": "high",
  "web_search": true
}
```

图片输入：

```json
{
  "model": "gpt-5.4-nano",
  "messages": [{
    "role": "user",
    "content": [
      {"type":"text","text":"描述图片"},
      {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}
    ]
  }]
}
```

流式响应以 `data: {...}` 分片，并以 `data: [DONE]` 结束。非流式响应返回 OpenAI `chat.completion` 对象。

## 2. Responses API

### `POST /v1/responses`

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | string | 否 | 省略时使用 `gpt-5-mini` |
| `input` | string/array | 是 | 字符串或 Responses 消息项 |
| `instructions` | string | 否 | 转换为系统消息 |
| `stream` | boolean | 否 | 是否返回 Responses SSE 事件 |
| `reasoning_effort` | string | 否 | 推理档位 |

```bash
curl http://localhost:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"gpt-5.4-nano","instructions":"简洁回答","input":"你好"}'
```

流式事件顺序包括：`response.created`、`response.output_item.added`、`response.content_part.added`、一个或多个 `response.output_text.delta`、`response.output_text.done`、`response.content_part.done`、`response.output_item.done`、`response.completed`。

## 3. Anthropic Messages API

### `POST /v1/messages`

这是 Claude Code 使用的 Anthropic 原生协议入口。支持普通响应和 Anthropic SSE 流。

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `model` | string | 否 | 默认 `claude-haiku-4-5` |
| `messages` | array | 是 | Anthropic 消息数组 |
| `max_tokens` | integer | 否 | 默认 `1024`；作为兼容参数接收 |
| `system` | string/array | 否 | 系统提示，支持文本块和 `cache_control` |
| `stream` | boolean | 否 | 返回 Anthropic SSE |
| `thinking` | object | 否 | `enabled` 及 `budget_tokens` 会映射到 Duck.ai 推理档位 |
| `tools` | array | 否 | 接收 Anthropic 工具定义；Duck.ai 只输出文本，不直接生成原生 `tool_use` |
| `tool_choice` | object | 否 | 兼容接收 |

基础请求：

```bash
curl http://localhost:8080/v1/messages \
  -H 'Content-Type: application/json' \
  -H 'anthropic-version: 2023-06-01' \
  -d '{
    "model":"claude-haiku-4-5",
    "max_tokens":1024,
    "messages":[{"role":"user","content":"你好"}]
  }'
```

扩展思考与 prompt caching：

```json
{
  "model": "claude-haiku-4-5",
  "max_tokens": 2048,
  "thinking": {"type":"enabled","budget_tokens":5000},
  "system": [{
    "type":"text",
    "text":"你是代码助手",
    "cache_control":{"type":"ephemeral"}
  }],
  "messages":[{"role":"user","content":"分析这段代码"}]
}
```

支持的内容块：

- `text`：保留为文本。
- `image`：Anthropic base64 图片转换为 OpenAI/Duck.ai 图片内容。
- `thinking`：不会把历史思维链发送给上游；请求级 `thinking` 会映射为推理档位。
- `tool_use`、`tool_result`：历史内容折叠为文本以维持上下文。当前上游只产生文本响应，因此响应不会生成原生 Anthropic 工具调用。

非流式响应为 `type: "message"`，包含文本内容块及 `usage`。流式事件顺序为 `message_start`、`content_block_start`、`content_block_delta`、`content_block_stop`、`message_delta`、`message_stop`。

缓存统计字段：`cache_creation_input_tokens` 和 `cache_read_input_tokens`。服务还返回 `X-Cache`、`Age`、`X-Cache-Creation-Tokens`、`X-Cache-Read-Tokens` 响应头。

## 4. 图像生成

### `POST /v1/images/generations`

```bash
curl http://localhost:8080/v1/images/generations \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"一只坐在窗边的猫","model":"gpt-5.4-nano","n":1}'
```

字段：`prompt`（必填）、`model`（默认 `gpt-5.4-nano`）、`n`（默认 1）、`reasoning_effort`、`response_format`。响应为：

```json
{"created":1719398400,"data":[{"b64_json":"...","revised_prompt":"..."}]}
```

## 5. 图像编辑

### `POST /v1/images/edits`

Multipart：

```bash
curl http://localhost:8080/v1/images/edits \
  -F 'image=@photo.png' \
  -F 'prompt=把背景改成海滩' \
  -F 'model=gpt-5.4-nano'
```

JSON base64：

```json
{"image":"<base64>","prompt":"把背景改成海滩","model":"gpt-5.4-nano"}
```

输入图片会压缩至最长边 512px 并转换为 WebP 后发往 Duck.ai。单个 multipart 请求最大读取 32 MiB。

## 6. 文件管理

### 上传

```bash
curl http://localhost:8080/v1/files -F 'file=@document.txt' -F 'purpose=assistants'
```

响应对象包含 `id`、`object`、`bytes`、`created_at`、`filename` 和 `purpose`。

### 查询、下载与删除

```bash
curl http://localhost:8080/v1/files
curl http://localhost:8080/v1/files/file-xxx
curl http://localhost:8080/v1/files/file-xxx/content -o document.txt
curl -X DELETE http://localhost:8080/v1/files/file-xxx
```

文件保存在当前进程内存中，服务重启后清空，不会跨实例共享。上传限制为 32 MiB。聊天请求可通过顶层 `file_ids`，或多模态内容块 `{"type":"input_file","file_id":"file-xxx"}` 引用文件。文本文件最多注入约 50,000 字节；图片文件会转换为 WebP。

## 7. 语音转文字

### `POST /v1/audio/transcriptions`

```bash
curl http://localhost:8080/v1/audio/transcriptions \
  -F 'file=@audio.webm' \
  -F 'model=whisper-1'
```

支持 WebM、OGG、MP3、WAV、M4A、FLAC、OPUS 和 AAC。`model` 是 OpenAI 兼容参数，实际调用 Duck.ai dictation。响应：`{"text":"..."}`。

## 8. 文字转语音

### `POST /v1/audio/speech`

```bash
curl http://localhost:8080/v1/audio/speech \
  -H 'Content-Type: application/json' \
  -d '{"model":"tts-1","input":"你好，世界","voice":"alloy","response_format":"ogg"}' \
  --output speech.ogg
```

| 字段 | 默认值 | 说明 |
|---|---|---|
| `model` | `tts-1` | 兼容参数 |
| `input` | — | 必填 |
| `voice` | `alloy` | 语音名称 |
| `response_format` | `mp3` | `mp3`、`wav`、`ogg`、`opus`、`flac`、`aac`、`pcm` |
| `speed` | — | 接收但当前未应用 |

底层通过 Duck.ai WebRTC/Realtime 会话接收 Opus。OGG/Opus 无需外部工具；MP3、WAV、FLAC、AAC 和 PCM 转换需要系统安装 `ffmpeg`。转换失败时接口回退返回 OGG，并将 `Content-Type` 设置为 `audio/ogg`。

## 9. 模型列表

### `GET /v1/models`

当前公开列表：

- `gpt-5.4-mini`
- `gpt-5.4-nano`
- `tinfoil/gpt-oss-120b`
- `claude-haiku-4-5`
- `mistral-small`

模型别名可能在转发时映射为 Duck.ai 实际模型 ID。

## 10. 健康检查

```bash
curl http://localhost:8080/
# {"message":"Hello World!"}

curl http://localhost:8080/ping
# {"message":"pong"}
```

## 错误格式

大多数错误使用下列结构：

```json
{"error":{"message":"...","type":"invalid_request_error","code":"..."}}
```

常见状态码：`400` 参数错误、`401` 鉴权失败、`404` 文件不存在、`418/429` Duck.ai challenge/限流、`500` 内部错误、`502` 上游请求失败。VQD challenge 的 `418/429` 会在 Provider 内自动刷新并重试。

## 环境变量

| 变量 | 默认值 | 说明 |
|---|---|---|
| `SERVER_HOST` | `0.0.0.0` | 监听地址 |
| `SERVER_PORT` | `8080` | 监听端口；未设置时可使用 `PORT` |
| `Authorization` | 空 | API Bearer key |
| `PREFIX` | 空 | 额外路由前缀，例如 `/api` |
| `PROXY_URL` | 空 | 上游代理；其次读取 `http_proxy` |
| `TLS_CERT` / `TLS_KEY` | 空 | HTTPS 证书与私钥 |
| `LOG_LEVEL` | — | 日志级别 |
| `FE_VERSION` | 内置值 | 覆盖 Duck.ai 前端版本 |
| `TOKEN_EXPIRATION_SECONDS` | `1` | VQD token 缓存 |
| `SCRIPTS_CACHE_SECONDS` | `3600` | challenge JS 缓存 |
| `CLAUDE_CODE_EFFORT_LEVEL` | 空 | Anthropic 未提供 `thinking` 时的默认推理档位 |

代理优先级为：`PROXY_URL`、项目目录中的 `proxies.txt`（逐行轮询）、`http_proxy`。

聊天及其他 Duck.ai 接口使用内嵌 Goja 运行时计算 VQD challenge，不依赖 Chrome/Chromium。
