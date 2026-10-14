package kb

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/Black0Bag/wikisql/internal/llm"
	"github.com/Black0Bag/wikisql/internal/store"
)

// fakeEmbedder 返回确定性向量：文本长度决定首元素，可稳定验证检索排序。
type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 8)
		// 用文本哈希造确定性向量
		sum := float32(0)
		for _, r := range t {
			sum += float32(r%97) / 97.0
		}
		v[0] = sum
		v[1] = float32(len(t) % 13)
		out[i] = v
	}
	return out, nil
}

type fakeChat struct{}

func (f *fakeChat) Chat(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	return "根据 [1] 的内容回答。", nil
}

func testKB(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	s, err := store.New(filepath.Join(dir, "kb-test.db"), testLogger())
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	svc, err := New(s, &fakeEmbedder{}, nil, Options{ChunkMaxRunes: 100}, testLogger())
	if err != nil {
		t.Fatalf("kb.New: %v", err)
	}
	if err := svc.InitDimension(context.Background(), 8); err != nil {
		t.Fatalf("InitDimension: %v", err)
	}
	// InitDimension 后需重建 vec 表（New 时 dimension 未设，ensureVecTable 未建表）
	return svc, s
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}
