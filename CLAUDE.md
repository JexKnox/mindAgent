# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

mindAgent 是一个基于字节 [Eino](https://github.com/cloudwego/eino) 框架 ReAct 机制的命令行 AI Agent（Go），具备完整的记忆系统（短期滑动窗口 + 长期向量记忆）。Agent 在「模型 → 工具调用 → 结果回填 → 再推理」的循环中自动选择工具，直到模型不再发起工具调用；CLI 侧维护多轮对话历史。代码与注释均使用中文。

## Commands

```bash
make run     # 运行 Agent REPL（go run ./cmd/mindagent）
make build   # 编译到 bin/mindagent
make test    # 运行全部测试
make vet     # go vet

# 运行单个包的测试
go test ./internal/memory/

# 运行单个测试函数
go test ./internal/memory/ -run TestServiceDedup
```

运行 Agent 需要配置（复制 `.env.example` 为 `.env`）：`LLM_API_KEY`（必填）、`LLM_BASE_URL`（默认 OpenAI，火山方舟用 `https://ark.cn-beijing.volces.com/api/v3`）、`LLM_MODEL`（默认 `gpt-4o-mini`）、`LLM_EMBEDDING_MODEL`（记忆向量化，默认 `text-embedding-3-small`，火山方舟需改）。端到端对话测试必须有真实可用的 API Key。

## Architecture

数据流：`main.go` 加载配置 → 组装 Agent（ChatModel + Embedder + 工具 + 记忆系统）→ REPL 循环调用 `agent.Generate(ctx, messages)`。

- **`cmd/mindagent/main.go`** — 所有 Eino 组件的组装点：`openaimodel.NewChatModel` + `openaiembed.NewEmbedder`（Embedding 复用同一 API Key/BaseURL）+ `react.NewAgent`（`ToolCallingModel` + `ToolsConfig` + `memory.NewMemoryModifier` 注入 persona 与记忆 + `MaxStep` 防死循环）。REPL 元命令：`reset`（清短期对话）、`memories` / `forget <id>`（管理长期记忆）。生成失败的轮次会从历史中移除以免污染上下文。
- **`internal/tools/`** — 基础工具（当前时间、计算器），用 `utils.InferTool(name, desc, fn)` 从 Go 函数签名自动推导 JSON Schema：入参是带 `jsonschema:"description=..."` tag 的结构体。工具函数内部通过 `trace()` 打印调用轨迹（`🔧 [工具] ...`），这是 CLI 里观察 ReAct 循环的主要手段。`calculator` 用 `go/parser` 解析 AST 求值，只允许纯算术节点（安全约束，勿放宽）。
- **`internal/config/`** — 零依赖的 `.env` 解析器 + 环境变量读取。已存在的环境变量优先于 `.env` 中的值。
- **`internal/memory/`** — 记忆系统（见下）。

### 记忆系统（internal/memory/）

- **`vectorstore.go`** — 嵌入式本地向量库：实现 eino 的 `indexer.Indexer` / `retriever.Retriever` 接口（可无缝换 eino-ext 官方向量库组件），`data/memories.json` 持久化（临时文件+rename 原子写），余弦相似度暴力检索。**索引和检索必须用同一个 Embedder**（维度一致）。
- **`memory.go`** — `Service` 服务层：`Save`（与 Top-1 相似度 ≥ 0.92 判重跳过）、`Search`、`DeleteBy*`、`FormatMemories`（system prompt 记忆块）。内含单条 query 缓存，避免 ReAct 循环内重复调 embedding API。
- **`modifier.go`** — 自定义 `react.MessageModifier`（`NewPersonaModifier` 已废弃，勿再用）：以最后一条 user 消息为 query 检索记忆，与 persona 合成一条 system 消息。用 `systemMarker` 前缀做**幂等替换**——ReAct 每次模型调用都会触发 modifier，没有标记会无限叠加。
- **`extractor.go`** — 每轮对话后自动抽取用户事实：LLM 返回 JSON 字符串数组，`cleanJSON` 容忍 markdown 围栏/前后缀。失败只 log 不打断对话。
- **`shortterm.go`** — `ShortTerm` 滑动窗口：超窗把「旧摘要+最老消息」滚动压缩为一条摘要 system 消息（前缀 `[历史对话摘要]` 识别），摘要失败退化为截断。
- **`tools.go`** — Agent 记忆工具：`save_memory` / `search_memories` / `delete_memory`。

### 新增工具的流程

1. 在 `internal/tools/`（基础工具）或 `internal/memory/tools.go`（记忆工具）定义入参结构体（字段带 `json` + `jsonschema` tag）和实现函数，用 `utils.InferTool` 包成 `tool.InvokableTool`；
2. 在 `cmd/mindagent/main.go` 的 `buildAgent` 中加入 `ToolsConfig.Tools`；
3. 工具描述要写清"何时使用"，模型据此决定是否调用——描述质量直接决定 ReAct 工具选择的准确度。

### Eino API 注意事项

- 依赖：`github.com/cloudwego/eino` v0.9.21 + `eino-ext/components/model/openai` + `eino-ext/components/embedding/openai`。ReAct Agent 在 `eino/flow/agent/react` 包。
- `openaimodel.ChatModelConfig.Temperature` 是 `*float32`（指针）。
- `react.AgentConfig` 用 `ToolCallingModel` 字段，`Model` 字段已废弃。
- `model.ChatModel` 与 `model.ToolCallingChatModel` 是**两个不互通的接口**；memory 包定义了最小接口 `memory.ChatModel`（仅需 `Generate`），摘要/抽取复用主模型实例。
- `schema.Document` 的向量通过 `WithDenseVector()` 存取（`doc.Vector` 字段已不存在），得分用 `WithScore()`。
