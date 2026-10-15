// mindAgent 是一个基于字节 Eino 框架 ReAct 机制的命令行 AI Agent。
//
// 启动后进入多轮对话 REPL，Agent 会在“思考 → 调用工具 → 观察 → 再思考”的
// ReAct 循环中自动选择并调用内置工具（当前时间、计算器），直到给出最终回答。
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"

	"mindAgent/internal/config"
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

	agent, err := buildAgent(ctx, cfg)
	if err != nil {
		return fmt.Errorf("初始化 Agent 失败: %w", err)
	}

	printBanner(cfg)

	// 对话历史：ReAct Agent 本身无状态，由调用方维护多轮上下文。
	messages := make([]*schema.Message, 0, 16)
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for {
		fmt.Print("\n你> ")
		if !scanner.Scan() {
			break // EOF / Ctrl-D
		}
		input := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(input) {
		case "":
			continue
		case "exit", "quit", "q", "退出":
			fmt.Println("再见！")
			return nil
		case "reset", "清空":
			messages = messages[:0]
			fmt.Println("（对话已清空）")
			continue
		}

		messages = append(messages, &schema.Message{
			Role:    schema.User,
			Content: input,
		})

		fmt.Print("Agent> ")
		reply, err := agent.Generate(ctx, messages)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n生成失败: %v\n", err)
			// 失败的这轮不保留，避免坏消息污染后续上下文。
			messages = messages[:len(messages)-1]
			continue
		}

		fmt.Println(reply.Content)
		messages = append(messages, reply)
	}
	return scanner.Err()
}

// buildAgent 组装 ReAct Agent：OpenAI 兼容 ChatModel + 内置工具。
func buildAgent(ctx context.Context, cfg *config.Config) (*react.Agent, error) {
	// 1. ChatModel：任何 OpenAI 兼容服务（OpenAI / 火山方舟豆包 / DeepSeek 等）均可。
	temperature := float32(cfg.Temperature)
	cm, err := openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{
		APIKey:      cfg.APIKey,
		BaseURL:     cfg.BaseURL,
		Model:       cfg.Model,
		Temperature: &temperature,
	})
	if err != nil {
		return nil, fmt.Errorf("创建 ChatModel 失败: %w", err)
	}

	// 2. 工具：InferTool 从 Go 函数签名自动推导 JSON Schema。
	timeTool, err := tools.CurrentTime()
	if err != nil {
		return nil, err
	}
	calcTool, err := tools.Calculator()
	if err != nil {
		return nil, err
	}

	// 3. ReAct Agent：模型输出工具调用则执行工具，将结果回填后继续推理，直到不再调用工具。
	return react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: cm,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: []tool.BaseTool{timeTool, calcTool},
		},
		MessageModifier: react.NewPersonaModifier(
			"你是 mindAgent，一个乐于助人的中文 AI 助手。" +
				"遇到需要精确计算或实时信息的问题时，优先使用工具而不是猜测。" +
				"回答保持简洁。",
		),
		MaxStep: cfg.MaxStep,
	})
}

func printBanner(cfg *config.Config) {
	fmt.Println("========================================")
	fmt.Println("  mindAgent — Eino ReAct Agent")
	fmt.Println("========================================")
	fmt.Printf("模型: %s (@ %s)\n", cfg.Model, cfg.BaseURL)
	fmt.Println("工具: get_current_time, calculator")
	fmt.Println("命令: reset 清空对话 | exit 退出")
}
