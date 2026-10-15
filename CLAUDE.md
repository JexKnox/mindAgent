# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

mindAgent 是一个基于字节 [Eino](https://github.com/cloudwego/eino) 框架 ReAct 机制的命令行 AI Agent（Go）。Agent 在「模型 → 工具调用 → 结果回填 → 再推理」的循环中自动选择工具，直到模型不再发起工具调用；CLI 侧维护多轮对话历史。代码与注释均使用中文。

## Commands

```bash
make run     # 运行 Agent REPL（go run ./cmd/mindagent）
make build   # 编译到 bin/mindagent
make test    # 运行全部测试
make vet     # go vet

# 运行单个包的测试
go test ./internal/tools/

# 运行单个测试函数
go test ./internal/tools/ -run TestEvalArith
```

运行 Agent 需要配置（复制 `.env.example` 为 `.env`）：`LLM_API_KEY`（必填）、`LLM_BASE_URL`（默认 OpenAI，火山方舟用 `https://ark.cn-beijing.volces.com/api/v3`）、`LLM_MODEL`（默认 `gpt-4o-mini`）。端到端对话测试必须有真实可用的 API Key。

## Architecture

三段式结构，数据流：`main.go` 加载配置 → 组装 Agent → REPL 循环调用 `agent.Generate(ctx, messages)`。

- **`cmd/mindagent/main.go`** — 所有 Eino 组件的组装点：`openaimodel.NewChatModel`（任何 OpenAI 兼容服务）+ `react.NewAgent`（`ToolCallingModel` + `ToolsConfig` + `NewPersonaModifier` 系统人设 + `MaxStep` 防死循环）。Agent 本身无状态，多轮上下文由 REPL 侧的 `messages` 切片维护；生成失败的轮次会从历史中移除以免污染上下文。
- **`internal/tools/`** — 内置工具，用 `utils.InferTool(name, desc, fn)` 从 Go 函数签名自动推导 JSON Schema：入参是带 `jsonschema:"description=..."` tag 的结构体。工具函数内部通过 `trace()` 打印调用轨迹（`🔧 [工具] ...`），这是 CLI 里观察 ReAct 循环的主要手段。`calculator` 用 `go/parser` 解析 AST 求值，只允许纯算术节点（安全约束，勿放宽）。
- **`internal/config/`** — 零依赖的 `.env` 解析器 + 环境变量读取。已存在的环境变量优先于 `.env` 中的值。

### 新增工具的流程

1. 在 `internal/tools/` 定义入参结构体（字段带 `json` + `jsonschema` tag）和实现函数，用 `utils.InferTool` 包成 `tool.InvokableTool`；
2. 在 `cmd/mindagent/main.go` 的 `buildAgent` 中加入 `ToolsConfig.Tools`；
3. 工具描述要写清"何时使用"，模型据此决定是否调用——描述质量直接决定 ReAct 工具选择的准确度。

### Eino API 注意事项

- 依赖：`github.com/cloudwego/eino` v0.9.21 + `eino-ext/components/model/openai`。ReAct Agent 在 `eino/flow/agent/react` 包。
- `openaimodel.ChatModelConfig.Temperature` 是 `*float32`（指针）。
- `react.AgentConfig` 用 `ToolCallingModel` 字段，`Model` 字段已废弃。
