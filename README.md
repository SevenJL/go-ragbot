# RAG Bot

一个基于 Go 的知识库问答机器人，内置 RAG 检索、插件机制、多轮 Skill，以及一个用 `React + Vite + Tailwind CSS` 构建的网页控制台。

默认配置下可以直接离线运行：
- LLM 使用 mock 实现
- 向量检索使用本地内存存储
- 控制台页面由 Go 服务直接托管

更多实现细节见 [DEVELOPMENT.md](./DEVELOPMENT.md)。

## 功能概览

| 功能 | 说明 |
| --- | --- |
| RAG 问答 | 上传 PDF / TXT / Markdown，自动分块、嵌入、检索并生成回答 |
| 插件机制 | 支持 `BeforeRAG`、`AfterRAG`、`FallbackProvider` |
| 多轮 Skill | 内置 `email`、`weather` 等多轮技能 |
| 动态 Skill | 运行时通过 API 注册 JSON 定义的 Skill |
| 流式聊天 | `/api/v1/chat` 支持 SSE 流式返回 |
| Web 控制台 | 文档管理、插件开关、技能查看、流式聊天 |
| 安全能力 | API Key、JWT、审计日志、限流、中间件防护 |

## 快速开始

### 1. 启动服务

```bash
go run ./cmd/server
```

启动后访问：

```text
http://localhost:8080
```

### 2. 运行测试

```bash
go test ./...
```

### 3. 构建二进制

```bash
go build ./cmd/server
```

## Web 控制台

项目内置了一个现代化前端控制台，源码在 [web](./web) 目录。

当前控制台支持：
- 流式聊天
- 文档上传与删除
- 插件启停
- Skill 列表查看
- API Key 本地保存
- 服务状态概览

### 前端本地开发

```bash
cd web
npm install
npm run dev
```

### 前端生产构建

```bash
cd web
npm install
npm run build
```

构建产物会输出到：

```text
internal/server/web/dist
```

Go 服务会通过 `embed` 自动托管这份前端构建结果，所以前端代码有改动后，需要重新执行一次 `npm run build`。

## 常见体验路径

- 输入 `现在几点`：触发时间插件
- 输入 `计算 (3+4)*5`：触发计算器插件
- 输入 `我要发邮件`：进入多轮邮件 Skill
- 输入 `查天气`：进入多轮天气 Skill
- 上传文档后提问：进入标准 RAG 流程

## API 概览

### 核心接口

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/` | Web 控制台 |
| GET | `/api/v1/health` | 健康检查 |
| POST | `/api/v1/chat` | 聊天问答，支持普通响应和流式响应 |
| POST | `/api/v1/upload` | 上传文档 |
| GET | `/api/v1/docs` | 列出已上传文档 |
| DELETE | `/api/v1/docs?id=<docID>` | 删除文档 |
| GET | `/api/v1/plugins` | 查看插件列表 |
| POST | `/api/v1/plugins/toggle` | 开关插件 |
| GET | `/api/v1/skills` | 查看 Skill 列表 |
| POST | `/api/v1/skills` | 动态注册 Skill |
| DELETE | `/api/v1/skills?name=<name>` | 删除动态 Skill |
| POST | `/api/v1/auth/token` | 申请 JWT Token |
| GET | `/api/v1/export` | 导出向量数据 |
| POST | `/api/v1/import` | 导入向量数据 |
| GET | `/api/v1/metrics` | Prometheus 指标 |

### 流式聊天

向 `/api/v1/chat` 发送：

```json
{
  "session_id": "demo",
  "message": "请总结这份文档",
  "stream": true
}
```

服务端会返回 `text/event-stream`，消息体包含：
- 普通 `data:` 事件：增量文本
- `event: meta`：来源信息、Skill 名称、检索片段
- `data: [DONE]`：流结束

### 动态注册 Skill 示例

```bash
curl -X POST http://localhost:8080/api/v1/skills \
  -H "Content-Type: application/json" \
  -d '{
    "name": "order-lunch",
    "description": "帮助记录点餐",
    "triggers": ["订餐", "点外卖"],
    "steps": [
      {"prompt": "谁要吃饭？", "key": "who"},
      {"prompt": "吃什么？", "key": "dish"}
    ],
    "finish_prompt": "请确认：{who} 点了 {dish}",
    "finish_message": "{who} 的 {dish} 已记录。"
  }'
```

## 认证

支持两种方式：

### API Key

如果配置了 `server.api_key`，前端控制台遇到 `401` 时会提示输入 API Key，并以 `Authorization: Bearer <token>` 形式自动附带到后续请求中。

### JWT

如果配置了 `server.jwt_secret`，并设置了 `server.admin_username` / `server.admin_password`，可以通过：

```text
POST /api/v1/auth/token
```

获取 Token。为兼容旧部署，已配置 `server.api_key` 时也可以把 API Key 作为密码换取管理员 Token。

## 接入真实模型或服务

可以参考 [config.example.json](./config.example.json)，通过环境变量注入配置，例如：

```bash
export OPENAI_API_KEY="sk-xxx"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_MODEL="deepseek-chat"
export TAVILY_API_KEY="your-tavily-key"
```

默认仓库中的 `config.json` 适合本地离线体验。

## 开发说明

常用命令：

```bash
go test ./...
go vet ./...
```

前端改动后的推荐流程：

```bash
cd web
npm install
npm run build
cd ..
go test ./...
```

如果你正在调试网页控制台，记得确认 `8080` 端口没有被旧进程占用。
