package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// fakeEmbedder 是固定映射的假 Embedder：每个词一个维度，出现的词该维为 1。
// "我叫James" -> [1,0,0]，"我喜欢Go" -> [0,1,0]，以此类推。
type fakeEmbedder struct {
	dict map[string]int
	next int
}

func (f *fakeEmbedder) EmbedStrings(ctx context.Context, texts []string, _ ...embedding.Option) ([][]float64, error) {
	out := make([][]float64, 0, len(texts))
	for _, t := range texts {
		vec := make([]float64, len(f.dict))
		for w, i := range f.dict {
			if strings.Contains(t, w) {
				vec[i] = 1
			}
		}
		out = append(out, vec)
	}
	return out, nil
}

func newFakeEmbedder() *fakeEmbedder {
	return &fakeEmbedder{dict: map[string]int{
		"James": 0, "Go": 1, "猫": 2, "狗": 3, "咖啡": 4,
	}}
}

// mockChatModel 是可编程的假 ChatModel，按序返回预设回复。
type mockChatModel struct {
	replies []string
	calls   int
	err     error
}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.calls >= len(m.replies) {
		return nil, errors.New("mockChatModel: 没有更多预设回复")
	}
	r := m.replies[m.calls]
	m.calls++
	return &schema.Message{Role: schema.Assistant, Content: r}, nil
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "memories.json"))
	if err != nil {
		t.Fatal(err)
	}
	return NewService(store, newFakeEmbedder(), DefaultConfig())
}

func TestServiceSaveAndSearch(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	for _, c := range []string{"用户名叫James", "用户喜欢Go语言", "用户养了一只猫", "用户爱喝咖啡"} {
		if _, err := svc.Save(ctx, c); err != nil {
			t.Fatalf("Save(%q) 报错: %v", c, err)
		}
	}

	// 检索应按相关性排序：Go 相关查询的第一条应是 Go 那条
	results, err := svc.Search(ctx, "用户用什么编程语言？Go", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || !strings.Contains(results[0].Content, "Go") {
		t.Errorf("Search 期望第一条含 'Go'，实际: %+v", results)
	}

	// 猫/狗 只差一个维度，猫应排前面
	results, err = svc.Search(ctx, "用户的宠物是猫", 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || !strings.Contains(results[0].Content, "猫") {
		t.Errorf("Search 期望第一条含 '猫'，实际: %+v", results)
	}
}

func TestServiceDedup(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if saved, _ := svc.Save(ctx, "用户名叫James"); !saved {
		t.Fatal("首次保存应成功")
	}
	// 与已有记忆仅差一个维度（相似度很高），应判重跳过
	saved, err := svc.Save(ctx, "用户名叫James 呀")
	if err != nil {
		t.Fatal(err)
	}
	if saved {
		t.Error("相似内容应被去重跳过")
	}
	if got := svc.AllCount(); got != 1 {
		t.Errorf("去重后应只有 1 条，实际 %d 条", got)
	}
}

func TestDeleteByContent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	svc.Save(ctx, "用户名叫James")
	svc.Save(ctx, "用户爱喝咖啡")

	ok, err := svc.DeleteByContent(ctx, "用户名叫James")
	if err != nil || !ok {
		t.Fatalf("DeleteByContent 失败: ok=%v err=%v", ok, err)
	}
	for _, m := range svc.All() {
		if strings.Contains(m.Content, "James") {
			t.Errorf("删除后仍存在: %s", m.Content)
		}
	}

	// 不匹配的内容不应删除任何东西
	ok, _ = svc.DeleteByContent(ctx, "完全无关的内容")
	if ok {
		t.Error("不匹配的内容不应被删除")
	}
}

func TestStorePersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "memories.json")

	// 写入后重开，数据应还在
	s1, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	doc := (&schema.Document{Content: "用户名叫James"}).WithDenseVector([]float64{1, 0, 0})
	if _, err := s1.Store(context.Background(), []*schema.Document{doc}); err != nil {
		t.Fatal(err)
	}

	s2, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Len() != 1 {
		t.Fatalf("重开后应恢复 1 条记忆，实际 %d", s2.Len())
	}
	rs := s2.SearchByVector([]float64{1, 0, 0}, 1)
	if len(rs) != 1 || rs[0].Content != "用户名叫James" {
		t.Errorf("恢复后的检索结果不符: %+v", rs)
	}

	// 删除后重开，应持久化删除
	id := s2.All()[0].ID
	if ok, _ := s2.DeleteByID(id); !ok {
		t.Fatal("DeleteByID 应成功")
	}
	s3, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if s3.Len() != 0 {
		t.Errorf("删除应已持久化，实际还有 %d 条", s3.Len())
	}
}

func TestCleanJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{`["a","b"]`, `["a","b"]`},
		{"```json\n[\"a\"]\n```", `["a"]`},
		{"好的，结果如下：\n[\"a\", \"b\"]", `["a", "b"]`},
		{"[]", "[]"},
		{"没有值得记住的内容", "没有值得记住的内容"}, // 无数组时原样返回，由调用方判定解析失败
	}
	for _, c := range cases {
		if got := cleanJSON(c.in); got != c.want {
			t.Errorf("cleanJSON(%q) = %q, 期望 %q", c.in, got, c.want)
		}
	}
}

func TestExtracter(t *testing.T) {
	svc := newTestService(t)
	m := &mockChatModel{replies: []string{
		"```json\n[\"用户名叫James\", \"用户喜欢Go\"]\n```",
	}}
	e := NewExtracter(m, svc)
	e.Extract(context.Background(), "我叫James，最喜欢Go", "你好James！")

	if got := svc.AllCount(); got != 2 {
		t.Fatalf("抽取应保存 2 条，实际 %d", got)
	}
}

func TestExtracterParseFailureNoop(t *testing.T) {
	svc := newTestService(t)
	m := &mockChatModel{replies: []string{"我觉得没什么好记的"}}
	e := NewExtracter(m, svc)
	e.Extract(context.Background(), "随便聊聊", "好的") // 解析失败只记日志，不应 panic
	if got := svc.AllCount(); got != 0 {
		t.Errorf("解析失败不应写入记忆，实际 %d 条", got)
	}
}

func TestShortTermCompress(t *testing.T) {
	m := &mockChatModel{replies: []string{"这是滚动摘要"}}
	st := NewShortTerm(5, m) // 窗口 5 条

	for i := 0; i < 8; i++ {
		st.Append(&schema.Message{Role: schema.User, Content: fmt.Sprintf("消息%d", i)})
	}

	st.Compress(context.Background())

	msgs := st.Messages()
	if len(msgs) != 5 {
		t.Fatalf("压缩后应保留 5 条（摘要+4条），实际 %d", len(msgs))
	}
	if msgs[0].Role != schema.System || !strings.HasPrefix(msgs[0].Content, summaryPrefix) {
		t.Errorf("第一条应为摘要消息，实际: %+v", msgs[0])
	}
	if msgs[1].Content != "消息4" {
		t.Errorf("保留的第一条应为消息4，实际 %s", msgs[1].Content)
	}
}

func TestShortTermFallbackOnSummaryFailure(t *testing.T) {
	m := &mockChatModel{err: errors.New("embedding 服务不可用")}
	st := NewShortTerm(5, m)

	for i := 0; i < 8; i++ {
		st.Append(&schema.Message{Role: schema.User, Content: fmt.Sprintf("消息%d", i)})
	}

	st.Compress(context.Background()) // 摘要失败 → 退化为截断

	msgs := st.Messages()
	if len(msgs) != 5 {
		t.Fatalf("摘要失败应退化为截断保留 5 条，实际 %d", len(msgs))
	}
	if msgs[0].Content != "消息3" {
		t.Errorf("截断后第一条应为消息3，实际 %s", msgs[0].Content)
	}
}

func TestMemoryModifierIdempotent(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()
	svc.Save(ctx, "用户名叫James")

	mod := NewMemoryModifier("你是助手", svc)
	input := []*schema.Message{
		{Role: schema.User, Content: "我是谁？James"},
		{Role: schema.Assistant, Content: "你是James"},
		{Role: schema.User, Content: "我叫什么名字？James"},
	}

	// 模拟 ReAct 循环：modifier 被连续调用两次，system 消息不能叠加
	out1 := mod(ctx, input)
	out2 := mod(ctx, out1)

	if len(out1) != len(out2) {
		t.Fatalf("幂等性破坏：第一次 %d 条，第二次 %d 条", len(out1), len(out2))
	}
	if out2[0].Role != schema.System || !strings.HasPrefix(out2[0].Content, systemMarker) {
		t.Errorf("第一条应为我们注入的 system 消息，实际: %+v", out2[0])
	}
	if !strings.Contains(out2[0].Content, "James") {
		t.Errorf("system 消息应包含检索到的相关记忆，实际: %s", out2[0].Content)
	}
	// 除 system 外其余消息应原样保留
	if len(out2) != len(input)+1 {
		t.Errorf("应注入 1 条 system + 原有 %d 条，实际 %d", len(input), len(out2))
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
