package kb

import (
	"context"
	"testing"
)

func TestCreateAndListCollections(t *testing.T) {
	svc, _ := testKB(t)
	ctx := context.Background()

	col, err := svc.CreateCollection(ctx, "tech", "技术文档")
	if err != nil {
		t.Fatalf("CreateCollection: %v", err)
	}
	if col.ID == 0 {
		t.Fatal("expected non-zero id")
	}

	if _, err := svc.CreateCollection(ctx, "tech", "dup"); err == nil {
		t.Fatal("expected duplicate name to fail")
	}

	cols, err := svc.ListCollections(ctx)
	if err != nil {
		t.Fatalf("ListCollections: %v", err)
	}
	if len(cols) != 1 || cols[0].Name != "tech" {
		t.Errorf("unexpected collections: %+v", cols)
	}
}

func TestIngestAndKeywordSearch(t *testing.T) {
	svc, _ := testKB(t)
	ctx := context.Background()

	col, _ := svc.CreateCollection(ctx, "notes", "")

	doc, err := svc.IngestDocument(ctx, col.ID, "Go 并发", "Go 语言的 goroutine 是轻量级线程。channel 用于协程之间的通信。sync.Mutex 提供互斥锁。", "text", "")
	if err != nil {
		t.Fatalf("IngestDocument: %v", err)
	}
	if doc.Status != "ready" {
		t.Fatalf("expected ready, got %s (error: %s)", doc.Status, doc.Error)
	}

	// 关键词检索：中文分词后应能命中
	resp, err := svc.Search(ctx, &SearchRequest{Query: "goroutine 轻量级", Mode: "keyword", TopK: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("keyword search returned no results")
	}
	if resp.Results[0].Title != "Go 并发" {
		t.Errorf("unexpected top result: %s", resp.Results[0].Title)
	}
}

func TestIngestDuplicate(t *testing.T) {
	svc, _ := testKB(t)
	ctx := context.Background()

	col, _ := svc.CreateCollection(ctx, "c1", "")
	content := "重复内容测试。这是一段用于验证内容哈希去重的文本。"

	d1, err := svc.IngestDocument(ctx, col.ID, "doc1", content, "text", "")
	if err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	d2, err := svc.IngestDocument(ctx, col.ID, "doc2", content, "text", "")
	if err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if d1.ID != d2.ID {
		t.Errorf("expected same document id for duplicate content, got %d and %d", d1.ID, d2.ID)
	}
}

func TestHybridSearch(t *testing.T) {
	svc, _ := testKB(t)
	ctx := context.Background()

	col, _ := svc.CreateCollection(ctx, "mixed", "")
	_, err := svc.IngestDocument(ctx, col.ID, "数据库", "SQLite 是嵌入式数据库。WAL 模式提供并发读。", "text", "")
	if err != nil {
		t.Fatalf("ingest 1: %v", err)
	}
	_, err = svc.IngestDocument(ctx, col.ID, "网络", "HTTP 是超文本传输协议。TCP 提供可靠传输。", "text", "")
	if err != nil {
		t.Fatalf("ingest 2: %v", err)
	}

	resp, err := svc.Search(ctx, &SearchRequest{Query: "WAL 并发", Mode: "hybrid", TopK: 5, ExpandContext: true})
	if err != nil {
		t.Fatalf("hybrid search: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("hybrid search returned nothing")
	}
	if resp.Results[0].Title != "数据库" {
		t.Errorf("expected 数据库 first, got %s", resp.Results[0].Title)
	}
}

func TestDeleteDocument(t *testing.T) {
	svc, _ := testKB(t)
	ctx := context.Background()

	col, _ := svc.CreateCollection(ctx, "del", "")
	doc, err := svc.IngestDocument(ctx, col.ID, "to delete", "待删除的内容。", "text", "")
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}

	if err := svc.DeleteDocument(ctx, doc.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.GetDocument(ctx, doc.ID); err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestRRFFusion(t *testing.T) {
	// 两个列表都含同一文档时，RRF 分数应高于单列表命中的
	a := []SearchResult{{Citation: Citation{ChunkID: 1}, Score: 1}}
	b := []SearchResult{{Citation: Citation{ChunkID: 1}, Score: 1},
		{Citation: Citation{ChunkID: 2}, Score: 0.5}}

	fused := fuseRRF([][]SearchResult{a, b}, 10)
	if len(fused) != 2 {
		t.Fatalf("expected 2 results, got %d", len(fused))
	}
	if fused[0].ChunkID != 1 {
		t.Errorf("chunk 1 should rank first (hit in both lists), got %d", fused[0].ChunkID)
	}
}

func TestBuildFTSQuery(t *testing.T) {
	q, err := BuildFTSQuery("SQLite 轻量级数据库")
	if err != nil {
		t.Fatalf("BuildFTSQuery: %v", err)
	}
	if q == "" {
		t.Fatal("expected non-empty match expression")
	}
	t.Logf("FTS query: %s", q)
}
