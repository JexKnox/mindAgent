// extractor.go 在每轮对话结束后，用一次轻量 LLM 调用自动抽取
// 值得长期记住的用户事实/偏好，经去重后写入向量库。
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Extracter 依赖一个 ChatModel 做事实抽取（可与主对话模型共用）。
type Extracter struct {
	model ChatModel
	svc   *Service
}

// NewExtracter 创建自动抽取器。
func NewExtracter(m ChatModel, svc *Service) *Extracter {
	return &Extracter{model: m, svc: svc}
}

const extractPrompt = `你是一个记忆抽取器。分析下面这轮对话，提取其中值得长期记住的「用户」事实或偏好。

只提取：
- 用户的姓名、身份、职业等个人信息
- 用户的偏好（喜欢/不喜欢什么）
- 用户提到的长期目标或计划
- 用户明确要求记住的内容

不要提取：
- 一次性的、临时的问题（如"现在几点"）
- 助手自己的回答内容
- 无法判断归属的泛泛内容

对话：
用户: %s
助手: %s

只输出一个 JSON 字符串数组，不要任何其他文字。没有值得记住的内容时输出 []。
示例: ["用户名叫James", "用户最喜欢的编程语言是Go"]`

// Extract 分析一轮对话并保存抽取结果。失败只记录日志，不影响主对话。
func (e *Extracter) Extract(ctx context.Context, userMsg, assistantReply string) {
	facts, err := e.parseFacts(ctx, userMsg, assistantReply)
	if err != nil {
		log.Printf("🧠 [记忆] 自动抽取失败（不影响对话）: %v", err)
		return
	}
	for _, fact := range facts {
		if _, err := e.svc.Save(ctx, fact); err != nil {
			log.Printf("🧠 [记忆] 保存抽取结果失败: %v", err)
		}
	}
}

// parseFacts 调用 LLM 并解析 JSON 数组结果。
func (e *Extracter) parseFacts(ctx context.Context, userMsg, assistantReply string) ([]string, error) {
	reply, err := e.model.Generate(ctx, []*schema.Message{
		schema.SystemMessage(fmt.Sprintf(extractPrompt, userMsg, assistantReply)),
	})
	if err != nil {
		return nil, fmt.Errorf("抽取请求失败: %w", err)
	}

	content := cleanJSON(reply.Content)
	var facts []string
	if err := json.Unmarshal([]byte(content), &facts); err != nil {
		// 模型偶尔会输出对象而非数组，忽略本次结果即可
		return nil, fmt.Errorf("解析抽取结果失败: %w（原始输出: %.100s）", err, reply.Content)
	}
	return facts, nil
}

// fenceRe 匹配 markdown 代码围栏 ```json ... ```。
var fenceRe = regexp.MustCompile("(?s)^```[a-zA-Z]*\\s*(.*?)\\s*```$")

// cleanJSON 去掉模型输出中常见的 markdown 围栏和首尾杂字符。
func cleanJSON(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		s = strings.TrimSpace(m[1])
	}
	// 截取第一个 [ 到最后一个 ]，容忍模型输出的前后缀解释文字
	if i, j := strings.Index(s, "["), strings.LastIndex(s, "]"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	return s
}
