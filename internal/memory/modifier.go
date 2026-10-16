// modifier.go 提供自定义的 react.MessageModifier：
// 每次模型调用前，用最新用户输入检索相关长期记忆，
// 与 persona 合成一条 system 消息幂等注入到消息列表最前。
package memory

import (
	"context"
	"log"
	"strings"

	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"
)

// systemMarker 用于识别我们自己注入的 system 消息，保证幂等替换：
// ReAct 循环内每次模型调用都会触发 modifier，没有标记会不断叠加记忆块。
const systemMarker = "[mindAgent-system]"

// NewMemoryModifier 返回 persona + 动态记忆注入的 MessageModifier。
//
// 等价于（且替代）已废弃的 react.NewPersonaModifier：每次模型调用时，
// 取消息列表中最后一条用户消息作为检索 query，将 Top-K 相关记忆
// 拼接到 system prompt。检索结果由 Service 内的 query 缓存兜底，
// 同一轮 ReAct 迭代不会重复调用 embedding API。
func NewMemoryModifier(persona string, svc *Service) react.MessageModifier {
	return func(ctx context.Context, input []*schema.Message) []*schema.Message {
		system := persona

		if query := lastUserQuery(input); query != "" {
			block, err := svc.FormatMemories(ctx, query)
			if err != nil {
				// 记忆检索失败不应阻断对话，仅提示
				log.Printf("🧠 [记忆] 检索失败（已跳过注入）: %v", err)
			} else {
				system += block
			}
		}

		res := make([]*schema.Message, 0, len(input)+1)
		res = append(res, &schema.Message{
			Role:    schema.System,
			Content: systemMarker + system,
		})

		// 跳过上一轮迭代注入的旧 system 消息，其余原样保留
		for _, m := range input {
			if m.Role == schema.System && strings.HasPrefix(m.Content, systemMarker) {
				continue
			}
			res = append(res, m)
		}
		return res
	}
}

// lastUserQuery 返回消息列表中最后一条用户消息的内容。
func lastUserQuery(msgs []*schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == schema.User {
			return msgs[i].Content
		}
	}
	return ""
}
