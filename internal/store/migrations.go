package store

import (
	"context"
	"fmt"
	"strings"
)

var migrations = []string{
	// 001: schema_migrations, audit_log, health_checks
	`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS audit_log (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		action TEXT NOT NULL,
		table_name TEXT,
		details TEXT,
		row_count INTEGER
	);

	CREATE TABLE IF NOT EXISTS health_checks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		check_name TEXT NOT NULL,
		status TEXT NOT NULL,
		severity TEXT NOT NULL DEFAULT 'info',
		message TEXT,
		details TEXT
	);

	CREATE INDEX IF NOT EXISTS idx_audit_log_created ON audit_log(created_at);
	CREATE INDEX IF NOT EXISTS idx_health_checks_created ON health_checks(created_at);
	CREATE INDEX IF NOT EXISTS idx_health_checks_status ON health_checks(status);`,

	// 002: knowledge base relational tables (FTS5 and vec0 virtual tables are created separately)
	`CREATE TABLE IF NOT EXISTS kb_collections (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL UNIQUE,
		description TEXT DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS kb_documents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		collection_id INTEGER NOT NULL REFERENCES kb_collections(id) ON DELETE CASCADE,
		title TEXT NOT NULL,
		source_type TEXT NOT NULL DEFAULT 'text',
		source_path TEXT DEFAULT '',
		content_hash TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending',
		error TEXT DEFAULT '',
		chunk_count INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS kb_chunks (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		document_id INTEGER NOT NULL REFERENCES kb_documents(id) ON DELETE CASCADE,
		seq INTEGER NOT NULL,
		content TEXT NOT NULL,
		token_count INTEGER NOT NULL DEFAULT 0,
		content_hash TEXT NOT NULL,
		context_header TEXT DEFAULT '',
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS kb_embed_cache (
		content_hash TEXT PRIMARY KEY,
		vector BLOB NOT NULL,
		created_at TEXT NOT NULL DEFAULT (datetime('now'))
	);

	CREATE TABLE IF NOT EXISTS kb_meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE IF NOT EXISTS kb_files (
		document_id INTEGER PRIMARY KEY REFERENCES kb_documents(id) ON DELETE CASCADE,
		content TEXT NOT NULL
	);

	CREATE VIRTUAL TABLE IF NOT EXISTS kb_chunks_fts USING fts5(content);

	CREATE INDEX IF NOT EXISTS idx_kb_documents_collection ON kb_documents(collection_id);
	CREATE INDEX IF NOT EXISTS idx_kb_documents_hash ON kb_documents(collection_id, content_hash);
	CREATE INDEX IF NOT EXISTS idx_kb_chunks_document ON kb_chunks(document_id, seq);`,
}

func (s *Store) runMigrations(ctx context.Context) error {
	if _, err := s.writer.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL DEFAULT (datetime('now'))
		)
	`); err != nil {
		return fmt.Errorf("creating migrations table: %w", err)
	}

	for i, migration := range migrations {
		version := i + 1
		var exists int
		err := s.writer.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE version = ?", version,
		).Scan(&exists)
		if err != nil {
			return fmt.Errorf("checking migration %d: %w", version, err)
		}
		if exists > 0 {
			continue
		}

		tx, err := s.writer.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("beginning migration %d transaction: %w", version, err)
		}

		stmts := strings.Split(migration, ";")
		for _, stmt := range stmts {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d statement failed: %w", version, err)
			}
		}

		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version) VALUES (?)", version,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("recording migration %d: %w", version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("committing migration %d: %w", version, err)
		}

		s.logger.Info("migration applied", "version", version)
	}

	return nil
}

func (s *Store) MigrationVersion(ctx context.Context) (int, error) {
	var version int
	err := s.writer.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) FROM schema_migrations",
	).Scan(&version)
	return version, err
}
