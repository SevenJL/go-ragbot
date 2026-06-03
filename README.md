# RAG Bot (Go)

基于知识库检索的对话机器人：RAG 文档问答 + 可插拔插件 + 多轮 Skill。零第三方依赖，开箱即跑。

## 快速开始

```bash
go run ./cmd/server      # 打开 http://localhost:8080
```

无 API Key 时使用 Mock LLM + 本地向量即可体验全流程。
完整说明见 **[DEVELOPMENT.md](./DEVELOPMENT.md)**（架构、模块、API、配置、扩展指南）。

## 能力概览

| 能力 | 说明 |
| --- | --- |
| RAG 问答 | 上传 PDF / TXT / Markdown → 分块 → 向量化 → 检索 → LLM 生成回答 |
| 插件机制 | BeforeRAG / AfterRAG 钩子 + FallbackProvider 联网兜底，运行时开关 |
| 多轮 Skill | EMAIL（发邮件）、Weather（查天气），关键词触发，完成后自动回到 RAG |
| 离线演示 | Mock LLM 会基于检索上下文合成模拟回答，无需任何 API Key |
| 生产就绪 | 健康检查、请求日志、优雅关闭、会话过期清理、配置校验 |

## 试试看

- `现在几点` — 时间插件直接回答
- `计算 (3+4)*5` — 计算器插件求值
- `我要发邮件` — 多轮邮件 Skill
- `查天气` — 多轮天气 Skill
- 上传文档后提问 — RAG 检索 + LLM 生成

## 开发

```bash
go test ./...     # 运行所有测试（15 个测试文件，>55 个测试用例）
go vet ./...      # 静态检查
gofmt -w .        # 格式化
go build ./cmd/server -o ragbot  # 编译单二进制
```

## 接入真实服务

参考 `config.example.json`，用环境变量注入 LLM / Embedding / Tavily / SMTP / API Key：

```bash
export OPENAI_API_KEY="sk-xxx"
export OPENAI_BASE_URL="https://api.deepseek.com/v1"
export OPENAI_MODEL="deepseek-chat"
export TAVILY_API_KEY="your-tavily-key"
```

默认 `config.json` 保持离线 mock，避免密钥写入仓库。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/` | 网页控制台（嵌入式 HTML） |
| GET | `/api/health` | 健康检查（状态、chunk 数、插件数等） |
| POST | `/api/chat` | 对话主入口 |
| POST | `/api/upload` | 上传文档入库（multipart） |
| GET | `/api/docs` | 列出已入库文档 |
| DELETE | `/api/docs?id=<docID>` | 删除某文档全部片段 |
| GET | `/api/plugins` | 列出插件及启用状态 |
| POST | `/api/plugins/toggle` | 运行时启用/禁用插件 |
| GET | `/api/skills` | 列出已加载 Skill |

对外暴露时配置 `server.api_key`；前端控制台首次 401 时提示输入 API Key。
