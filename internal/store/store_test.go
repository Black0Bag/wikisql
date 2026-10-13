package store

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func testStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	s, err := New(dbPath, testLogger())
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestNewStore(t *testing.T) {
	s := testStore(t)

	version, err := s.MigrationVersion(context.Background())
	if err != nil {
		t.Fatalf("MigrationVersion() error: %v", err)
	}
	if version < 1 {
		t.Errorf("expected migration version >= 1, got %d", version)
	}
}

func TestMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s1, err := New(dbPath, testLogger())
	if err != nil {
		t.Fatalf("first New() error: %v", err)
	}
	v1, _ := s1.MigrationVersion(context.Background())
	s1.Close()

	s2, err := New(dbPath, testLogger())
	if err != nil {
		t.Fatalf("second New() error: %v", err)
	}
	defer s2.Close()
	v2, _ := s2.MigrationVersion(context.Background())

	if v1 != v2 {
		t.Errorf("migration version changed after reopen: %d != %d", v1, v2)
	}
}

func TestBackupRestore(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_, err := s.ExecContext(ctx, "CREATE TABLE test_data (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	_, err = s.ExecContext(ctx, "INSERT INTO test_data (value) VALUES ('hello')")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	backupDir := filepath.Join(t.TempDir(), "backups")
	backupPath, err := s.Backup(ctx, backupDir)
	if err != nil {
		t.Fatalf("Backup() error: %v", err)
	}
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Fatalf("backup file does not exist: %s", backupPath)
	}

	restored, err := New(backupPath, testLogger())
	if err != nil {
		t.Fatalf("opening restored backup: %v", err)
	}
	defer restored.Close()

	var value string
	err = restored.QueryRowContext(ctx, "SELECT value FROM test_data LIMIT 1").Scan(&value)
	if err != nil {
		t.Fatalf("query restored data: %v", err)
	}
	if value != "hello" {
		t.Errorf("expected 'hello', got %q", value)
	}
}

func TestConcurrentWrites(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_, err := s.ExecContext(ctx, "CREATE TABLE counter (id INTEGER PRIMARY KEY, n INTEGER)")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func(n int) {
			for j := 0; j < 10; j++ {
				_, err := s.ExecContext(ctx,
					"INSERT INTO counter (n) VALUES (?)", n*10+j)
				if err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(i)
	}

	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent write error: %v", err)
		}
	}

	var count int64
	err = s.QueryRowContext(ctx, "SELECT COUNT(*) FROM counter").Scan(&count)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 80 {
		t.Errorf("expected 80 rows, got %d", count)
	}
}

func TestListTables(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()

	_, err := s.ExecContext(ctx, "CREATE TABLE my_custom_table (id INTEGER)")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	tables, err := s.ListTables(ctx)
	if err != nil {
		t.Fatalf("ListTables: %v", err)
	}

	found := false
	for _, name := range tables {
		if name == "my_custom_table" {
			found = true
		}
		if name == "sqlite_sequence" {
			t.Errorf("sqlite internal table should not be listed: %s", name)
		}
	}
	if !found {
		t.Errorf("expected my_custom_table in %v", tables)
	}
}

func TestIntegrityCheck(t *testing.T) {
	s := testStore(t)
	result, err := s.IntegrityCheck(context.Background())
	if err != nil {
		t.Fatalf("IntegrityCheck: %v", err)
	}
	if result != "ok" {
		t.Errorf("expected 'ok', got %q", result)
	}
}
