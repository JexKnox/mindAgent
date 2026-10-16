// mindAgent 是一个基于字节 Eino 框架 ReAct 机制的命令行 AI Agent。
//
// 启动后进入多轮对话 REPL，Agent 会在“思考 → 调用工具 → 观察 → 再思考”的
// ReAct 循环中自动选择并调用工具（当前时间、计算器、记忆管理），直到给出最终回答。
// 记忆系统：短期记忆为滑动窗口 + 溢出摘要压缩；长期记忆为向量库持久化，
// 通过 system prompt 相关性检索注入，并由模型工具 + 每轮自动抽取双通道写入。
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	openaiembed "github.com/cloudwego/eino-ext/components/embedding/openai"

	"mindAgent/internal/config"
	"mindAgent/internal/memory"
	"mindAgent/internal/tools"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()

	agent, mem, err := buildAgent(ctx, cfg)
	if err != nil {
		return fmt.Errorf("初始化 Agent 失败: %w", err)
	}
	chatModel, err := newChatModel(ctx, cfg)
	if err != nil {
		return err
	}
	extracter := memory.NewExtracter(chatModel, mem)
	shortTerm := memory.NewShortTerm(cfg.MemoryWindow, chatModel)

	printBanner(cfg, mem)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Print("\n你> ")
		if !scanner.Scan() {
			break // EOF / Ctrl-D
		}
		input := strings.TrimSpace(scanner.Text())
		if handled := handleCommand(input, shortTerm, mem); handled {
			continue
		}
		if input == "" {
			continue
		}

		shortTerm.Append(&schema.Message{Role: schema.User, Content: input})
		// 超窗的旧消息先压缩，再送入 Agent
		shortTerm.Compress(ctx)

		fmt.Print("Agent> ")
		reply, err := agent.Generate(ctx, shortTerm.Messages())
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n生成失败: %v\n", err)
			// 失败的这轮不保留，避免坏消息污染后续上下文。
			shortTerm.PopLast()
			continue
		}

		fmt.Println(reply.Content)
		shortTerm.Append(reply)

		// 每轮对话后自动抽取值得长期记住的事实（可配置关闭）
		if cfg.MemoryAutoExtract {
			extracter.Extract(ctx, input, reply.Content)
		}
	}
	return scanner.Err()
}

// handleCommand 处理 REPL 元命令，返回是否已消费该输入。
func handleCommand(input string, shortTerm *memory.ShortTerm, mem *memory.Service) bool {
	lower := strings.ToLower(input)
	switch {
	case lower == "":
		return true
	case lower == "exit" || lower == "quit" || lower == "q" || lower == "退出":
		fmt.Println("再见！")
		os.Exit(0)
	case lower == "reset" || lower == "清空":
		shortTerm.Reset()
		fmt.Println("（短期对话已清空，长期记忆保留；使用 memories 查看）")
		return true
	case lower == "memories" || lower == "记忆":
		listMemories(mem)
		return true
	case strings.HasPrefix(lower, "forget "):
		id := strings.TrimSpace(input[len("forget "):])
		ok, err := mem.DeleteByID(id)
		switch {
		case err != nil:
			fmt.Printf("删除失败: %v\n", err)
		case ok:
			fmt.Printf("已删除记忆 %s\n", id)
		default:
			fmt.Printf("未找到 ID 为 %s 的记忆\n", id)
		}
		return true
	}
	return false
}

// listMemories 列出全部长期记忆。
func listMemories(mem *memory.Service) {
	all := mem.All()
	if len(all) == 0 {
		fmt.Println("（长期记忆为空）")
		return
	}
	fmt.Printf("长期记忆共 %d 条：\n", len(all))
	for _, m := range all {
		fmt.Printf("  %s  %s\n", m.ID, m.Content)
	}
}

// newChatModel 创建主对话模型（摘要 / 记忆抽取也复用它）。
func newChatModel(ctx context.Context, cfg *config.Config) (model.ToolCallingChatModel, error) {
	temperature := float32(cfg.Temperature)
	return openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:      cfg.APIKey,
		BaseURL:     cfg.BaseURL,
		Model:       cfg.Model,
		Temperature: &temperature,
	})
}

// buildAgent 组装 ReAct Agent：ChatModel + 内置工具 + 记忆系统。
func buildAgent(ctx context.Context, cfg *config.Config) (*react.Agent, *memory.Service, error) {
	// 1. ChatModel：任何 OpenAI 兼容服务（OpenAI / 火山方舟豆包 / DeepSeek 等）均可。
	chatModel, err := newChatModel(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 ChatModel 失败: %w", err)
	}

	// 2. 长期记忆：OpenAI 兼容 Embedder + 嵌入式本地向量库。
	embedder, err := openaiembed.NewEmbedder(ctx, &openaiembed.EmbeddingConfig{
		APIKey: cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.EmbeddingModel,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("创建 Embedder 失败: %w", err)
	}
	store, err := memory.NewStore(cfg.MemoryFile)
	if err != nil {
		return nil, nil, err
	}
	mem := memory.NewService(store, embedder, memory.Config{
		TopK:         cfg.MemoryTopK,
		DupThreshold: 0.92,
	})

	// 3. 工具：内置工具 + 记忆管理工具（模型自主决定何时存取记忆）。
	basicTools, err := basicTools()
	if err != nil {
		return nil, nil, err
	}
	memTools, err := memory.Tools(mem)
	if err != nil {
		return nil, nil, err
	}
	allTools := append(basicTools, memTools...)

	// 4. ReAct Agent：persona 与相关长期记忆由 MessageModifier 动态注入 system prompt。
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: toolsToBase(allTools),
		},
		MessageModifier: memory.NewMemoryModifier(persona, mem),
		MaxStep:         cfg.MaxStep,
	})
	if err != nil {
		return nil, nil, err
	}
	return agent, mem, nil
}

const persona = "你是 mindAgent，一个乐于助人的中文 AI 助手，具备跨会话的长期记忆能力。" +
	"回答保持简洁。遇到需要精确计算或实时信息的问题时，优先使用工具而不是猜测。" +
	"当用户透露重要的个人信息或明确要求记住某事时，使用 save_memory 保存；" +
	"用户要求忘记某事时，先用 search_memories 确认再用 delete_memory 删除。"

// basicTools 返回非记忆类的内置工具。
func basicTools() ([]tool.InvokableTool, error) {
	timeTool, err := tools.CurrentTime()
	if err != nil {
		return nil, err
	}
	calcTool, err := tools.Calculator()
	if err != nil {
		return nil, err
	}
	return []tool.InvokableTool{timeTool, calcTool}, nil
}

// toolsToBase 将 InvokableTool 切片转为 ToolsConfig 需要的 BaseTool 切片。
func toolsToBase(invokable []tool.InvokableTool) []tool.BaseTool {
	base := make([]tool.BaseTool, 0, len(invokable))
	for _, t := range invokable {
		base = append(base, t)
	}
	return base
}

func printBanner(cfg *config.Config, mem *memory.Service) {
	fmt.Println("========================================")
	fmt.Println("  mindAgent — Eino ReAct Agent")
	fmt.Println("========================================")
	fmt.Printf("模型: %s (@ %s)\n", cfg.Model, cfg.BaseURL)
	fmt.Printf("记忆: %d 条长期记忆 | Top-%d 相关注入 | 短期窗口 %d 条\n",
		mem.AllCount(), cfg.MemoryTopK, cfg.MemoryWindow)
	fmt.Println("工具: get_current_time, calculator, save_memory, search_memories, delete_memory")
	fmt.Println("命令: reset 清空短期对话 | memories 查看长期记忆 | forget <id> 删除 | exit 退出")
}
