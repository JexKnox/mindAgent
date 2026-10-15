# mindAgent

基于字节 [Eino](https://github.com/cloudwego/eino) 框架 ReAct 机制实现的命令行 AI Agent。

Agent 在「思考 → 调用工具 → 观察 → 再思考」的 ReAct 循环中自动选择工具完成任务，内置两个示例工具：`get_current_time`（当前时间）和 `calculator`（四则运算计算器）。

## 快速开始

```bash
# 1. 配置（支持任何 OpenAI 兼容服务：OpenAI / 火山方舟豆包 / DeepSeek 等）
cp .env.example .env
# 编辑 .env，填入 LLM_API_KEY，按需修改 LLM_BASE_URL / LLM_MODEL

# 2. 运行
make run        # 等价于 go run ./cmd/mindagent
```

进入 REPL 后直接对话即可，例如：

```
你> 现在几点了？再帮我算一下 (128*7+45)/3
```

工具调用会以 `🔧 [工具] ...` 打印到终端，可以直观看到 ReAct 的每一轮 Action。

REPL 命令：`reset` 清空对话历史，`exit` 退出。

## 开发

```bash
make build   # 编译到 bin/mindagent
make test    # 运行单元测试
make vet     # go vet
```

## 代码结构

```
cmd/mindagent/main.go    入口：组装 ChatModel + 工具 + ReAct Agent，REPL 交互循环
internal/config/         环境变量 / .env 配置加载（零依赖的 .env 解析）
internal/tools/          内置工具，用 eino 的 InferTool 从 Go 函数自动推导 JSON Schema
```

## 如何新增工具

在 `internal/tools` 里写一个带 JSON tag 入参结构体的函数，用 `utils.InferTool` 包一层，再在 `cmd/mindagent/main.go` 的 `ToolsConfig.Tools` 中注册即可——模型会自动看到新工具并按需调用。

## 环境变量

| 变量 | 必填 | 说明 | 默认值 |
|---|---|---|---|
| `LLM_API_KEY` | ✅ | LLM 服务鉴权 Key | - |
| `LLM_BASE_URL` | - | OpenAI 兼容 API 地址 | `https://api.openai.com/v1` |
| `LLM_MODEL` | - | 模型 ID | `gpt-4o-mini` |
