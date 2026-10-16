// memory.go 是记忆系统的服务层：组合向量库与 Embedder，
// 提供去重写入、Top-K 检索、删除和 system prompt 记忆块格式化。
package memory

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// nowUnix 可在测试中被替换。
var nowUnix = func() int64 { return time.Now().Unix() }

// ChatModel 是记忆系统（摘要 / 抽取）所需的最小模型接口：
// 只需单次非流式生成，便于测试 mock。
type ChatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
}

// SearchResult 是一条带相关度得分的记忆。
type SearchResult struct {
	ID        string  `json:"id"`
	Content   string  `json:"content"`
	CreatedAt int64   `json:"created_at"`
	Score     float64 `json:"score,omitempty"`
}

// Memory 是对外暴露的记忆条目（工具返回 / CLI 展示用）。
type Memory = SearchResult

// Config 是 Service 的配置项。
type Config struct {
	TopK       int     // 每轮注入 / 搜索返回的记忆条数
	DupThreshold float64 // 与已有记忆相似度超过该值视为重复，跳过写入
}

// DefaultConfig 返回默认配置。
func DefaultConfig() Config {
	return Config{TopK: 5, DupThreshold: 0.92}
}

// Service 是长期记忆服务。Save/Search/Delete 是核心入口。
type Service struct {
	store    *Store
	embedder embedding.Embedder
	cfg      Config

	// 查询缓存：ReAct 循环内每次模型调用都会触发 MessageModifier 重新注入，
	// 同一 query 的检索结果缓存在这里，避免重复调用 embedding API。
	// 只保留最近一条（对话是串行的），足够覆盖单轮 ReAct 迭代。
	cacheMu   sync.Mutex
	cachedFor string
	cachedRes []SearchResult
}

// NewService 创建记忆服务。embedder 必须与所有写入/检索共用（维度一致）。
func NewService(store *Store, embedder embedding.Embedder, cfg Config) *Service {
	if cfg.TopK <= 0 {
		cfg = DefaultConfig()
	}
	return &Service{store: store, embedder: embedder, cfg: cfg}
}

// Save 写入一条长期记忆：先向量化，与库内 Top-1 相似度超过阈值则判重跳过。
// 返回 (是否真正写入, 错误)。
func (s *Service) Save(ctx context.Context, content string) (bool, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return false, fmt.Errorf("记忆内容为空")
	}

	vec, err := s.embedOne(ctx, content)
	if err != nil {
		return false, err
	}

	if dup := s.store.Top1(vec); dup != nil && dup.Score >= s.cfg.DupThreshold {
		log.Printf("🧠 [记忆] 跳过重复内容（与 %s 相似度 %.3f）: %s", shortID(dup.ID), dup.Score, content)
		return false, nil
	}

	_, err = s.store.Store(ctx, []*schema.Document{
		(&schema.Document{Content: content}).WithDenseVector(vec),
	})
	if err != nil {
		return false, err
	}
	log.Printf("🧠 [记忆] 已保存: %s", content)
	return true, nil
}

// Search 以 query 检索最相关的 Top-K 条记忆。
func (s *Service) Search(ctx context.Context, query string, topK int) ([]SearchResult, error) {
	if topK <= 0 {
		topK = s.cfg.TopK
	}
	vec, err := s.embedOne(ctx, query)
	if err != nil {
		return nil, err
	}
	return s.store.SearchByVector(vec, topK), nil
}

// DeleteByID 按 ID 删除记忆。
func (s *Service) DeleteByID(id string) (bool, error) {
	return s.store.DeleteByID(id)
}

// DeleteByContent 按内容删除：找到与 content 最相似的一条（需超过去重阈值，
// 防止误删），删除之。返回 (是否删除, 错误)。
func (s *Service) DeleteByContent(ctx context.Context, content string) (bool, error) {
	vec, err := s.embedOne(ctx, content)
	if err != nil {
		return false, err
	}
	top := s.store.Top1(vec)
	if top == nil || top.Score < s.cfg.DupThreshold {
		return false, nil
	}
	return s.store.DeleteByID(top.ID)
}

// All 返回全部记忆（CLI `memories` 命令用）。
func (s *Service) All() []Memory { return s.store.All() }

// AllCount 返回记忆总条数（启动横幅用）。
func (s *Service) AllCount() int { return s.store.Len() }

// FormatMemories 检索与 query 相关的记忆并格式化为 system prompt 记忆块。
// 结果按 query 缓存（ReAct 每次模型调用都会触发注入）。
// 没有相关记忆时返回空字符串。
func (s *Service) FormatMemories(ctx context.Context, query string) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", nil
	}

	if cached, ok := s.cached(query); ok {
		return renderMemories(cached), nil
	}

	results, err := s.Search(ctx, query, s.cfg.TopK)
	if err != nil {
		return "", err
	}
	s.cache(query, results)
	return renderMemories(results), nil
}

func renderMemories(results []SearchResult) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## 长期记忆（与当前话题相关的历史记忆，供参考）\n")
	for i, r := range results {
		fmt.Fprintf(&b, "%d. %s（%s）\n", i+1, r.Content, time.Unix(r.CreatedAt, 0).Format("2006-01-02"))
	}
	return b.String()
}

func (s *Service) cached(query string) ([]SearchResult, bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.cachedFor == query {
		return s.cachedRes, true
	}
	return nil, false
}

func (s *Service) cache(query string, results []SearchResult) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.cachedFor = query
	s.cachedRes = results
}

func (s *Service) embedOne(ctx context.Context, text string) ([]float64, error) {
	vecs, err := s.embedder.EmbedStrings(ctx, []string{text})
	if err != nil {
		return nil, fmt.Errorf("向量化失败: %w", err)
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("向量化返回 %d 个向量，期望 1 个", len(vecs))
	}
	return vecs[0], nil
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}
