# mindAgent

基于字节 [Eino](https://github.com/cloudwego/eino) 框架 ReAct 机制实现的命令行 AI Agent，具备完整的记忆系统。

Agent 在「思考 → 调用工具 → 观察 → 再思考」的 ReAct 循环中自动选择工具完成任务，内置工具：`get_current_time`（当前时间）、`calculator`（四则运算计算器）和三个记忆管理工具。

## 记忆系统

**短期记忆**：当前会话的对话历史，滑动窗口管理（默认 40 条），超窗时自动用 LLM 将最老的消息滚动压缩为一条摘要，上下文长度有界且早期要点不丢。

**长期记忆**：跨会话持久化，Embedding + 本地向量库（`data/memories.json`）：

- **写入双通道**：① 模型通过 `save_memory` 工具自主保存（用户明确要求记住/透露重要事实时）；② 每轮对话后自动抽取器用一次轻量 LLM 调用提取值得记住的用户事实（可关）
- **检索注入**：每轮以用户输入为 query 做余弦相似度 Top-K 检索，相关记忆动态注入 system prompt
- **去重**：与已有记忆相似度 ≥ 0.92 自动跳过

验证记忆：告诉它「我叫 XX，我最喜欢 Go」，退出重启后问「我是谁？」

## 快速开始

```bash
# 1. 配置（支持任何 OpenAI 兼容服务：OpenAI / 火山方舟豆包 / DeepSeek 等）
cp .env.example .env
# 编辑 .env，填入 LLM_API_KEY，按需修改 LLM_BASE_URL / LLM_MODEL / LLM_EMBEDDING_MODEL

# 2. 运行
make run        # 等价于 go run ./cmd/mindagent
```

进入 REPL 后直接对话即可，例如：

```
你> 记住，我叫 James，我最喜欢的语言是 Go
你> 现在几点了？再帮我算一下 (128*7+45)/3
```

工具调用（`🔧 [工具] ...`）和记忆读写（`🧠 [记忆] ...`）会打印轨迹，可以直观看到 ReAct 每一轮 Action 和记忆系统的运作。

REPL 命令：`reset` 清空短期对话 | `memories` 查看全部长期记忆 | `forget <id>` 删除某条 | `exit` 退出

## 开发

```bash
make build   # 编译到 bin/mindagent
make test    # 运行单元测试
make vet     # go vet
```

## 代码结构

```
cmd/mindagent/main.go    入口：组装 ChatModel + Embedder + 工具 + ReAct Agent，REPL 交互循环
internal/config/         环境变量 / .env 配置加载（零依赖的 .env 解析）
internal/tools/          内置工具（时间/计算器），用 eino 的 InferTool 从 Go 函数自动推导 JSON Schema
internal/memory/         记忆系统（见下）
```

### internal/memory 结构

```
vectorstore.go   嵌入式本地向量库：实现 eino indexer.Indexer / retriever.Retriever 接口，
                 JSON 文件持久化 + 余弦相似度检索，可无缝替换为官方向量库组件
memory.go        服务层：去重写入 / Top-K 检索 / 删除 / system prompt 记忆块格式化 + query 缓存
tools.go         Agent 记忆工具：save_memory / search_memories / delete_memory
extractor.go     每轮对话后的自动事实抽取（LLM 返回 JSON 数组，容错解析）
modifier.go      react.MessageModifier：persona + 相关记忆幂等注入 system prompt
shortterm.go     短期记忆：滑动窗口 + 溢出滚动摘要，摘要失败退化为截断
```

## 如何新增工具

在 `internal/tools` 里写一个带 JSON tag 入参结构体的函数，用 `utils.InferTool` 包一层，再在 `cmd/mindagent/main.go` 的 `buildAgent` 中注册即可——模型会自动看到新工具并按需调用。

## 环境变量

| 变量 | 必填 | 说明 | 默认值 |
|---|---|---|---|
| `LLM_API_KEY` | ✅ | LLM 服务鉴权 Key | - |
| `LLM_BASE_URL` | - | OpenAI 兼容 API 地址 | `https://api.openai.com/v1` |
| `LLM_MODEL` | - | 对话模型 ID | `gpt-4o-mini` |
| `LLM_EMBEDDING_MODEL` | - | 记忆向量化模型 ID | `text-embedding-3-small` |
| `MEMORY_FILE` | - | 长期记忆持久化文件 | `./data/memories.json` |
| `MEMORY_TOP_K` | - | 每轮注入的相关记忆条数 | `5` |
| `MEMORY_AUTO_EXTRACT` | - | 每轮自动记忆抽取开关 | `true` |
| `MEMORY_WINDOW` | - | 短期记忆窗口（消息条数） | `40` |
