// vectorstore.go 实现一个嵌入式本地向量库：内存检索 + JSON 文件持久化。
// 同时实现 Eino 的 indexer.Indexer 和 retriever.Retriever 接口，
// 未来可无缝替换为 eino-ext 官方向量库组件（Redis/Qdrant/Milvus 等）。
package memory

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/cloudwego/eino/components/indexer"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/schema"
)

// record 是向量库中的一条记忆记录。
type record struct {
	ID        string    `json:"id"`
	Content   string    `json:"content"`
	Vector    []float64 `json:"vector"`
	CreatedAt int64     `json:"created_at"` // unix 秒
}

// Store 是嵌入式向量库，检索在内存中做余弦相似度暴力排序，
// 每次写入都同步刷盘（原子写：临时文件 + rename），进程重启后自动加载。
//
// 并发安全：Retrieve 允许并发读，写入全程持锁。
type Store struct {
	mu   sync.RWMutex
	path string
	data []record
}

// NewStore 打开（或创建）path 指定的向量库文件。目录不存在时自动创建。
func NewStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("创建记忆目录失败: %w", err)
	}
	s := &Store{path: path}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取记忆文件失败: %w", err)
	}
	if err := json.Unmarshal(raw, &s.data); err != nil {
		return nil, fmt.Errorf("记忆文件损坏: %w", err)
	}
	return s, nil
}

// Store 实现 indexer.Indexer：写入文档并返回分配的 ID。
// 向量已由调用方（Service）生成后放入 doc.Vector，本方法不调用 Embedder。
func (s *Store) Store(ctx context.Context, docs []*schema.Document, opts ...indexer.Option) ([]string, error) {
	_ = ctx
	_ = opts // 无 Index/SubIndexes 需求

	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, 0, len(docs))
	for _, doc := range docs {
		vec := doc.DenseVector()
		if len(vec) == 0 {
			return nil, fmt.Errorf("文档 %q 缺少向量，应由 Service 生成后再写入", doc.Content)
		}
		r := record{
			ID:        newID(),
			Content:   doc.Content,
			Vector:    append([]float64(nil), vec...),
			CreatedAt: nowUnix(),
		}
		s.data = append(s.data, r)
		ids = append(ids, r.ID)
	}
	if err := s.flushLocked(); err != nil {
		return nil, err
	}
	return ids, nil
}

// Retrieve 实现 retriever.Retriever：按向量余弦相似度返回 Top-K 文档。
// query 的向量通过 retriever.WithEmbedder 注入的 Embedder 生成；
// 未注入时返回错误（本库不内置 Embedder，保证索引/检索使用同一模型）。
func (s *Store) Retrieve(ctx context.Context, query string, opts ...retriever.Option) ([]*schema.Document, error) {
	options := retriever.GetCommonOptions(&retriever.Options{}, opts...)
	if options.Embedding == nil {
		return nil, fmt.Errorf("必须通过 retriever.WithEmbedder 提供与索引时相同的 Embedder")
	}
	if options.TopK == nil {
		topK := 3
		options.TopK = &topK
	}
	vecs, err := options.Embedding.EmbedStrings(ctx, []string{query})
	if err != nil {
		return nil, fmt.Errorf("生成查询向量失败: %w", err)
	}
	results := s.SearchByVector(vecs[0], *options.TopK)
	docs := make([]*schema.Document, 0, len(results))
	for _, r := range results {
		if options.ScoreThreshold != nil && r.Score < *options.ScoreThreshold {
			continue
		}
		docs = append(docs, (&schema.Document{
			ID:      r.ID,
			Content: r.Content,
		}).WithScore(r.Score))
	}
	return docs, nil
}

// SearchByVector 是核心检索：对 vec 做余弦相似度排序，返回 Top-K。
func (s *Store) SearchByVector(vec []float64, topK int) []SearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]SearchResult, 0, len(s.data))
	for _, r := range s.data {
		score := cosine(vec, r.Vector)
		if score <= 0 || math.IsNaN(score) {
			continue
		}
		results = append(results, SearchResult{
			ID:        r.ID,
			Content:   r.Content,
			CreatedAt: r.CreatedAt,
			Score:     score,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

// Top1 返回与 vec 最相似的一条记录（空库返回 nil）。
func (s *Store) Top1(vec []float64) *SearchResult {
	rs := s.SearchByVector(vec, 1)
	if len(rs) == 0 {
		return nil
	}
	return &rs[0]
}

// DeleteByID 按 ID 删除，返回是否删除成功。
func (s *Store) DeleteByID(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, r := range s.data {
		if r.ID == id {
			s.data = append(s.data[:i], s.data[i+1:]...)
			return true, s.flushLocked()
		}
	}
	return false, nil
}

// All 返回全部记忆（按写入顺序），供 CLI 列出。
func (s *Store) All() []SearchResult {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]SearchResult, 0, len(s.data))
	for _, r := range s.data {
		out = append(out, SearchResult{ID: r.ID, Content: r.Content, CreatedAt: r.CreatedAt})
	}
	return out
}

// Len 返回记忆条数。
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.data)
}

// flushLocked 将内存数据原子写入磁盘（调用方须持写锁）。
func (s *Store) flushLocked() error {
	raw, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return fmt.Errorf("写入记忆文件失败: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("替换记忆文件失败: %w", err)
	}
	return nil
}

// cosine 计算两个向量的余弦相似度（假设维度一致，长度不同返回 NaN）。
func cosine(a, b []float64) float64 {
	if len(a) != len(b) {
		return math.NaN()
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// newID 生成 16 位随机十六进制 ID。
func newID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand 失败极罕见，退化为时间戳保证唯一性可用
		return fmt.Sprintf("t%d", nowUnix())
	}
	return hex.EncodeToString(b[:])
}

// 编译期接口实现检查。
var (
	_ indexer.Indexer    = (*Store)(nil)
	_ retriever.Retriever = (*Store)(nil)
)
