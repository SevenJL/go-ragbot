# RAG Bot (Go)

基于知识库检索的对话机器人：RAG 文档问答 + 可插拔插件 + 多轮 Skill。零第三方依赖，开箱即跑。

```bash
go run ./cmd/server      # 打开 http://localhost:8080
```

无 API Key 时使用 Mock LLM + 本地向量即可体验全流程。
完整说明见 **[DEVELOPMENT.md](./DEVELOPMENT.md)**（架构、模块、API、配置、扩展指南）。

- 试试：「现在几点」「计算 (3+4)*5」「我要发邮件」「查天气」，以及上传文档后提问。
- 测试：`go test ./...`，静态检查：`go vet ./...`，格式化：`gofmt -w .`。
- 接真实服务：参考 `config.example.json`，用环境变量注入 LLM/Embedding/Tavily/SMTP/API Key。
  默认 `config.json` 仍保持离线 mock，避免把密钥写进仓库。
- 对外暴露时配置 `server.api_key`；前端控制台会在首次 401 时提示输入 API Key。
