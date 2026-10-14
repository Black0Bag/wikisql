package kb

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Black0Bag/wikisql/internal/llm"
)

type SearchRequest struct {
	Query         string  `json:"query"`
	CollectionIDs []int64 `json:"collection_ids,omitempty"`
	TopK          int     `json:"top_k,omitempty"`
	Mode          string  `json:"mode,omitempty"`     // hybrid | keyword | semantic
	Rewrite       bool    `json:"rewrite,omitempty"`  // 增强档：LLM 查询改写
	Rerank        bool    `json:"rerank,omitempty"`   // 增强档：LLM 列表式重排
	ExpandContext bool    `json:"expand_context,omitempty"` // small-to-big：拼接相邻块
}

type Citation struct {
	Index      int    `json:"index"`
	ChunkID    int64  `json:"chunk_id"`
	DocumentID int64  `json:"document_id"`
	Title      string `json:"title"`
	Snippet    string `json:"snippet"`
}

type SearchResponse struct {
	Results []SearchResult `json:"results"`
}

type SearchResult struct {
	Citation
	Score float64 `json:"score"`
}

type ChatResponse struct {
	Answer    string     `json:"answer"`
	Citations []Citation `json:"citations"`
}

const rrfK = 60

func (svc *Service) Search(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	if strings.TrimSpace(req.Query) == "" {
		return nil, NewValidationError("query must not be empty")
	}
	if req.TopK <= 0 {
		req.TopK = 5
	}
	if req.Mode == "" {
		req.Mode = "hybrid"
	}
	if req.Mode != "hybrid" && req.Mode != "keyword" && req.Mode != "semantic" {
		return nil, NewValidationError("mode must be hybrid, keyword or semantic")
	}

	query := req.Query
	if req.Rewrite && svc.chat != nil {
		rewritten, err := svc.rewriteQuery(ctx, query)
		if err != nil {
			svc.logger.Warn("query rewrite failed, falling back to original", "err", err)
		} else if rewritten != "" {
			query = rewritten
		}
	}

	overfetch := req.TopK * 4
	if overfetch < 20 {
		overfetch = 20
	}

	var fused []SearchResult
	switch req.Mode {
	case "keyword":
		fused = svc.keywordSearch(ctx, query, req.CollectionIDs, overfetch)
	case "semantic":
		fused = svc.semanticSearch(ctx, query, req.CollectionIDs, overfetch)
	default:
		kw := svc.keywordSearch(ctx, query, req.CollectionIDs, overfetch)
		se := svc.semanticSearch(ctx, query, req.CollectionIDs, overfetch)
		fused = fuseRRF([][]SearchResult{kw, se}, overfetch)
	}

	if req.Rerank && svc.chat != nil && len(fused) > 1 {
		reranked, err := svc.rerank(ctx, query, fused, req.TopK)
		if err != nil {
			svc.logger.Warn("rerank failed, using fused order", "err", err)
		} else {
			fused = reranked
		}
	}

	if len(fused) > req.TopK {
		fused = fused[:req.TopK]
	}
	for i := range fused {
		fused[i].Citation.Index = i + 1
	}

	if req.ExpandContext {
		for i := range fused {
			fused[i].Snippet = svc.expandSnippet(ctx, fused[i].ChunkID)
		}
	}

	return &SearchResponse{Results: fused}, nil
}

func (svc *Service) keywordSearch(ctx context.Context, query string, collectionIDs []int64, limit int) []SearchResult {
	match, err := BuildFTSQuery(query)
	if err != nil || match == "" {
		return nil
	}

	q := `SELECT c.id, d.id, d.title, c.content, bm25(kb_chunks_fts) AS score
		  FROM kb_chunks_fts f
		  JOIN kb_chunks c ON c.id = f.rowid
		  JOIN kb_documents d ON d.id = c.document_id
		  WHERE kb_chunks_fts MATCH ?`
	args := []any{match}

	if len(collectionIDs) > 0 {
		placeholders := strings.Repeat("?,", len(collectionIDs))
		placeholders = placeholders[:len(placeholders)-1]
		q += fmt.Sprintf(" AND d.collection_id IN (%s)", placeholders)
		for _, id := range collectionIDs {
			args = append(args, id)
		}
	}
	q += " ORDER BY score LIMIT ?"
	args = append(args, limit)

	rows, err := svc.store.QueryContext(ctx, q, args...)
	if err != nil {
		svc.logger.Error("keyword search failed", "err", err)
		return nil
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		var bm25 float64
		if err := rows.Scan(&r.ChunkID, &r.DocumentID, &r.Title, &r.Snippet, &bm25); err != nil {
			return nil
		}
		r.Score = -bm25
		results = append(results, r)
	}
	return results
}

func (svc *Service) semanticSearch(ctx context.Context, query string, collectionIDs []int64, limit int) []SearchResult {
	if svc.embed == nil {
		return nil
	}
	vectors, err := svc.embed.Embed(ctx, []string{query})
	if err != nil {
		svc.logger.Error("query embedding failed", "err", err)
		return nil
	}

	q := `SELECT v.rowid, distance FROM kb_chunks_vec v WHERE embedding MATCH ? AND k = ?`
	args := []any{encodeVector(vectors[0]), limit}

	rows, err := svc.store.QueryContext(ctx, q, args...)
	if err != nil {
		svc.logger.Error("vector search failed", "err", err)
		return nil
	}

	type vecHit struct {
		chunkID  int64
		distance float64
	}
	var hits []vecHit
	for rows.Next() {
		var h vecHit
		if err := rows.Scan(&h.chunkID, &h.distance); err != nil {
			rows.Close()
			return nil
		}
		hits = append(hits, h)
	}
	rows.Close()

	var results []SearchResult
	for _, h := range hits {
		var docID int64
		var title, content string
		var collID int64
		err := svc.store.QueryRowContext(ctx,
			`SELECT d.id, d.title, c.content, d.collection_id
			 FROM kb_chunks c JOIN kb_documents d ON d.id = c.document_id
			 WHERE c.id = ?`, h.chunkID).Scan(&docID, &title, &content, &collID)
		if err != nil {
			continue
		}
		if len(collectionIDs) > 0 && !containsInt64(collectionIDs, collID) {
			continue
		}
		results = append(results, SearchResult{
			Citation: Citation{
				ChunkID:    h.chunkID,
				DocumentID: docID,
				Title:      title,
				Snippet:    content,
			},
			Score: 1.0 - h.distance,
		})
	}
	return results
}

func fuseRRF(lists [][]SearchResult, limit int) []SearchResult {
	type entry struct {
		result SearchResult
		score  float64
	}
	byID := make(map[int64]*entry)

	for _, list := range lists {
		for rank, r := range list {
			e, ok := byID[r.ChunkID]
			if !ok {
				e = &entry{result: r}
				byID[r.ChunkID] = e
			}
			e.score += 1.0 / float64(rrfK+rank+1)
		}
	}

	var fused []SearchResult
	for _, e := range byID {
		e.result.Score = e.score
		fused = append(fused, e.result)
	}
	sort.Slice(fused, func(i, j int) bool { return fused[i].Score > fused[j].Score })
	if len(fused) > limit {
		fused = fused[:limit]
	}
	return fused
}

func (svc *Service) rewriteQuery(ctx context.Context, query string) (string, error) {
	resp, err := svc.chat.Chat(ctx, []llm.ChatMessage{
		{Role: "system", Content: "把用户的搜索查询改写成一句独立、完整、无指代的检索查询。只输出改写后的查询本身，不要任何解释。"},
		{Role: "user", Content: query},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp), nil
}

func (svc *Service) rerank(ctx context.Context, query string, results []SearchResult, topK int) ([]SearchResult, error) {
	if len(results) > 20 {
		results = results[:20]
	}

	var sb strings.Builder
	for i, r := range results {
		snippet := r.Snippet
		if len(snippet) > 400 {
			snippet = snippet[:400]
		}
		fmt.Fprintf(&sb, "[%d] %s\n", i+1, snippet)
	}

	prompt := fmt.Sprintf("用户查询：%s\n\n候选片段：\n%s\n\n请按与查询的相关度从高到低排序，输出候选编号列表（JSON 数组，如 [3,1,2]），只输出 JSON。",
		query, sb.String())

	resp, err := svc.chat.Chat(ctx, []llm.ChatMessage{
		{Role: "system", Content: "你是检索结果重排助手。只输出 JSON 数组，不要任何其他文字。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, err
	}

	resp = strings.TrimSpace(resp)
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	var order []int
	if err := json.Unmarshal([]byte(resp), &order); err != nil {
		return nil, fmt.Errorf("parse rerank output: %w", err)
	}

	var reranked []SearchResult
	seen := make(map[int]bool)
	for _, idx := range order {
		if idx >= 1 && idx <= len(results) && !seen[idx-1] {
			seen[idx-1] = true
			reranked = append(reranked, results[idx-1])
		}
	}
	for i, r := range results {
		if !seen[i] {
			reranked = append(reranked, r)
		}
	}
	if len(reranked) > topK {
		reranked = reranked[:topK]
	}
	return reranked, nil
}

func (svc *Service) expandSnippet(ctx context.Context, chunkID int64) string {
	var docID int64
	var seq int
	err := svc.store.QueryRowContext(ctx,
		"SELECT document_id, seq FROM kb_chunks WHERE id = ?", chunkID).Scan(&docID, &seq)
	if err != nil {
		return ""
	}

	rows, err := svc.store.QueryContext(ctx,
		"SELECT content FROM kb_chunks WHERE document_id = ? AND seq >= ? AND seq <= ? ORDER BY seq",
		docID, seq-1, seq+1)
	if err != nil {
		return ""
	}
	defer rows.Close()

	var parts []string
	for rows.Next() {
		var p string
		if rows.Scan(&p) == nil {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "\n")
}

func containsInt64(list []int64, v int64) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (svc *Service) Chat(ctx context.Context, req *SearchRequest) (*ChatResponse, error) {
	if svc.chat == nil {
		return nil, fmt.Errorf("chat model not configured")
	}

	resp, err := svc.Search(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(resp.Results) == 0 {
		return &ChatResponse{
			Answer: "知识库中没有找到与问题相关的内容。",
			Citations: []Citation{},
		}, nil
	}

	var sb strings.Builder
	for _, r := range resp.Results {
		fmt.Fprintf(&sb, "[%d] 文档《%s》：\n%s\n\n", r.Citation.Index, r.Title, r.Snippet)
	}

	prompt := fmt.Sprintf("请仅根据以下知识库片段回答用户问题。回答中用 [n] 标记引用来源（n 是片段编号）。如果片段不足以回答，明确说明。\n\n知识库片段：\n%s\n\n用户问题：%s",
		sb.String(), req.Query)

	answer, err := svc.chat.Chat(ctx, []llm.ChatMessage{
		{Role: "system", Content: "你是一个严谨的个人知识库问答助手。只依据提供的片段回答，引用必须带 [n] 标记。"},
		{Role: "user", Content: prompt},
	})
	if err != nil {
		return nil, fmt.Errorf("chat: %w", err)
	}

	citations := make([]Citation, len(resp.Results))
	for i, r := range resp.Results {
		citations[i] = r.Citation
	}

	return &ChatResponse{Answer: answer, Citations: citations}, nil
}
