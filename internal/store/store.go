package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

type Store struct {
	writer *sql.DB
	reader *sql.DB
	logger *slog.Logger
	dbPath string
}

func New(dbPath string, logger *slog.Logger) (*Store, error) {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0750); err != nil {
		return nil, fmt.Errorf("creating database directory %s: %w", dir, err)
	}

pragmaParams := url.Values{}
pragmaParams.Add("_pragma", "journal_mode(WAL)")
pragmaParams.Add("_pragma", "busy_timeout(5000)")
pragmaParams.Add("_pragma", "foreign_keys(1)")
pragmaParams.Add("_pragma", "synchronous(NORMAL)")

writerDSN := fmt.Sprintf("file:%s?_txlock=immediate&%s", dbPath, pragmaParams.Encode())
readerDSN := fmt.Sprintf("file:%s?%s", dbPath, pragmaParams.Encode())

writer, err := sql.Open("sqlite", writerDSN)
if err != nil {
	return nil, fmt.Errorf("opening writer connection: %w", err)
}
writer.SetMaxOpenConns(1)
writer.SetMaxIdleConns(1)
writer.SetConnMaxLifetime(0)

	reader, err := sql.Open("sqlite", readerDSN)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("opening reader connection: %w", err)
	}
	reader.SetMaxOpenConns(4)
	reader.SetMaxIdleConns(4)
	reader.SetConnMaxLifetime(5 * time.Minute)

	if err := writer.PingContext(context.Background()); err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	s := &Store{
		writer: writer,
		reader: reader,
		logger: logger,
		dbPath: dbPath,
	}

	if err := s.runMigrations(context.Background()); err != nil {
		writer.Close()
		reader.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	var errs []error
	if err := s.writer.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing writer: %w", err))
	}
	if err := s.reader.Close(); err != nil {
		errs = append(errs, fmt.Errorf("closing reader: %w", err))
	}
	if len(errs) > 0 {
		return fmt.Errorf("store close errors: %v", errs)
	}
	return nil
}

func (s *Store) Writer() *sql.DB {
	return s.writer
}

func (s *Store) Reader() *sql.DB {
	return s.reader
}

func (s *Store) DBPath() string {
	return s.dbPath
}

func (s *Store) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return s.writer.ExecContext(ctx, query, args...)
}

func (s *Store) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return s.reader.QueryContext(ctx, query, args...)
}

func (s *Store) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return s.reader.QueryRowContext(ctx, query, args...)
}

func (s *Store) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return s.writer.BeginTx(ctx, opts)
}
