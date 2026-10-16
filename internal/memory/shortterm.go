// shortterm.go 实现短期记忆：滑动窗口管理当前会话的多轮消息历史，
// 超过窗口时把最老的消息用 LLM 压缩成一条「之前的对话摘要」，
// 保证上下文长度有界的同时尽量保留早期对话的要点。
package memory

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// summaryPrefix 短期记忆摘要消息的内容前缀，用于识别摘要消息。
const summaryPrefix = "[历史对话摘要] "

// ShortTerm 维护当前会话的消息列表，超窗自动压缩。
// 非并发安全：REPL 是串行的，无需加锁。
type ShortTerm struct {
	window int         // 窗口大小：保留的最近消息条数（不含摘要消息）
	model  ChatModel   // 摘要用的 ChatModel（可与主模型共用）
	msgs   []*schema.Message
}

// NewShortTerm 创建短期记忆管理器。window <= 0 时使用默认值 40。
func NewShortTerm(window int, m ChatModel) *ShortTerm {
	if window <= 0 {
		window = 40
	}
	return &ShortTerm{window: window, model: m}
}

// Append 追加一条消息。
func (s *ShortTerm) Append(m *schema.Message) {
	s.msgs = append(s.msgs, m)
}

// Messages 返回当前应传给 agent.Generate 的消息列表。
func (s *ShortTerm) Messages() []*schema.Message {
	return s.msgs
}

// Reset 清空当前会话的短期记忆（长期记忆不受影响）。
func (s *ShortTerm) Reset() {
	s.msgs = nil
}

// PopLast 移除最后一条消息（生成失败的轮次调用，避免污染上下文）。
func (s *ShortTerm) PopLast() {
	if len(s.msgs) > 0 {
		s.msgs = s.msgs[:len(s.msgs)-1]
	}
}

// Compress 在消息数超过 window 时，将最老的部分压缩为一条摘要消息。
// 压缩后的不变式：总条数 ≤ window = 摘要 1 条 + 最近 window-1 条原始消息。
// 摘要失败时退化为直接丢弃最老消息（宁可丢历史也不阻塞对话）。
// 每轮 Generate 之前调用。
func (s *ShortTerm) Compress(ctx context.Context) {
	if len(s.msgs) <= s.window {
		return
	}
	keep := s.window - 1 // 摘要自身占 1 个位置
	if keep < 1 {
		keep = 1
	}

	// 旧摘要也参与滚动压缩：把「旧摘要 + 最老消息」一起重新摘要
	var oldSummary *schema.Message
	start := 0
	if len(s.msgs) > 0 && isSummary(s.msgs[0]) {
		oldSummary = s.msgs[0]
		start = 1
	}

	// 需要折叠进摘要的消息数（至少 1 条）
	foldCount := len(s.msgs) - start - keep
	if foldCount < 1 {
		return
	}
	toSummarize := s.msgs[start : start+foldCount]

	summary := s.summarize(ctx, oldSummary, toSummarize)
	if summary == nil {
		// 退化为截断：没有摘要占位，保留最近 window 条
		log.Printf("🧠 [短期记忆] 摘要失败，直接截断最老的 %d 条消息", len(s.msgs)-s.window)
		s.msgs = append([]*schema.Message(nil), s.msgs[len(s.msgs)-s.window:]...)
		return
	}

	rest := append([]*schema.Message(nil), s.msgs[start+foldCount:]...)
	s.msgs = append([]*schema.Message{summary}, rest...)
}

// isSummary 判断消息是否为我们生成的历史摘要消息。
func isSummary(m *schema.Message) bool {
	return m != nil && m.Role == schema.System && strings.HasPrefix(m.Content, summaryPrefix)
}

const summarizePrompt = `把下面的对话历史压缩成一段简洁的摘要，保留：
- 用户透露的关键信息（姓名、偏好、目标）
- 已完成的任务和结论
- 尚未解决的问题
用第三人称陈述，直接输出摘要内容，不要任何前缀和解释。控制在 200 字以内。

%s
对话：
%s`

// summarize 调用 LLM 生成滚动摘要，失败返回 nil。
func (s *ShortTerm) summarize(ctx context.Context, oldSummary *schema.Message, msgs []*schema.Message) *schema.Message {
	if len(msgs) == 0 && oldSummary == nil {
		return nil
	}

	prev := "（无）"
	if oldSummary != nil {
		prev = oldSummary.Content[len(summaryPrefix):]
	}
	var dialogue string
	for _, m := range msgs {
		role := "用户"
		if m.Role == schema.Assistant {
			role = "助手"
		} else if m.Role == schema.Tool {
			role = "工具结果"
		}
		dialogue += fmt.Sprintf("%s: %s\n", role, m.Content)
	}

	reply, err := s.model.Generate(ctx, []*schema.Message{
		schema.UserMessage(fmt.Sprintf(summarizePrompt, prev, dialogue)),
	})
	if err != nil {
		log.Printf("🧠 [短期记忆] 摘要请求失败: %v", err)
		return nil
	}
	return &schema.Message{
		Role:    schema.System,
		Content: summaryPrefix + reply.Content,
	}
}
