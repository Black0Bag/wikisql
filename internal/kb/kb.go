package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/1Vewton/textsplitter/recursivesplitter"
	"github.com/aicode/kb-server/internal/llm"
	"github.com/aicode/kb-server/internal/store"
)

type Options struct {
	ChunkMaxRunes     int
	ContextHeaders    bool
	EmbedBatchSize    int
}

type Service struct {
	store     *store.Store
	embed     llm.EmbedClient
	chat      llm.ChatClient
	opts      Options
	logger    *slog.Logger
	splitter  *recursivesplitter.RecursiveSplitter
}

func New(s *store.Store, embed llm.EmbedClient, chat llm.ChatClient, opts Options, logger *slog.Logger) (*Service, error) {
	if opts.ChunkMaxRunes <= 0 {
		opts.ChunkMaxRunes = 1000
	}
	if opts.EmbedBatchSize <= 0 {
		opts.EmbedBatchSize = 32
	}

	svc := &Service{
		store:  s,
		embed:  embed,
		chat:   chat,
		opts:   opts,
		logger: logger,
	}
	svc.splitter = recursivesplitter.NewRecursiveSplitter(opts.ChunkMaxRunes, 0, nil)

	// 维度未设时由 InitDimension 稍后建表；已设时立即校验并建表。
	var dimStr string
	err := s.QueryRowContext(context.Background(),
		"SELECT value FROM kb_meta WHERE key = 'vec_dimension'").Scan(&dimStr)
	if err == nil {
		if err := svc.ensureVecTable(context.Background()); err != nil {
			return nil, err
		}
	}
	return svc, nil
}

func (svc *Service) ensureVecTable(ctx context.Context) error {
	var dimStr string
	err := svc.store.QueryRowContext(ctx,
		"SELECT value FROM kb_meta WHERE key = 'vec_dimension'").Scan(&dimStr)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("vec_dimension not set; cannot create vector table")
	}
	if err != nil {
		return fmt.Errorf("reading vec dimension: %w", err)
	}

	var count int
	err = svc.store.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='kb_chunks_vec'").Scan(&count)
	if err != nil {
		return fmt.Errorf("checking vec table: %w", err)
	}
	if count > 0 {
		return nil
	}

	_, err = svc.store.ExecContext(ctx, fmt.Sprintf(
		"CREATE VIRTUAL TABLE kb_chunks_vec USING vec0(embedding float[%s])", dimStr))
	if err != nil {
		return fmt.Errorf("creating vec0 table: %w", err)
	}
	svc.logger.Info("vector table created", "dimension", dimStr)
	return nil
}

// DimensionReady 报告向量索引是否已初始化。
func (svc *Service) DimensionReady(ctx context.Context) bool {
	var count int
	err := svc.store.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM kb_meta WHERE key = 'vec_dimension'").Scan(&count)
	return err == nil && count > 0
}

func (svc *Service) InitDimension(ctx context.Context, dimension int) error {
	var existing string
	err := svc.store.QueryRowContext(ctx,
		"SELECT value FROM kb_meta WHERE key = 'vec_dimension'").Scan(&existing)
	if err == nil {
		if existing != fmt.Sprintf("%d", dimension) {
			return fmt.Errorf("embedding dimension mismatch: index built with %s, config says %d; reindex required", existing, dimension)
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("reading vec dimension: %w", err)
	}

	_, err = svc.store.ExecContext(ctx,
		"INSERT INTO kb_meta (key, value) VALUES ('vec_dimension', ?)", fmt.Sprintf("%d", dimension))
	if err != nil {
		return fmt.Errorf("recording vec dimension: %w", err)
	}
	return svc.ensureVecTable(ctx)
}

type Collection struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
}

func (svc *Service) CreateCollection(ctx context.Context, name, description string) (*Collection, error) {
	if strings.TrimSpace(name) == "" {
		return nil, NewValidationError("collection name must not be empty")
	}
	res, err := svc.store.ExecContext(ctx,
		"INSERT INTO kb_collections (name, description) VALUES (?, ?)", name, description)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, NewValidationError(fmt.Sprintf("collection %q already exists", name))
		}
		return nil, fmt.Errorf("creating collection: %w", err)
	}
	id, _ := res.LastInsertId()
	return &Collection{ID: id, Name: name, Description: description}, nil
}

func (svc *Service) ListCollections(ctx context.Context) ([]Collection, error) {
	rows, err := svc.store.QueryContext(ctx,
		"SELECT id, name, description, created_at FROM kb_collections ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.CreatedAt); err != nil {
			return nil, err
		}
		cols = append(cols, c)
	}
	return cols, nil
}

func (svc *Service) DeleteCollection(ctx context.Context, id int64) error {
	rows, err := svc.store.QueryContext(ctx,
		"SELECT id FROM kb_chunks WHERE document_id IN (SELECT id FROM kb_documents WHERE collection_id = ?)", id)
	if err != nil {
		return err
	}
	var chunkIDs []int64
	for rows.Next() {
		var cid int64
		if err := rows.Scan(&cid); err != nil {
			rows.Close()
			return err
		}
		chunkIDs = append(chunkIDs, cid)
	}
	rows.Close()

	for _, cid := range chunkIDs {
		svc.store.ExecContext(ctx, "DELETE FROM kb_chunks_fts WHERE rowid = ?", cid)
		svc.store.ExecContext(ctx, "DELETE FROM kb_chunks_vec WHERE rowid = ?", cid)
	}

	if _, err := svc.store.ExecContext(ctx, "DELETE FROM kb_collections WHERE id = ?", id); err != nil {
		return fmt.Errorf("deleting collection: %w", err)
	}
	return nil
}

type Document struct {
	ID           int64  `json:"id"`
	CollectionID int64  `json:"collection_id"`
	Title        string `json:"title"`
	SourceType   string `json:"source_type"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	ChunkCount   int    `json:"chunk_count"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

func (svc *Service) GetDocument(ctx context.Context, id int64) (*Document, error) {
	var d Document
	err := svc.store.QueryRowContext(ctx,
		`SELECT id, collection_id, title, source_type, status,
		        COALESCE(error,''), chunk_count, created_at, updated_at
		 FROM kb_documents WHERE id = ?`, id,
	).Scan(&d.ID, &d.CollectionID, &d.Title, &d.SourceType, &d.Status, &d.Error, &d.ChunkCount, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return &d, err
}

func (svc *Service) setStatus(ctx context.Context, docID int64, status, errMsg string) {
	_, err := svc.store.ExecContext(ctx,
		"UPDATE kb_documents SET status = ?, error = ?, updated_at = datetime('now') WHERE id = ?",
		status, errMsg, docID)
	if err != nil {
		svc.logger.Error("failed to update document status", "doc", docID, "err", err)
	}
}

// IngestDocument 走完整状态机：pending → chunking → embedding → ready / failed。
func (svc *Service) IngestDocument(ctx context.Context, collectionID int64, title, content, sourceType, sourcePath string) (*Document, error) {
	contentHash := hashString(content)

	var existing int64
	err := svc.store.QueryRowContext(ctx,
		"SELECT id FROM kb_documents WHERE collection_id = ? AND content_hash = ?",
		collectionID, contentHash).Scan(&existing)
	if err == nil {
		return svc.GetDocument(ctx, existing)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("checking duplicate: %w", err)
	}

	res, err := svc.store.ExecContext(ctx,
		`INSERT INTO kb_documents (collection_id, title, source_type, source_path, content_hash, status)
		 VALUES (?, ?, ?, ?, ?, 'pending')`,
		collectionID, title, sourceType, sourcePath, contentHash)
	if err != nil {
		return nil, fmt.Errorf("inserting document: %w", err)
	}
	docID, _ := res.LastInsertId()

	if err := svc.processDocument(ctx, docID, title, content); err != nil {
		svc.setStatus(ctx, docID, "failed", err.Error())
		return svc.GetDocument(ctx, docID)
	}
	return svc.GetDocument(ctx, docID)
}

// RetryDocument 从分块阶段重跑失败的文档。
func (svc *Service) RetryDocument(ctx context.Context, docID int64) (*Document, error) {
	var title, content string
	err := svc.store.QueryRowContext(ctx,
		`SELECT d.title, COALESCE(f.content, '')
		 FROM kb_documents d
		 LEFT JOIN kb_files f ON f.document_id = d.id
		 WHERE d.id = ?`, docID).Scan(&title, &content)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	svc.deleteDocIndexes(ctx, docID)
	if err := svc.processDocument(ctx, docID, title, content); err != nil {
		svc.setStatus(ctx, docID, "failed", err.Error())
		return svc.GetDocument(ctx, docID)
	}
	return svc.GetDocument(ctx, docID)
}

func (svc *Service) deleteDocIndexes(ctx context.Context, docID int64) {
	rows, err := svc.store.QueryContext(ctx,
		"SELECT id FROM kb_chunks WHERE document_id = ?", docID)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var cid int64
		rows.Scan(&cid)
		ids = append(ids, cid)
	}
	rows.Close()
	for _, cid := range ids {
		svc.store.ExecContext(ctx, "DELETE FROM kb_chunks_fts WHERE rowid = ?", cid)
		svc.store.ExecContext(ctx, "DELETE FROM kb_chunks_vec WHERE rowid = ?", cid)
	}
	svc.store.ExecContext(ctx, "DELETE FROM kb_chunks WHERE document_id = ?", docID)
}

func (svc *Service) DeleteDocument(ctx context.Context, docID int64) error {
	svc.deleteDocIndexes(ctx, docID)
	if _, err := svc.store.ExecContext(ctx, "DELETE FROM kb_documents WHERE id = ?", docID); err != nil {
		return fmt.Errorf("deleting document: %w", err)
	}
	return nil
}

func (svc *Service) processDocument(ctx context.Context, docID int64, title, content string) error {
	svc.setStatus(ctx, docID, "chunking", "")

	chunks, err := svc.splitter.SplitText(ctx, content)
	if err != nil {
		return fmt.Errorf("splitting text: %w", err)
	}
	if len(chunks) == 0 {
		return fmt.Errorf("document produced no chunks")
	}

	type chunkRow struct {
		id    int64
		hash  string
		text  string
	}
	var chunkRows []chunkRow

	for i, ch := range chunks {
		ch = strings.TrimSpace(ch)
		if ch == "" {
			continue
		}
		hash := hashString(ch)
		res, err := svc.store.ExecContext(ctx,
			`INSERT INTO kb_chunks (document_id, seq, content, token_count, content_hash)
			 VALUES (?, ?, ?, ?, ?)`,
			docID, i, ch, utf8.RuneCountInString(ch), hash)
		if err != nil {
			return fmt.Errorf("inserting chunk %d: %w", i, err)
		}
		id, _ := res.LastInsertId()
		chunkRows = append(chunkRows, chunkRow{id: id, hash: hash, text: ch})
	}

	if _, err := svc.store.ExecContext(ctx,
		"UPDATE kb_documents SET chunk_count = ?, updated_at = datetime('now') WHERE id = ?",
		len(chunkRows), docID); err != nil {
		return err
	}

	svc.setStatus(ctx, docID, "embedding", "")

	batchSize := svc.opts.EmbedBatchSize
	for start := 0; start < len(chunkRows); start += batchSize {
		end := start + batchSize
		if end > len(chunkRows) {
			end = len(chunkRows)
		}
		batch := chunkRows[start:end]

		texts := make([]string, len(batch))
		for j, cr := range batch {
			texts[j] = cr.text
		}

		vectors, err := svc.embedWithCache(ctx, texts)
		if err != nil {
			return fmt.Errorf("embedding batch %d-%d: %w", start, end, err)
		}

		for j, cr := range batch {
			if _, err := svc.store.ExecContext(ctx,
				"INSERT INTO kb_chunks_vec (rowid, embedding) VALUES (?, ?)",
				cr.id, encodeVector(vectors[j])); err != nil {
				return fmt.Errorf("inserting vector for chunk %d: %w", cr.id, err)
			}
			tok, err := Tokenize(cr.text)
			if err != nil {
				return fmt.Errorf("tokenizing chunk %d: %w", cr.id, err)
			}
			if _, err := svc.store.ExecContext(ctx,
				"INSERT INTO kb_chunks_fts (rowid, content) VALUES (?, ?)",
				cr.id, tok); err != nil {
				return fmt.Errorf("inserting fts for chunk %d: %w", cr.id, err)
			}
		}
	}

	svc.setStatus(ctx, docID, "ready", "")
	svc.logger.Info("document ingested", "doc", docID, "title", title, "chunks", len(chunkRows))
	return nil
}

func (svc *Service) embedWithCache(ctx context.Context, texts []string) ([][]float32, error) {
	hashes := make([]string, len(texts))
	vectors := make([][]float32, len(texts))
	var needIdx []int

	for i, t := range texts {
		hashes[i] = hashString(t)
		var blob []byte
		err := svc.store.QueryRowContext(ctx,
			"SELECT vector FROM kb_embed_cache WHERE content_hash = ?", hashes[i]).Scan(&blob)
		if err == nil {
			vectors[i] = decodeVector(blob)
			continue
		}
		needIdx = append(needIdx, i)
	}

	if len(needIdx) > 0 {
		needTexts := make([]string, len(needIdx))
		for j, i := range needIdx {
			needTexts[j] = texts[i]
		}
		newVectors, err := svc.embed.Embed(ctx, needTexts)
		if err != nil {
			return nil, err
		}
		for j, i := range needIdx {
			vectors[i] = newVectors[j]
			blob := encodeVector(newVectors[j])
			svc.store.ExecContext(ctx,
				"INSERT OR REPLACE INTO kb_embed_cache (content_hash, vector) VALUES (?, ?)",
				hashes[i], blob)
		}
	}
	return vectors, nil
}

func (svc *Service) SaveFileContent(ctx context.Context, docID int64, content string) error {
	_, err := svc.store.ExecContext(ctx,
		"INSERT OR REPLACE INTO kb_files (document_id, content) VALUES (?, ?)",
		docID, content)
	return err
}
