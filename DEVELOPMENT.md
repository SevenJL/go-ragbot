# RAG Bot 开发文档

基于知识库检索的对话机器人 —— Go 语言实现。
内置三大能力：**RAG 文档问答**、**可插拔插件机制**、**多轮任务型 Skill**。

> 设计原则：**零第三方依赖**（仅 Go 标准库），开箱即可 `go build` / `go run`，
> 无需联网下载任何模块；同时通过接口抽象，方便后续替换为真实的大模型、
> Embedding 服务与向量库（Chroma / FAISS）。

---

## 1. 项目概述

| 能力 | 说明 |
| --- | --- |
| RAG 核心 | 上传 PDF / TXT / Markdown → 分块 → 向量化 → 本地向量库 → 检索 → 拼接 Prompt → 调用 LLM |
| 插件机制 | 统一接口 `BeforeRAG` / `AfterRAG`，支持运行时启用/禁用，按配置文件加载 |
| Skill 能力 | 有状态的多轮任务流（发邮件、查天气），可由关键词或 LLM 意图触发，完成后回到 RAG 模式 |

整个系统是一个单进程 HTTP 服务，自带一个网页控制台（已嵌入二进制，单文件部署）。

---

## 2. 技术选型

| 关注点 | 选择 | 说明 |
| --- | --- | --- |
| 语言 | Go 1.22 | 单二进制、并发友好、部署简单 |
| Web 框架 | `net/http`（标准库） | 不引入 gin/echo，降低依赖 |
| 配置 | JSON（`encoding/json`） | 零依赖；如需 YAML 可替换为 `gopkg.in/yaml.v3` |
| 向量库 | 内存 + JSON 持久化 | 轻量本地实现，接口与 Chroma 对齐，可平滑替换 |
| Embedding | 本地哈希向量 / OpenAI 兼容 API | 本地实现无需模型权重；生产可换 bge-small-zh |
| LLM | OpenAI 兼容 API / Mock | 兼容 DeepSeek、智谱、通义千问的 `/chat/completions` |
| PDF 解析 | `pdftotext`（poppler）+ 纯 Go 兜底 | 优先用系统工具，缺失时回退到内置简单解析 |
| 前端 | 原生 HTML/JS（`go:embed`） | 单文件嵌入；可替换为 Vue3 工程 |

为什么本地 Embedding 不直接用 `sentence-transformers`：那是 Python 生态，Go 端调用需要额外服务或 cgo。
本项目用「字符 bigram + 词哈希 + 带符号哈希 + L2 归一化」实现了一个**确定性、可离线**的词法语义向量，
用于本地开发与演示足够；生产环境把 `embedding.provider` 改为 `openai` 指向真实 Embedding 服务即可。

---

## 3. 整体架构

```
                            ┌──────────────────────────────────────────┐
   浏览器 / API 调用方  ───▶ │              HTTP Server                   │
                            │  /api/chat  /api/upload  /api/docs ...     │
                            └───────────────────┬────────────────────────┘
                                                │
                                          ┌─────▼─────┐
                                          │  Engine   │  编排核心（rag 包）
                                          └─────┬─────┘
       ┌─────────────┬──────────────┬──────────┼───────────┬───────────────┐
       ▼             ▼              ▼          ▼           ▼               ▼
  ┌─────────┐  ┌──────────┐  ┌──────────┐ ┌────────┐ ┌──────────┐  ┌────────────┐
  │ Session │  │  Skills  │  │ Plugins  │ │ Embed  │ │  Vector  │  │    LLM     │
  │  状态   │  │ 多轮任务 │  │ 前后置钩子│ │ 向量化 │ │  Store   │  │ 生成回答   │
  └─────────┘  └──────────┘  └──────────┘ └────────┘ └──────────┘  └────────────┘
                                                          ▲
                                                          │
                              ┌───────────────────────────┴──────────┐
                              │   Document: Loader(PDF/TXT/MD) + Chunker │
                              └────────────────────────────────────────┘
```

**核心思路**：`Engine` 是唯一的编排者，它按固定优先级把一条用户消息路由给
Skill / Plugin / RAG，并负责文档入库。其余模块都通过接口被依赖，互不耦合。

---

## 4. 目录结构

```
ragbot/
├── go.mod                       # module ragbot, go 1.22, 无第三方依赖
├── config.json                  # 运行配置
├── cmd/server/main.go           # 入口：装配各组件并启动 HTTP 服务
├── data/                        # 向量库持久化文件存放处
└── internal/
    ├── core/        types.go        # 共享数据类型（无内部依赖，避免循环引用）
    ├── config/      config.go       # 配置加载与默认值
    ├── document/    loader.go       # PDF/TXT/MD 文本提取
    │                chunker.go      # 文本分块（按段落/句子，支持重叠）
    ├── embedding/   embedding.go    # Embedder 接口 + 工厂
    │                local.go        # 本地哈希向量（离线）
    │                openai.go       # OpenAI 兼容 Embedding 客户端
    ├── vectorstore/ store.go        # Store 接口
    │                memory.go       # 内存 + JSON 持久化，余弦检索
    ├── llm/         llm.go          # LLM 接口 + 工厂
    │                openai.go       # OpenAI 兼容对话客户端
    │                mock.go         # 离线 Mock LLM
    ├── plugin/      plugin.go       # Plugin 接口、Manager、FallbackProvider
    │                time.go         # 时间插件
    │                calculator.go   # 计算器插件（含表达式求值器）
    │                websearch.go    # 联网搜索兜底插件
    ├── skill/       skill.go        # Skill 接口 + Manager
    │                email.go        # 发邮件 Skill
    │                weather.go      # 查天气 Skill
    ├── session/     session.go      # 会话状态（历史 + 当前 Skill 状态）
    ├── rag/         engine.go       # 编排核心：入库 + 应答
    └── server/      server.go       # HTTP 路由与处理器
                     index.html      # 嵌入式网页控制台
```

依赖方向（无环）：`core` 被所有人依赖；`rag.Engine` 依赖其余全部；`server` 只依赖 `rag`。

---

## 5. 核心模块详解

### 5.1 共享类型（`internal/core`）

```go
type Message        struct { Role, Content string }              // 传给 LLM 的一轮对话
type Chunk          struct { ID, DocID, Source string; Index int; Text string; Embedding []float64 }
type RetrievedChunk struct { Chunk; Score float64 }               // 检索命中片段 + 相似度
type DocInfo        struct { ID, Source string; Chunks int }      // 文档摘要
```

向量统一用 `[]float64`，简化数学运算（不在 float32/float64 间反复转换）。

### 5.2 文档处理（`internal/document`）

**Loader**（`loader.go`）按扩展名分发：

- `.txt`：原样读取。
- `.md / .markdown`：`stripMarkdown` 去掉标题井号、强调符、代码围栏、图片，链接保留可见文字。
- `.pdf`：先尝试调用系统 `pdftotext`（poppler-utils，最稳健）；不可用时回退到
  `pdfNaive` —— 解压 FlateDecode 流，正则提取 `(...) Tj` / `[...] TJ` 文本操作符。
  纯 Go 兜底只能处理简单文本型 PDF，生产建议安装 poppler 或换用专门库。

**Chunker**（`chunker.go`）以**字符（rune）**为单位，先按 `\n\n` 切段、再按中英文句末标点
（`。！？；!?;`）切句，然后贪心打包到 `chunk_size` 大小的窗口，相邻窗口保留 `chunk_overlap`
个字符的尾部重叠以保证上下文连续。超长单句会被硬切。以字符为单位是为了正确处理中文。

### 5.3 Embedding（`internal/embedding`）

```go
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float64, error)
    Dim() int
    Name() string
}
```

- **Local**（默认，离线）：把文本特征（小写拉丁词 + CJK 单字 + CJK bigram）用 FNV 哈希到
  固定维度 `dim` 的桶里，使用带符号哈希减少碰撞抵消，最后 L2 归一化（使余弦相似度 = 点积）。
- **OpenAIEmbedder**：调用 `/embeddings`，可指向 OpenAI 或任何兼容网关（可把 bge-small-zh
  挂在兼容网关后面再指过来）。

工厂 `embedding.New(cfg)` 按 `provider` 返回对应实现。

### 5.4 向量库（`internal/vectorstore`）

```go
type Store interface {
    Add(ctx, chunks) error
    Search(ctx, query []float64, topK int) ([]RetrievedChunk, error)
    Docs() []DocInfo
    Delete(docID string) error
    Save() error
    Count() int
}
```

`Memory` 实现：全部 chunk 放内存切片，`Search` 对每个 chunk 算余弦相似度后排序取 topK，
增删后写回 JSON 文件持久化，使用 `sync.RWMutex` 保证并发安全。
**替换为 Chroma**：只需新建一个实现同一 `Store` 接口的 Chroma HTTP 客户端，在 `main.go` 里换掉即可，
上层完全无感。

### 5.5 LLM（`internal/llm`）

```go
type LLM interface {
    Chat(ctx context.Context, messages []core.Message) (string, error)
    Name() string
}
```

- **OpenAI**：POST `{base_url}/chat/completions`，带 `Authorization: Bearer`。
  DeepSeek / 智谱 / 通义千问都暴露同样的接口，改 `base_url` + `model` + `api_key` 即可。
- **Mock**：离线占位，回显问题与上下文，让整条链路无 Key 也能跑通。

### 5.6 编排核心（`internal/rag` —— 最重要）

`Engine` 持有 embedder / store / llm / plugin.Manager / skill.Manager / session.Store。

**入库** `Ingest(ctx, filename, data)`：
`LoadText` → `Chunk` → `Embed` → 组装 `[]core.Chunk`（doc id 取文件名+内容的 SHA1 前 12 位）→ `store.Add`。

**应答** `Answer(ctx, sessionID, message)` 是固定优先级的决策管线（见第 6 节）。

### 5.7 插件机制（`internal/plugin`）

统一接口（对应需求里的 `before_rag` / `after_rag`）：

```go
type Plugin interface {
    Name() string
    Description() string
    IsEnabled() bool
    SetEnabled(bool)
    BeforeRAG(ctx, query string) (*Result, error)          // 可短路：Result.Handled=true 直接返回
    AfterRAG(ctx, query, answer string) (*Result, error)   // 可改写最终回答
}

// 可选能力：知识库无命中时提供补充上下文（websearch 实现它）
type FallbackProvider interface {
    Fallback(ctx, query string) (extraContext string, err error)
}
```

- 嵌入 `base` 结构体复用启用/禁用（带 `sync.RWMutex`）。
- `Manager` 按注册顺序遍历：`RunBeforeRAG` 第一个 `Handled` 即短路；`RunAfterRAG` 依次改写；
  `Fallbacks` 收集所有 `FallbackProvider` 的补充上下文。
- 内置插件：
  - **time**：命中「现在几点 / 今天日期 / what time」等关键词，直接返回当前时间（短路）。
  - **calculator**：抽取数学表达式并用内置递归下降求值器计算（支持 `+ - * / % ^` 与括号），短路返回。
  - **websearch**：实现 `FallbackProvider`，知识库无命中时联网补充上下文（支持 Tavily 风格 API，
    未配置 Key 时返回 mock 提示）。

### 5.8 Skill 机制（`internal/skill`）

```go
type Skill interface {
    Name() string
    Description() string
    Match(input string) bool                                  // 关键词触发
    Start(ctx, sess) (string, error)                          // 进入流程，返回首条提示
    Handle(ctx, sess, input string) (reply string, done bool, err error) // 处理一轮输入
}
```

- 状态存在 `session.Session` 上：`ActiveSkill` / `SkillStep` / `SkillData`。
- **EmailSkill**：收件人 → 主题 → 正文 → 确认 → 发送（未配置 SMTP 时模拟发送；配置后用
  `net/smtp` 真实发送）。任意时刻输入「取消/退出」可中止。
- **WeatherSkill**：城市 → 日期 → 返回天气（默认确定性 mock，可接 open-meteo 等）。
- Skill 完成后调用 `sess.EndSkill()`，引擎自动回到 RAG 模式。

### 5.9 会话（`internal/session`）

`Store` 是线程安全的 `map[sessionID]*Session`。`Session` 保存有上限的历史（默认保留最近 20 条）
和当前 Skill 状态。`AddMessage` / `StartSkill` / `EndSkill` 是主要操作。

---

## 6. 请求处理全流程

`Engine.Answer` 的优先级管线（顺序很关键）：

```
用户消息
   │  记录到会话历史
   ▼
① 会话中已有激活的 Skill ?  ── 是 ──▶ 交给该 Skill.Handle，返回（done 时自动回到 RAG）
   │否
   ▼
② 命中某个 Skill 的触发词 ? ── 是 ──▶ Skill.Start，返回首条引导提示
   │否
   ▼
③ 插件 BeforeRAG 短路 ?      ── 是 ──▶ 走 AfterRAG 后直接返回（time/calculator）
   │否
   ▼
④ 向量检索 retrieve(topK)
   │
   ▼
⑤ 没有达到 min_score 的好命中 ? ── 是 ──▶ 调用 FallbackProvider（websearch）补充上下文
   │
   ▼
⑥ 拼接 Prompt（system: 角色+上下文+补充资料；history；user）→ LLM.Chat
   │
   ▼
⑦ 插件 AfterRAG 后处理（可改写答案）
   │
   ▼
返回 { answer, source, skill_name?, retrieved? }
```

`source` 字段标明本轮答案来自 `skill` / `plugin` / `rag`，便于前端与调试。

---

## 7. HTTP API

基础地址默认 `http://localhost:8080`。请求/响应均为 JSON（上传除外）。

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/` | 网页控制台（嵌入式 HTML） |
| POST | `/api/chat` | 对话主入口 |
| POST | `/api/upload` | 上传文档入库（multipart） |
| GET | `/api/docs` | 列出已入库文档 |
| DELETE | `/api/docs?id=<docID>` | 删除某文档全部片段 |
| GET | `/api/plugins` | 列出插件及启用状态 |
| POST | `/api/plugins/toggle` | 运行时启用/禁用插件 |
| GET | `/api/skills` | 列出已加载 Skill |

**POST /api/chat**

```jsonc
// 请求
{ "session_id": "u-123", "message": "公司的报销流程是什么？" }
// 响应
{
  "answer": "……",
  "source": "rag",                 // skill | plugin | rag
  "skill_name": "",                // source=skill 时有值
  "retrieved": [                   // source=rag 时返回命中片段
    { "id": "ab12#3", "doc_id": "ab12", "source": "手册.pdf", "index": 3, "text": "……", "score": 0.42 }
  ]
}
```

**POST /api/upload**：表单字段名 `file`。

```jsonc
{ "doc_id": "ab12cd34ef56", "filename": "手册.pdf", "chunks": 27 }
```

**POST /api/plugins/toggle**

```jsonc
{ "name": "websearch", "enabled": false }
```

示例 curl：

```bash
# 上传文档
curl -F "file=@手册.pdf" http://localhost:8080/api/upload
# 提问
curl -X POST http://localhost:8080/api/chat \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"u1","message":"报销流程是什么"}'
# 如果配置了 server.api_key，额外加：
#   -H "Authorization: Bearer $RAGBOT_API_KEY"
# 或：
#   -H "X-API-Key: $RAGBOT_API_KEY"
# 关闭联网搜索插件
curl -X POST http://localhost:8080/api/plugins/toggle \
  -H 'Content-Type: application/json' -d '{"name":"websearch","enabled":false}'
```

---

## 8. 配置说明（`config.json`）

```jsonc
{
  "server": {
    "addr": ":8080",
    "api_key": ""                            // 可选；配置后 /api/* 需要 Bearer 或 X-API-Key
  },

  "llm": {
    "provider": "mock",                       // mock | openai/deepseek/zhipu/qwen
    "base_url": "https://api.deepseek.com/v1", // OpenAI 兼容端点
    "api_key":  "",
    "model":    "deepseek-chat"
  },

  "embedding": {
    "provider": "local",                      // local | openai
    "base_url": "", "api_key": "", "model": "",
    "dim": 256                                 // local 向量维度
  },

  "rag": {
    "chunk_size":    500,                      // 每块字符数
    "chunk_overlap": 80,                       // 相邻块重叠字符数
    "top_k":         4,                        // 检索返回片段数
    "min_score":     0.12,                     // 低于此值视为“无结果”，触发兜底
    "store_path":    "data/vectorstore.json"
  },

  "plugins": {
    "enabled": ["time", "calculator", "websearch"],   // 控制加载哪些插件
    "websearch": { "provider": "mock", "api_key": "", "endpoint": "https://api.tavily.com/search" }
  },

  "skills": {
    "enabled": ["email", "weather"],
    "email":   { "smtp_host": "", "smtp_port": 587, "username": "", "password": "", "from": "" },
    "weather": { "provider": "mock", "api_key": "" }
  }
}
```

切换到真实模型的最小改动示例（以 DeepSeek 为例）：

```jsonc
"llm": { "provider": "openai", "base_url": "https://api.deepseek.com/v1",
         "api_key": "sk-xxx", "model": "deepseek-chat" }
```

真实服务推荐参考 `config.example.json`，用环境变量注入密钥，避免把 token 写入仓库：

- `OPENAI_API_KEY` / `OPENAI_BASE_URL` / `OPENAI_MODEL`：LLM。
- `OPENAI_EMBEDDING_MODEL`：真实 Embedding 模型。
- `TAVILY_API_KEY`：联网搜索插件，使用 Tavily Search API 的 Bearer 鉴权。
- `SMTP_HOST` / `SMTP_USERNAME` / `SMTP_PASSWORD` / `SMTP_FROM`：邮件发送。
- `RAGBOT_API_KEY`：HTTP API 鉴权；前端控制台会在首次 401 时提示输入。
- `skills.weather.provider=open-meteo`：使用 Open-Meteo 地理编码和天气预报 API。

---

## 9. 构建与运行

环境要求：Go 1.22+；解析 PDF 建议安装 poppler（`apt install poppler-utils` 提供 `pdftotext`）。

```bash
cd ragbot
go run ./cmd/server                 # 直接运行（默认读 ./config.json）
# 或
go build -o ragbot ./cmd/server     # 编译为单二进制（已嵌入网页）
./ragbot -config config.json
```

常用检查：

```bash
gofmt -w .
go test ./...
go vet ./...
go build ./...
```

启动后浏览器打开 `http://localhost:8080`：左侧上传文档、开关插件，右侧聊天。
无任何 API Key 时即可用 Mock LLM + 本地向量跑通全链路；插件（时间/计算）、Skill（发邮件/查天气）
均可离线体验。

---

## 10. 扩展开发指南

### 10.1 新增一个插件

1. 在 `internal/plugin/` 新建 `foo.go`，嵌入 `base`，实现 `Plugin` 接口（如需兜底再实现 `FallbackProvider`）。

```go
type FooPlugin struct{ base }
func NewFooPlugin(enabled bool) *FooPlugin { p := &FooPlugin{}; p.SetEnabled(enabled); return p }
func (p *FooPlugin) Name() string        { return "foo" }
func (p *FooPlugin) Description() string  { return "示例插件" }
func (p *FooPlugin) BeforeRAG(ctx context.Context, q string) (*plugin.Result, error) {
    if 命中条件 { return &plugin.Result{Handled: true, Answer: "直接回答"}, nil }
    return nil, nil
}
func (p *FooPlugin) AfterRAG(ctx context.Context, q, a string) (*plugin.Result, error) { return nil, nil }
```

2. 在 `cmd/server/main.go` 注册：`pm.Register(plugin.NewFooPlugin(config.Enabled(cfg.Plugins.Enabled, "foo")))`。
3. 在 `config.json` 的 `plugins.enabled` 加上 `"foo"`。

### 10.2 新增一个 Skill

1. 在 `internal/skill/` 新建文件，实现 `Skill` 接口，用 `sess.SkillStep` / `sess.SkillData` 驱动状态机，
   结束时调用 `sess.EndSkill()` 并返回 `done=true`。
2. 在 `main.go` 按配置注册：`if config.Enabled(cfg.Skills.Enabled, "foo") { sm.Register(...) }`。
3. 在 `config.json` 的 `skills.enabled` 加上对应名字。

> 进阶：当前 Skill 触发是关键词匹配（`MatchTrigger`）。要接入 LLM 意图识别，
> 可在 `Engine.Answer` 的第 ② 步前增加一次「让 LLM 判断意图并选择 Skill」的调用，
> 再调用对应 `Skill.Start`。

### 10.3 接入真实向量库（Chroma）

新建 `internal/vectorstore/chroma.go`，实现 `Store` 接口（`Add` 写入 collection，
`Search` 调 query 接口），在 `main.go` 用它替换 `NewMemory`。上层 `Engine` 不改一行。

### 10.4 接入真实 Embedding（bge-small-zh）

把模型挂在一个 OpenAI 兼容的 embeddings 网关后，配置 `embedding.provider=openai` +
`base_url` 指过去即可；或新增一个实现 `Embedder` 接口的客户端。

---

## 11. 已知限制与后续规划

- 纯 Go 的 PDF 兜底解析较弱，复杂排版/扫描件请装 poppler 或换专门库（如 `ledongthuc/pdf`）。
- 本地哈希 Embedding 是词法级别的，召回质量不如真实向量模型，仅适合离线演示。
- 内存向量库适合中小规模；大规模请换 Chroma / FAISS / pgvector。
- 同一 `session_id` 的 `Answer` 已做会话级互斥；高并发长请求会按会话串行排队，必要时可引入队列/超时策略。
- Skill 触发为关键词规则，未接入 LLM 意图识别（已在 10.2 给出扩展点）。
- HTTP API 已支持可选 Bearer / `X-API-Key` 鉴权；对外暴露前仍建议补充限流、HTTPS、审计日志与更细粒度权限。

---

## 附：模块依赖与可替换点速查

| 接口 | 默认实现 | 生产可替换为 |
| --- | --- | --- |
| `embedding.Embedder` | 本地哈希向量 | OpenAI 兼容 / bge-small-zh 网关 |
| `vectorstore.Store` | 内存 + JSON | Chroma / FAISS / pgvector |
| `llm.LLM` | Mock | DeepSeek / 智谱 / 通义千问 / OpenAI |
| `plugin.Plugin` | time / calculator / websearch | 任意自定义插件 |
| `skill.Skill` | email / weather | 任意多轮任务 |

所有替换都只动 `cmd/server/main.go` 的装配，不影响 `Engine` 与其他模块——这正是接口抽象的价值。
