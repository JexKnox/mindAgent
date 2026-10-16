// tools.go 提供暴露给 ReAct Agent 的记忆管理工具，
// 模型在对话中自主决定何时保存/检索/删除长期记忆。
package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/components/tool/utils"
)

// trace 打印工具调用轨迹，与 internal/tools 包保持一致的观察方式。
func trace(format string, args ...any) {
	fmt.Printf("🔧 [工具] "+format+"\n", args...)
}

// SaveInput 是 save_memory 工具的入参。
type SaveInput struct {
	// Content 要长期记住的内容，建议为一条独立、自包含的事实
	Content string `json:"content" jsonschema:"description=要长期记住的内容，写成一条独立完整的事实，例如：用户名叫James，最喜欢Go语言"`
}

// SaveTool 返回「保存记忆」工具。
func SaveTool(svc *Service) (tool.InvokableTool, error) {
	return utils.InferTool("save_memory", "将用户的重要信息（姓名、偏好、背景、目标等）保存到长期记忆，跨会话生效。当用户明确说'记住…'或透露值得长期记住的事实时使用",
		func(ctx context.Context, in SaveInput) (string, error) {
			trace("save_memory(%s)", in.Content)
			saved, err := svc.Save(ctx, in.Content)
			if err != nil {
				return "", err
			}
			if !saved {
				return "该内容与已有记忆重复，未保存", nil
			}
			return "已保存到长期记忆", nil
		})
}

// SearchInput 是 search_memories 工具的入参。
type SearchInput struct {
	Query string `json:"query" jsonschema:"description=检索关键词或话题，例如：用户的编程语言偏好"`
}

// SearchTool 返回「检索记忆」工具。
func SearchTool(svc *Service) (tool.InvokableTool, error) {
	return utils.InferTool("search_memories", "在长期记忆中检索与某话题相关的历史记忆。当你需要回忆用户的个人信息、历史偏好或之前提过的事实时使用",
		func(ctx context.Context, in SearchInput) (string, error) {
			trace("search_memories(%s)", in.Query)
			results, err := svc.Search(ctx, in.Query, svc.cfg.TopK)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return "没有找到相关记忆", nil
			}
			var b strings.Builder
			for i, r := range results {
				fmt.Fprintf(&b, "%d. [ID=%s] %s\n", i+1, r.ID, r.Content)
			}
			return b.String(), nil
		})
}

// DeleteInput 是 delete_memory 工具的入参。
type DeleteInput struct {
	// MemoryID 与 Content 二选一
	MemoryID string `json:"memory_id,omitempty" jsonschema:"description=要删除的记忆 ID（来自 search_memories 的结果），优先使用"`
	Content  string `json:"content,omitempty" jsonschema:"description=要删除的记忆内容（当没有 ID 时，按内容精确匹配）"`
}

// DeleteTool 返回「删除记忆」工具。
func DeleteTool(svc *Service) (tool.InvokableTool, error) {
	return utils.InferTool("delete_memory", "删除一条过时或错误的长期记忆。当用户要求忘记某事、或发现某条记忆已不再正确时使用；删除前应先用 search_memories 确认目标",
		func(ctx context.Context, in DeleteInput) (string, error) {
			trace("delete_memory(id=%s, content=%s)", in.MemoryID, in.Content)
			if in.MemoryID != "" {
				ok, err := svc.DeleteByID(in.MemoryID)
				if err != nil {
					return "", err
				}
				if !ok {
					return fmt.Sprintf("未找到 ID 为 %s 的记忆", in.MemoryID), nil
				}
				return "已删除", nil
			}
			if in.Content != "" {
				ok, err := svc.DeleteByContent(ctx, in.Content)
				if err != nil {
					return "", err
				}
				if !ok {
					return "未找到足够匹配的记忆", nil
				}
				return "已删除", nil
			}
			return "", fmt.Errorf("memory_id 与 content 必须提供其一")
		})
}

// Tools 返回全部记忆工具，直接并入 ToolsConfig.Tools。
func Tools(svc *Service) ([]tool.InvokableTool, error) {
	t1, err := SaveTool(svc)
	if err != nil {
		return nil, err
	}
	t2, err := SearchTool(svc)
	if err != nil {
		return nil, err
	}
	t3, err := DeleteTool(svc)
	if err != nil {
		return nil, err
	}
	return []tool.InvokableTool{t1, t2, t3}, nil
}
